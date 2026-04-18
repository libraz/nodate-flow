// Package router assembles the nodate-flow HTTP API router. It exists
// so that both cmd/api/main.go and the integration test harness
// (apps/api/tests/helpers) can mount the exact same route set against a
// real *sql.DB without duplicating the wiring in two places.
//
// The router intentionally takes its dependencies as an explicit Deps
// struct rather than reading environment variables, so tests can
// construct it with a fixed test cipher and an empty default workspace
// id.
package router

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"github.com/danielgtaylor/huma/v2/adapters/humachi"
	"github.com/go-chi/chi/v5"

	"github.com/nodate-flow/nodate-flow/apps/api/internal/ai"
	"github.com/nodate-flow/nodate-flow/apps/api/internal/ai/agentruntime"
	"github.com/nodate-flow/nodate-flow/apps/api/internal/ai/embed"
	"github.com/nodate-flow/nodate-flow/apps/api/internal/ai/nlcommand"
	"github.com/nodate-flow/nodate-flow/apps/api/internal/ai/nlconstraint"
	"github.com/nodate-flow/nodate-flow/apps/api/internal/ai/nlquery"
	"github.com/nodate-flow/nodate-flow/apps/api/internal/ai/providers"
	airelations "github.com/nodate-flow/nodate-flow/apps/api/internal/ai/relations"
	"github.com/nodate-flow/nodate-flow/apps/api/internal/audit"
	"github.com/nodate-flow/nodate-flow/apps/api/internal/auth"
	"github.com/nodate-flow/nodate-flow/apps/api/internal/auth/sessionstore"
	"github.com/nodate-flow/nodate-flow/apps/api/internal/crypto"
	"github.com/nodate-flow/nodate-flow/apps/api/internal/db/generated"
	"github.com/nodate-flow/nodate-flow/apps/api/internal/db/types"
	"github.com/nodate-flow/nodate-flow/apps/api/internal/eventbus"
	aihandlers "github.com/nodate-flow/nodate-flow/apps/api/internal/http/handlers/ai"
	authhandlers "github.com/nodate-flow/nodate-flow/apps/api/internal/http/handlers/auth"
	"github.com/nodate-flow/nodate-flow/apps/api/internal/http/handlers/dashboard"
	exporthandlers "github.com/nodate-flow/nodate-flow/apps/api/internal/http/handlers/export"
	"github.com/nodate-flow/nodate-flow/apps/api/internal/http/handlers/inbox"
	integrationshandlers "github.com/nodate-flow/nodate-flow/apps/api/internal/http/handlers/integrations"
	"github.com/nodate-flow/nodate-flow/apps/api/internal/http/handlers/lenses"
	"github.com/nodate-flow/nodate-flow/apps/api/internal/http/handlers/notifications"
	"github.com/nodate-flow/nodate-flow/apps/api/internal/http/handlers/pages"
	"github.com/nodate-flow/nodate-flow/apps/api/internal/http/handlers/projects"
	"github.com/nodate-flow/nodate-flow/apps/api/internal/http/handlers/relations"
	"github.com/nodate-flow/nodate-flow/apps/api/internal/http/handlers/signals"
	"github.com/nodate-flow/nodate-flow/apps/api/internal/http/handlers/tasks"
	"github.com/nodate-flow/nodate-flow/apps/api/internal/http/handlers/timeboxes"
	"github.com/nodate-flow/nodate-flow/apps/api/internal/http/handlers/timeline"
	"github.com/nodate-flow/nodate-flow/apps/api/internal/http/handlers/webhooks"
	"github.com/nodate-flow/nodate-flow/apps/api/internal/http/handlers/workspaces"
	"github.com/nodate-flow/nodate-flow/apps/api/internal/http/middleware"
	integrationspkg "github.com/nodate-flow/nodate-flow/apps/api/internal/integrations"
	"github.com/nodate-flow/nodate-flow/apps/api/internal/integrations/email"
	"github.com/nodate-flow/nodate-flow/apps/api/internal/mcp"
	"github.com/nodate-flow/nodate-flow/apps/api/internal/obs"
	"github.com/nodate-flow/nodate-flow/apps/api/internal/storage"
	"github.com/nodate-flow/nodate-flow/apps/api/internal/stream"
)

// Deps is the dependency bundle Build needs in order to wire every
// route in the API. All fields are required except Cipher and
// DefaultWorkspaceID.
type Deps struct {
	// DB is the live database handle. It MUST be non-nil in tests.
	DB *sql.DB
	// Queries is a sqlc Queries handle wrapping DB. Callers should pass
	// generated.New(DB) so both handles share the same connection pool.
	Queries *generated.Queries
	// JWT is the access-token issuer used by RequireAuth and by the auth
	// handlers themselves.
	JWT *auth.JWTIssuer
	// Sessions is the refresh-token session store driver. When nil,
	// [Build] falls back to [sessionstore.NewMySQLStore] over the
	// sqlc query handle so tests do not need to wire it explicitly.
	Sessions sessionstore.Store
	// Cipher is optional: when nil, the AI provider endpoints return
	// AI.PROVIDER.NOT_CONFIGURED and the MCP propose_* tools are degraded,
	// but the rest of the API still boots. Tests typically pass a fixed
	// 32-byte key cipher so the AI-provider and MCP tests can exercise
	// encryption end to end.
	Cipher *crypto.Cipher
	// GhWebhookSecret is the shared HMAC secret for the GitHub webhook.
	// Empty in tests.
	GhWebhookSecret string
	// SlackSigningSecret is the v0 signing secret for the Slack webhook.
	// Empty in tests.
	SlackSigningSecret string
	// GoogleChannelToken authenticates inbound Google Drive push notifications.
	GoogleChannelToken string
	// DefaultWorkspaceID is the workspace public id (UUID v7) that
	// webhook-origin signals are routed to. Empty in tests.
	DefaultWorkspaceID string
	// CookieSecure toggles the Secure flag on the refresh cookie. Tests
	// leave it false so http://127.0.0.1 traffic works; the prod main
	// wires it from cfg.CookieSecure.
	CookieSecure bool
	// RegistrationOpen controls whether POST /auth/register is allowed.
	// When false the handler returns 403. Defaults to true.
	RegistrationOpen bool
	// AiMock toggles the deterministic in-memory AI provider used by
	// development and tests. When true the orchestrator routes
	// every workspace to a fixture-backed Provider regardless of the
	// workspace.ai_providers rows.
	AiMock bool
	// StreamNotifier is the realtime fan-out for SSE subscribers.
	// Nil means realtime is disabled: the SSE route still mounts
	// but serves only heartbeats, and eventbus.Append does not
	// publish. Tests that don't care pass nil.
	StreamNotifier stream.Notifier
	// StreamRemember is the callback the SSE handler uses to teach
	// the eventbus tap the internal→public workspace id mapping.
	// May be nil when StreamNotifier is nil.
	StreamRemember stream.RememberWorkspaceFunc
	// AiInvocationPublisher is called after every successful
	// ai_invocations write so SSE subscribers see the
	// ai.invocation.written marker without depending on eventbus.
	// Nil means the hook is skipped. Tests typically leave this nil.
	AiInvocationPublisher func(context.Context, uint32)

	// AgentQueue is the queue the manual-trigger endpoint enqueues
	// runs into when the api is running in multi-replica mode. Nil
	// in single-process deployments and tests; the handler falls
	// back to dispatching AgentRunner synchronously so manual
	// triggers still work without an agent_runs table.
	AgentQueue agentruntime.Queue
	// AgentRunner is the synchronous Runner used by the manual
	// trigger when AgentQueue is nil. Leaving both nil forces the
	// handler to return AI.AGENT.RUNTIME_DISABLED so operators
	// notice misconfiguration instead of a silent no-op.
	AgentRunner agentruntime.Runner

	// Storage is the S3-compatible object store client for file
	// uploads / downloads. Nil in tests; presign endpoints return
	// INTERNAL.UNEXPECTED.
	Storage *storage.Client

	// EmailSender is the outbound email transport. Nil or a NoopSender
	// when SMTP is not configured; handlers should check for
	// email.ErrNotConfigured on Send failures. Tests typically pass
	// an email.MemorySender.
	EmailSender email.Sender

	// EmbedOpenAIKey is the plaintext OpenAI API key for the embedding
	// pipeline. When non-empty, the router uses embed.OpenAIProvider
	// instead of the mock. Empty in tests (mock is used).
	EmbedOpenAIKey string
	// EmbedModel overrides the default embedding model. Empty means
	// text-embedding-3-small.
	EmbedModel string
	// EmbedBaseURL overrides the embeddings API base URL.
	EmbedBaseURL string

	// Integrations is the personal-OAuth provider registry (GitHub /
	// Slack / Google Calendar). Nil in tests; the handlers degrade
	// gracefully by returning INTEGRATION.OAUTH.PROVIDER_NOT_CONFIGURED.
	Integrations *integrationspkg.Registry
	// PublicBaseURL is the origin used to build OAuth callback URLs
	// (e.g. https://api.example.com + /oauth/callback/github).
	PublicBaseURL string
	// WebBaseURL is where the OAuth callback handler bounces the user
	// back to after writing the user_integrations row.
	WebBaseURL string
}

// Result is what BuildResult returns: the composed chi router plus the
// list of huma.API instances that were registered against it. The
// dump-openapi command merges each API's OpenAPI document into a single
// spec for TypeScript SDK generation.
type Result struct {
	Handler http.Handler
	APIs    []huma.API
}

// Build mounts every nodate-flow API route onto a fresh chi router and
// returns it as an http.Handler. It is a thin wrapper around BuildResult
// for callers (cmd/api, tests) that only need the handler.
func Build(deps Deps) http.Handler {
	return BuildResult(deps).Handler
}

// BuildResult mounts every nodate-flow API route onto a fresh chi router
// and returns the handler together with the list of huma.API instances
// used. It mirrors cmd/api/main.go so that the integration test harness
// can exercise the full API surface without duplicating the wiring.
func BuildResult(deps Deps) Result {
	r := chi.NewRouter()
	// Outermost layer: extract and stash the client IP so auth
	// handlers can record it on new sessions without re-parsing
	// X-Forwarded-For themselves.
	r.Use(middleware.ClientIP())
	// Security response headers: CSP, HSTS, X-Content-Type-Options, etc.
	r.Use(middleware.SecurityHeaders())
	// Each huma.API needs its own huma.Config so it gets a fresh
	// schema registry and its own *OpenAPI document; sharing one
	// config between sub-APIs would point every group at the same
	// registry and panic on duplicate anonymous "ListOutputBody" style
	// schema names.
	newConfig := func() huma.Config {
		return huma.DefaultConfig("nodate-flow", "0.0.0")
	}
	api := humachi.New(r, newConfig())
	apis := []huma.API{api}
	newSubAPI := func(sub chi.Router) huma.API {
		a := humachi.New(sub, newConfig())
		apis = append(apis, a)
		return a
	}

	// Health endpoint.
	huma.Register(api, huma.Operation{
		OperationID: "health",
		Method:      http.MethodGet,
		Path:        "/health",
		Summary:     "Health check",
	}, func(_ context.Context, _ *struct{}) (*healthOutput, error) {
		out := &healthOutput{}
		out.Body.Status = "ok"
		return out, nil
	})

	sessionStore := deps.Sessions
	if sessionStore == nil {
		sessionStore = sessionstore.NewMySQLStore(deps.Queries)
	}
	auditRec := audit.New(deps.Queries)
	authDeps := authhandlers.Deps{DB: deps.DB, Queries: deps.Queries, Sessions: sessionStore, JWT: deps.JWT, Cipher: deps.Cipher, CookieSecure: deps.CookieSecure, RegistrationOpen: deps.RegistrationOpen, Audit: auditRec}
	integrationsDeps := integrationshandlers.Deps{
		DB:            deps.DB,
		Queries:       deps.Queries,
		Cipher:        deps.Cipher,
		Registry:      deps.Integrations,
		PublicBaseURL: deps.PublicBaseURL,
		WebBaseURL:    deps.WebBaseURL,
	}
	// Public auth endpoints (login / register) are behind a per-IP rate
	// limiter to slow down brute-force and credential-stuffing attacks.
	authRateLimiter := middleware.NewIPRateLimiter(middleware.RateLimitConfig{
		MaxRequests: 5,
		Window:      15 * time.Minute,
	})
	r.Group(func(sub chi.Router) {
		sub.Use(authRateLimiter.Middleware())
		authRateLimitedAPI := newSubAPI(sub)
		registerPublicAuthRoutes(authRateLimitedAPI, authDeps)
	})
	huma.Register(api, huma.Operation{
		OperationID: "oauth-integration-callback",
		Method:      http.MethodGet,
		Path:        "/oauth/callback/{provider}",
		Summary:     "Complete a personal OAuth integration flow",
	}, integrationshandlers.Callback(integrationsDeps))

	authMW := middleware.RequireAuth(middleware.AuthDeps{
		JWT:     deps.JWT,
		Queries: deps.Queries,
		DB:      passthroughDB{deps.DB},
	})
	aclDB := passthroughDB{deps.DB}
	wsDeps := workspaces.Deps{DB: deps.DB, Queries: deps.Queries, Audit: auditRec}
	prjDeps := projects.Deps{DB: deps.DB, Queries: deps.Queries, Audit: auditRec}
	// Write-time embedding client (ADR 0003). Uses the OpenAI provider
	// when NF_EMBED_OPENAI_KEY is set, otherwise the deterministic mock.
	var embedClient *embed.Client
	var nlQueryCompiler *nlquery.Compiler
	var nlConstraintCompiler *nlconstraint.Compiler
	var nlCommandResolver *nlcommand.Resolver
	_ = nlCommandResolver // used by a handler being wired in a parallel branch
	switch {
	case deps.AiMock:
		embedClient = embed.New(embed.NewMockProvider(), deps.Queries)
		nlQueryCompiler = nlquery.New(nlquery.NewMockProvider())
		nlConstraintCompiler = nlconstraint.New(nlconstraint.NewMockProvider())
		nlCommandResolver = nlcommand.New(nlcommand.NewMockProvider(), nil)
		nlCommandResolver.Cache = nlcommand.NewCache(5 * time.Minute)
	case deps.EmbedOpenAIKey != "":
		var opts []embed.OpenAIOption
		if deps.EmbedModel != "" {
			opts = append(opts, embed.WithOpenAIModel(deps.EmbedModel))
		}
		if deps.EmbedBaseURL != "" {
			opts = append(opts, embed.WithOpenAIBaseURL(deps.EmbedBaseURL))
		}
		embedClient = embed.New(embed.NewOpenAIProvider(deps.EmbedOpenAIKey, opts...), deps.Queries)
	}
	// When a real cipher is available but the NL compilers were not
	// set up by the embed-key path above, wire them to the workspace's
	// configured AI provider so the AI endpoints work without
	// NF_AI_MOCK.
	if deps.Cipher != nil {
		wsResolver := providers.NewWorkspaceResolver(deps.Queries, deps.Cipher)
		extractWS := func(ctx context.Context) (uint32, bool) {
			ws, ok := middleware.WorkspaceFromContext(ctx)
			return ws.ID, ok
		}
		if nlQueryCompiler == nil {
			nlQueryCompiler = nlquery.New(nlquery.NewWorkspaceProvider(wsResolver, extractWS))
		}
		if nlConstraintCompiler == nil {
			nlConstraintCompiler = nlconstraint.New(nlconstraint.NewWorkspaceProvider(wsResolver, extractWS))
		}
		if nlCommandResolver == nil {
			tools := []nlcommand.ToolSpec{
				{Name: "create_task", Description: "Create a new task", InputSchema: map[string]any{"type": "object", "properties": map[string]any{"title": map[string]any{"type": "string"}, "priority": map[string]any{"type": "integer", "minimum": 1, "maximum": 3}}}},
				{Name: "update_task", Description: "Update an existing task", InputSchema: map[string]any{"type": "object", "properties": map[string]any{"taskId": map[string]any{"type": "string"}, "title": map[string]any{"type": "string"}, "status": map[string]any{"type": "string"}}}},
				{Name: "search_tasks", Description: "Search tasks by query", InputSchema: map[string]any{"type": "object", "properties": map[string]any{"query": map[string]any{"type": "string"}}}},
				{Name: "propose_lens", Description: "Propose a filter lens from natural language", InputSchema: map[string]any{"type": "object", "properties": map[string]any{"prompt": map[string]any{"type": "string"}}}},
				{Name: "add_comment", Description: "Add a comment to a task", InputSchema: map[string]any{"type": "object", "properties": map[string]any{"taskId": map[string]any{"type": "string"}, "body": map[string]any{"type": "string"}}}},
				{Name: "list_tasks", Description: "List tasks in a project", InputSchema: map[string]any{"type": "object", "properties": map[string]any{"projectId": map[string]any{"type": "string"}}}},
				{Name: "list_projects", Description: "List projects in a workspace", InputSchema: map[string]any{"type": "object", "properties": map[string]any{}}},
			}
			nlCommandResolver = nlcommand.New(nlcommand.NewWorkspaceProvider(wsResolver, extractWS), tools)
			nlCommandResolver.Cache = nlcommand.NewCache(5 * time.Minute)
		}
	}
	taskDeps := tasks.Deps{DB: deps.DB, Queries: deps.Queries, Embedder: embedClient, NlConstraint: nlConstraintCompiler, Storage: deps.Storage, Audit: auditRec}
	tlDeps := timeline.Deps{DB: deps.DB, Queries: deps.Queries}
	inboxDeps := inbox.Deps{DB: deps.DB, Queries: deps.Queries}
	notifDeps := notifications.Deps{DB: deps.DB, Queries: deps.Queries, Audit: auditRec}
	// NOTE: relationDeps and exportDeps are declared inside chi groups
	// where they are scoped to the right middleware. Do not hoist them.
	aiDeps := aihandlers.Deps{DB: deps.DB, Queries: deps.Queries, Cipher: deps.Cipher, NlQuery: nlQueryCompiler, NlCommand: nlCommandResolver, Audit: auditRec}

	// AI orchestrator. Built once and shared by the MCP server, the
	// inbox triage handler, and any future AI endpoint. When
	// NF_AI_MOCK is set the resolver short-circuits to a deterministic
	// fixture-backed provider so tests do not need a cipher.
	var aiOrch *ai.Orchestrator
	switch {
	case deps.AiMock:
		mockResolver := providers.NewMockResolver(providers.NewMockProvider(""))
		budget := ai.BudgetReaderFunc(func(_ context.Context, _ uint32) (int64, error) { return 0, nil })
		aiOrch = &ai.Orchestrator{
			Resolver:      mockResolver,
			Guard:         ai.NewCostGuard(budget, 0),
			DB:            deps.DB,
			Queries:       deps.Queries,
			OnInvocation:  obs.RecordAIInvocation,
			ProposalCache: ai.NewProposalCache(10 * time.Minute),
		}
	case deps.Cipher != nil:
		resolver := providers.NewWorkspaceResolver(deps.Queries, deps.Cipher)
		budget := ai.BudgetReaderFunc(func(ctx context.Context, wsID uint32) (int64, error) {
			return deps.Queries.SumAiCostTodayForWorkspace(ctx, generated.SumAiCostTodayForWorkspaceParams{
				WorkspaceID: wsID,
				InvokedAt:   time.Now().UTC().Truncate(24 * time.Hour),
			})
		})
		aiOrch = &ai.Orchestrator{
			Resolver:      resolver,
			Guard:         ai.NewCostGuard(budget, 0),
			DB:            deps.DB,
			Queries:       deps.Queries,
			LogInvoke:     newDBInvocationLogger(deps.Queries, deps.AiInvocationPublisher),
			OnInvocation:  obs.RecordAIInvocation,
			ProposalCache: ai.NewProposalCache(10 * time.Minute),
		}
	}

	// /auth/refresh and /auth/logout authenticate via the nf_rt httpOnly
	// cookie, not the Bearer access token, so they must sit outside the
	// authMW group — otherwise a page reload (which starts with no access
	// token in memory) can never rotate the refresh token.
	//
	// A separate, more generous rate limiter protects these routes: refresh
	// fires on every page load so the window must be wider than the strict
	// login limiter above.
	cookieAuthRateLimiter := middleware.NewIPRateLimiter(middleware.RateLimitConfig{
		MaxRequests: 30,
		Window:      15 * time.Minute,
	})
	r.Group(func(sub chi.Router) {
		sub.Use(cookieAuthRateLimiter.Middleware())
		cookieAuthAPI := newSubAPI(sub)
		registerPublicAuthCookieRoutes(cookieAuthAPI, authDeps)
	})

	// /me, /workspaces{,list}.
	r.Group(func(sub chi.Router) {
		sub.Use(authMW)
		subAPI := newSubAPI(sub)
		registerProtectedAuthRoutes(subAPI, authDeps)
		huma.Register(subAPI, huma.Operation{
			OperationID: "me-integrations-list",
			Method:      http.MethodGet,
			Path:        "/me/integrations",
			Summary:     "List the authenticated user's personal OAuth integrations",
		}, integrationshandlers.List(integrationsDeps))
		huma.Register(subAPI, huma.Operation{
			OperationID: "me-integrations-connect",
			Method:      http.MethodPost,
			Path:        "/me/integrations/{provider}/connect",
			Summary:     "Start a personal OAuth connect flow",
		}, integrationshandlers.Connect(integrationsDeps))
		huma.Register(subAPI, huma.Operation{
			OperationID: "me-integrations-disconnect",
			Method:      http.MethodDelete,
			Path:        "/me/integrations/{id}",
			Summary:     "Disconnect a personal OAuth integration",
		}, integrationshandlers.Disconnect(integrationsDeps))
		huma.Register(subAPI, huma.Operation{
			OperationID: "workspaces-create",
			Method:      http.MethodPost,
			Path:        "/workspaces",
			Summary:     "Create a workspace",
		}, workspaces.Create(wsDeps))
		huma.Register(subAPI, huma.Operation{
			OperationID: "workspaces-list",
			Method:      http.MethodGet,
			Path:        "/workspaces",
			Summary:     "List workspaces visible to the caller",
		}, workspaces.List(wsDeps))
	})

	// /workspaces/{wsId} member-level reads, plus project list.
	r.Group(func(sub chi.Router) {
		sub.Use(authMW)
		sub.Use(middleware.RequireWorkspaceMember(aclDB))
		subAPI := newSubAPI(sub)
		huma.Register(subAPI, huma.Operation{
			OperationID: "workspaces-get",
			Method:      http.MethodGet,
			Path:        "/workspaces/{wsId}",
			Summary:     "Fetch a workspace",
		}, workspaces.Get(wsDeps))
		huma.Register(subAPI, huma.Operation{
			OperationID: "workspaces-members-list",
			Method:      http.MethodGet,
			Path:        "/workspaces/{wsId}/members",
			Summary:     "List members of a workspace",
		}, workspaces.ListMembers(wsDeps))
		huma.Register(subAPI, huma.Operation{
			OperationID: "workspaces-users-list",
			Method:      http.MethodGet,
			Path:        "/workspaces/{wsId}/users",
			Summary:     "List workspace users (minimal summary for actor pickers)",
		}, workspaces.ListUsers(wsDeps))
		huma.Register(subAPI, huma.Operation{
			OperationID: "projects-list",
			Method:      http.MethodGet,
			Path:        "/workspaces/{wsId}/projects",
			Summary:     "List projects in a workspace",
		}, projects.List(prjDeps))
		lensDeps := lenses.Deps{DB: deps.DB, Queries: deps.Queries, Audit: auditRec}
		lenses.RegisterWorkspaceScoped(subAPI, lensDeps)
		exportDeps := exporthandlers.Deps{DB: deps.DB, Queries: deps.Queries, Audit: auditRec}
		exporthandlers.RegisterWorkspaceScoped(subAPI, sub, exportDeps)
		tbDeps := timeboxes.Deps{DB: deps.DB, Queries: deps.Queries, Audit: auditRec}
		timeboxes.RegisterWorkspaceScoped(subAPI, tbDeps)
		dashDeps := dashboard.Deps{DB: deps.DB, Queries: deps.Queries, Audit: auditRec}
		dashboard.RegisterWorkspaceScoped(subAPI, dashDeps)
		pageDeps := pages.Deps{DB: deps.DB, Queries: deps.Queries, Audit: auditRec}
		pages.RegisterWorkspaceScoped(subAPI, pageDeps)
		huma.Register(subAPI, huma.Operation{
			OperationID: "ai-cost-today",
			Method:      http.MethodGet,
			Path:        "/workspaces/{wsId}/ai/cost-today",
			Summary:     "Today's accumulated LLM spend (USD) for a workspace",
		}, aihandlers.CostToday(aiDeps))
		huma.Register(subAPI, huma.Operation{
			OperationID: "ai-metrics",
			Method:      http.MethodGet,
			Path:        "/workspaces/{wsId}/ai/metrics",
			Summary:     "AI suggestion acceptance metrics over a trailing window",
		}, aihandlers.Metrics(aiDeps))
		huma.Register(subAPI, huma.Operation{
			OperationID: "ai-agent-pause",
			Method:      http.MethodPost,
			Path:        "/workspaces/{wsId}/ai/agents/{agentId}/pause",
			Summary:     "Toggle the kill switch on an AI agent (4.AGENT-3)",
		}, aihandlers.PauseAgent(aiDeps))
		huma.Register(subAPI, huma.Operation{
			OperationID: "ai-agents-list",
			Method:      http.MethodGet,
			Path:        "/workspaces/{wsId}/ai/agents",
			Summary:     "List AI agents for a workspace",
		}, aihandlers.ListAgents(aiDeps))
		huma.Register(subAPI, huma.Operation{
			OperationID: "ai-agents-create",
			Method:      http.MethodPost,
			Path:        "/workspaces/{wsId}/ai/agents",
			Summary:     "Create a new AI agent",
		}, aihandlers.CreateAgent(aiDeps))
		huma.Register(subAPI, huma.Operation{
			OperationID: "ai-agent-schedule-update",
			Method:      http.MethodPatch,
			Path:        "/workspaces/{wsId}/ai/agents/{agentId}/schedule",
			Summary:     "Update an AI agent's schedule_kind trigger mode",
		}, aihandlers.UpdateAgentSchedule(aiDeps))
		huma.Register(subAPI, huma.Operation{
			OperationID: "ai-agent-event-triggers-update",
			Method:      http.MethodPatch,
			Path:        "/workspaces/{wsId}/ai/agents/{agentId}/event-triggers",
			Summary:     "Replace an AI agent's event_trigger_types list",
		}, aihandlers.UpdateAgentEventTriggers(aiDeps))
		huma.Register(subAPI, huma.Operation{
			OperationID: "ai-agent-trigger",
			Method:      http.MethodPost,
			Path:        "/workspaces/{wsId}/ai/agents/{agentId}/trigger",
			Summary:     "Manually trigger one run of an AI agent",
		}, aihandlers.TriggerAgent(aiDeps, deps.AgentQueue, deps.AgentRunner))
		huma.Register(subAPI, huma.Operation{
			OperationID: "ai-models-list",
			Method:      http.MethodGet,
			Path:        "/workspaces/{wsId}/ai/models",
			Summary:     "List workspace AI models across all providers",
		}, aihandlers.ListModels(aiDeps))
		huma.Register(subAPI, huma.Operation{
			OperationID: "ai-priority-suggestions-list",
			Method:      http.MethodGet,
			Path:        "/workspaces/{wsId}/ai/priority-suggestions",
			Summary:     "Suggest priority adjustments for open tasks in a workspace",
		}, aihandlers.ListPrioritySuggestions(aiDeps))
		huma.Register(subAPI, huma.Operation{
			OperationID: "ai-compile-lens",
			Method:      http.MethodPost,
			Path:        "/workspaces/{wsId}/ai/compile-lens",
			Summary:     "Compile natural-language prose into a validated Lens JSON (ADR 0004)",
		}, aihandlers.CompileLens(aiDeps))
		huma.Register(subAPI, huma.Operation{
			OperationID: "ai-state-suggestions",
			Method:      http.MethodGet,
			Path:        "/workspaces/{wsId}/ai/state-suggestions",
			Summary:     "Workspace-wide deterministic state inference proposals (2.AI-1)",
		}, aihandlers.ListStateSuggestions(aiDeps))
		huma.Register(subAPI, huma.Operation{
			OperationID: "ai-reminders",
			Method:      http.MethodGet,
			Path:        "/workspaces/{wsId}/ai/reminders",
			Summary:     "Workspace-wide deterministic reminder engine proposals (2.AI-4)",
		}, aihandlers.ListReminders(aiDeps))
		huma.Register(subAPI, huma.Operation{
			OperationID: "ai-auto-actions",
			Method:      http.MethodGet,
			Path:        "/workspaces/{wsId}/ai/auto-actions",
			Summary:     "Workspace-wide deterministic auto-action proposals (2.AI-3)",
		}, aihandlers.ListAutoActions(aiDeps))
		huma.Register(subAPI, huma.Operation{
			OperationID: "ai-weekly-digest",
			Method:      http.MethodGet,
			Path:        "/workspaces/{wsId}/ai/weekly-digest",
			Summary:     "Deterministic weekly digest markdown for a workspace (2.AI-9)",
		}, aihandlers.WeeklyDigest(aiDeps))
		huma.Register(subAPI, huma.Operation{
			OperationID: "ai-invocations-list",
			Method:      http.MethodGet,
			Path:        "/workspaces/{wsId}/ai/invocations",
			Summary:     "List redacted LLM call audit rows for the AI reasoning panel",
		}, aihandlers.ListInvocations(aiDeps))
		huma.Register(subAPI, huma.Operation{
			OperationID: "ai-resolve-command",
			Method:      http.MethodPost,
			Path:        "/workspaces/{wsId}/ai/resolve-command",
			Summary:     "Resolve a natural-language command into an MCP tool invocation",
		}, aihandlers.ResolveCommand(aiDeps))

		// AI-powered smart task creation (propose + apply).
		smartCreateDeps := tasks.SmartCreateDeps{DB: deps.DB, Queries: deps.Queries, AI: aiOrch, Embedder: embedClient, Audit: auditRec}
		tasks.RegisterSmartCreate(subAPI, smartCreateDeps)

		// Realtime SSE stream for this workspace. Not a Huma
		// operation because the response is a long-lived
		// text/event-stream that never fits the request/response DTO
		// model. See ADR 0005.
		notifier := deps.StreamNotifier
		if notifier == nil {
			notifier = stream.NopNotifier{}
		}
		sub.Get("/workspaces/{wsId}/stream", stream.SSEHandler(notifier, deps.StreamRemember))
		notifications.RegisterWorkspaceScoped(subAPI, notifDeps)
		relationDeps := relations.Deps{DB: deps.DB, Queries: deps.Queries, Audit: auditRec}
		relations.RegisterWorkspaceScoped(subAPI, relationDeps)
	})

	// Per-user MCP tokens (workspace member, not admin).
	r.Group(func(sub chi.Router) {
		sub.Use(authMW)
		sub.Use(middleware.RequireWorkspaceMember(aclDB))
		subAPI := newSubAPI(sub)
		aihandlers.RegisterMcpTokens(subAPI, aiDeps)
	})

	// Workspace admin routes + AI providers + project create.
	r.Group(func(sub chi.Router) {
		sub.Use(authMW)
		sub.Use(middleware.RequireWorkspaceMember(aclDB))
		sub.Use(middleware.RequireWorkspaceRole(middleware.WorkspaceRoleAdmin))
		subAPI := newSubAPI(sub)
		huma.Register(subAPI, huma.Operation{
			OperationID: "workspaces-patch",
			Method:      http.MethodPatch,
			Path:        "/workspaces/{wsId}",
			Summary:     "Patch a workspace",
		}, workspaces.Patch(wsDeps))
		huma.Register(subAPI, huma.Operation{
			OperationID: "workspaces-members-invite",
			Method:      http.MethodPost,
			Path:        "/workspaces/{wsId}/members",
			Summary:     "Invite a user to a workspace",
		}, workspaces.InviteMember(wsDeps))
		huma.Register(subAPI, huma.Operation{
			OperationID: "workspaces-members-update-role",
			Method:      http.MethodPatch,
			Path:        "/workspaces/{wsId}/members/{userId}",
			Summary:     "Change a member's role",
		}, workspaces.UpdateMemberRole(wsDeps))
		huma.Register(subAPI, huma.Operation{
			OperationID: "workspaces-members-remove",
			Method:      http.MethodDelete,
			Path:        "/workspaces/{wsId}/members/{userId}",
			Summary:     "Remove a member from a workspace",
		}, workspaces.RemoveMember(wsDeps))
		huma.Register(subAPI, huma.Operation{
			OperationID: "projects-create",
			Method:      http.MethodPost,
			Path:        "/workspaces/{wsId}/projects",
			Summary:     "Create a project in a workspace",
		}, projects.Create(prjDeps))
		aihandlers.RegisterProviders(subAPI, aiDeps)
		aihandlers.RegisterAutoActionSettings(subAPI, aiDeps)
		aihandlers.RegisterAutoActionRules(subAPI, aiDeps)
		webhookDeps := webhooks.Deps{DB: deps.DB, Queries: deps.Queries, Audit: auditRec}
		webhooks.Register(subAPI, webhookDeps)
	})

	// Workspace owner-only.
	r.Group(func(sub chi.Router) {
		sub.Use(authMW)
		sub.Use(middleware.RequireWorkspaceMember(aclDB))
		sub.Use(middleware.RequireWorkspaceRole(middleware.WorkspaceRoleOwner))
		subAPI := newSubAPI(sub)
		huma.Register(subAPI, huma.Operation{
			OperationID: "workspaces-disable",
			Method:      http.MethodDelete,
			Path:        "/workspaces/{wsId}",
			Summary:     "Soft-disable a workspace",
		}, workspaces.Disable(wsDeps))
	})

	// /projects/{prjId}.
	r.Group(func(sub chi.Router) {
		sub.Use(authMW)
		sub.Use(middleware.RequireProjectMemberByGlobalId(aclDB))
		subAPI := newSubAPI(sub)
		projects.RegisterGlobal(subAPI, prjDeps)
		timeline.RegisterProjectScoped(subAPI, tlDeps)
	})

	// Task collection routes.
	r.Group(func(sub chi.Router) {
		sub.Use(authMW)
		subAPI := newSubAPI(sub)
		tasks.RegisterCollection(subAPI, taskDeps)
	})

	// Task-scoped routes + task timeline.
	r.Group(func(sub chi.Router) {
		sub.Use(authMW)
		sub.Use(middleware.RequireTaskAccess(aclDB))
		subAPI := newSubAPI(sub)
		tasks.RegisterTaskScoped(subAPI, taskDeps)
		timeline.RegisterTaskScoped(subAPI, tlDeps)
		relationTaskDeps := relations.Deps{DB: deps.DB, Queries: deps.Queries, Audit: auditRec}
		relations.RegisterTaskScoped(subAPI, relationTaskDeps)

		// AI-powered step decomposition (propose + apply).
		stepsDeps := tasks.StepsDeps{DB: deps.DB, Queries: deps.Queries, AI: aiOrch, Audit: auditRec}
		tasks.RegisterSteps(subAPI, stepsDeps)
	})

	// Workspace timeline.
	r.Group(func(sub chi.Router) {
		sub.Use(authMW)
		sub.Use(middleware.RequireWorkspaceMember(aclDB))
		subAPI := newSubAPI(sub)
		timeline.RegisterWorkspaceScoped(subAPI, tlDeps)
	})

	// Signals + inbox (auth only; handlers resolve ws membership themselves).
	signalDeps := signals.Deps{
		DB:                 deps.DB,
		Queries:            deps.Queries,
		Audit:              auditRec,
		GhWebhookSecret:    deps.GhWebhookSecret,
		SlackSigningSecret: deps.SlackSigningSecret,
		GoogleChannelToken: deps.GoogleChannelToken,
		DefaultWorkspaceID: deps.DefaultWorkspaceID,
	}
	r.Group(func(sub chi.Router) {
		sub.Use(authMW)
		subAPI := newSubAPI(sub)
		signals.RegisterCollection(subAPI, signalDeps)
		inbox.Register(subAPI, inboxDeps)
		notifications.Register(subAPI, notifDeps)
		relationAuthDeps := relations.Deps{DB: deps.DB, Queries: deps.Queries, Audit: auditRec}
		relations.RegisterAuthScoped(subAPI, relationAuthDeps)
	})

	// MCP server uses the orchestrator built above. The SSE event hook
	// is registered so workspace events are broadcast to connected MCP
	// clients in real time.
	mcpHandler := mcp.NewHandler(mcp.Deps{DB: deps.DB, Queries: deps.Queries, AI: aiOrch, Embedder: embedClient, NlQuery: nlQueryCompiler})
	eventbus.AddNotifyHook(mcpHandler.RegisterEventHook())
	r.Handle("/mcp", mcpHandler)

	// Relation auto-detect pipeline (INTEL-3). Fires on task.created
	// and task.updated events, creates relation_suggestions via
	// embedding similarity in a background goroutine.
	if embedClient != nil {
		relationPipeline := &airelations.Pipeline{DB: deps.DB, Queries: deps.Queries, Embedder: embedClient}
		eventbus.AddNotifyHook(relationPipeline.Hook())
	}

	// Workspace-scoped AI inbox triage. Registered in
	// its own group so the auth + workspace-member middleware applies
	// without leaking the orchestrator to the v1 inbox routes.
	r.Group(func(sub chi.Router) {
		sub.Use(authMW)
		sub.Use(middleware.RequireWorkspaceMember(aclDB))
		subAPI := newSubAPI(sub)
		triageDeps := inbox.TriageDeps{Deps: inboxDeps, AI: aiOrch}
		inbox.RegisterTriage(subAPI, triageDeps)
		inbox.RegisterAiSuggestions(subAPI, triageDeps)
	})

	// Public lens sharing (no auth, per-IP rate limited).
	publicLensRateLimiter := middleware.NewIPRateLimiter(middleware.RateLimitConfig{
		MaxRequests: 30,
		Window:      15 * time.Minute,
	})
	r.Group(func(sub chi.Router) {
		sub.Use(publicLensRateLimiter.Middleware())
		publicLensAPI := newSubAPI(sub)
		publicLensDeps := lenses.Deps{DB: deps.DB, Queries: deps.Queries, Audit: auditRec}
		lenses.RegisterPublic(publicLensAPI, publicLensDeps)
	})

	// Public webhooks (verify their own signatures).
	r.Post("/webhooks/github", signals.HandleGithubWebhook(signalDeps))
	r.Post("/webhooks/slack", signals.HandleSlackWebhook(signalDeps))
	r.Post("/webhooks/google", signals.HandleGoogleWebhook(signalDeps))

	// OpenAPI spec and Scalar API reference UI — public, no auth.
	specJSON := buildOpenAPIJSON(apis)
	r.Get("/openapi.json", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write(specJSON) //nolint:errcheck // best-effort write to HTTP client
	})
	r.Get("/api-reference", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Write([]byte(scalarHTML)) //nolint:errcheck // best-effort write to HTTP client
	})

	return Result{Handler: r, APIs: apis}
}

type healthOutput struct {
	Body struct {
		Status string `json:"status"`
	}
}

// registerPublicAuthRoutes wires the unauthenticated auth endpoints.
func registerPublicAuthRoutes(api huma.API, deps authhandlers.Deps) {
	huma.Register(api, huma.Operation{
		OperationID: "auth-register",
		Method:      http.MethodPost,
		Path:        "/auth/register",
		Summary:     "Register a new local-password account",
	}, authhandlers.Register(deps))
	huma.Register(api, huma.Operation{
		OperationID: "auth-login",
		Method:      http.MethodPost,
		Path:        "/auth/login",
		Summary:     "Log in with email and password",
	}, authhandlers.Login(deps))
	huma.Register(api, huma.Operation{
		OperationID: "auth-oidc-google-start",
		Method:      http.MethodGet,
		Path:        "/auth/oidc/google/start",
		Summary:     "Start a Google OIDC login flow",
	}, authhandlers.OIDCGoogleStart(deps))
	huma.Register(api, huma.Operation{
		OperationID: "auth-oidc-google-callback",
		Method:      http.MethodGet,
		Path:        "/auth/oidc/google/callback",
		Summary:     "Complete a Google OIDC login flow",
	}, authhandlers.OIDCGoogleCallback(deps))
}

// registerPublicAuthCookieRoutes wires the auth endpoints that
// authenticate via the nf_rt httpOnly refresh cookie rather than the
// Bearer access JWT. They must not be behind authMW, otherwise a page
// reload (which starts with an empty in-memory access token) can never
// reach /auth/refresh to rotate the session.
func registerPublicAuthCookieRoutes(api huma.API, deps authhandlers.Deps) {
	huma.Register(api, huma.Operation{
		OperationID: "auth-refresh",
		Method:      http.MethodPost,
		Path:        "/auth/refresh",
		Summary:     "Rotate refresh token and issue a new access token",
	}, authhandlers.Refresh(deps))
	huma.Register(api, huma.Operation{
		OperationID: "auth-logout",
		Method:      http.MethodPost,
		Path:        "/auth/logout",
		Summary:     "Revoke a session",
	}, authhandlers.Logout(deps))
	huma.Register(api, huma.Operation{
		OperationID: "auth-login-totp",
		Method:      http.MethodPost,
		Path:        "/auth/login/totp",
		Summary:     "Complete a TOTP step-up login after /auth/login returned totp_required",
	}, authhandlers.LoginTotp(deps))
}

// registerProtectedAuthRoutes wires the bearer-protected auth endpoints.
func registerProtectedAuthRoutes(api huma.API, deps authhandlers.Deps) {
	huma.Register(api, huma.Operation{
		OperationID: "me",
		Method:      http.MethodGet,
		Path:        "/me",
		Summary:     "Return the authenticated user's profile",
	}, authhandlers.Me(deps))
	huma.Register(api, huma.Operation{
		OperationID: "me-patch",
		Method:      http.MethodPatch,
		Path:        "/me",
		Summary:     "Patch the authenticated user's profile",
	}, authhandlers.PatchMe(deps))
	huma.Register(api, huma.Operation{
		OperationID: "me-sessions-list",
		Method:      http.MethodGet,
		Path:        "/me/sessions",
		Summary:     "List the authenticated user's active sessions",
	}, authhandlers.ListSessions(deps))
	huma.Register(api, huma.Operation{
		OperationID: "me-sessions-revoke",
		Method:      http.MethodDelete,
		Path:        "/me/sessions/{sessionId}",
		Summary:     "Revoke a single session by public id",
	}, authhandlers.RevokeOneSession(deps))
	huma.Register(api, huma.Operation{
		OperationID: "me-sessions-revoke-others",
		Method:      http.MethodDelete,
		Path:        "/me/sessions",
		Summary:     "Revoke every session except the one on the current request",
	}, authhandlers.RevokeAllOtherSessions(deps))
	huma.Register(api, huma.Operation{
		OperationID: "me-password-change",
		Method:      http.MethodPost,
		Path:        "/me/password",
		Summary:     "Change the authenticated user's password",
	}, authhandlers.ChangePassword(deps))
	huma.Register(api, huma.Operation{
		OperationID: "me-totp-status",
		Method:      http.MethodGet,
		Path:        "/me/totp",
		Summary:     "Return the authenticated user's TOTP 2FA status",
	}, authhandlers.TotpStatus(deps))
	huma.Register(api, huma.Operation{
		OperationID: "me-totp-enroll",
		Method:      http.MethodPost,
		Path:        "/me/totp/enroll",
		Summary:     "Begin TOTP 2FA enrollment (returns otpauth URL)",
	}, authhandlers.TotpEnroll(deps))
	huma.Register(api, huma.Operation{
		OperationID: "me-totp-confirm",
		Method:      http.MethodPost,
		Path:        "/me/totp/confirm",
		Summary:     "Confirm TOTP 2FA enrollment with a generated code",
	}, authhandlers.TotpConfirm(deps))
	huma.Register(api, huma.Operation{
		OperationID: "me-totp-disable",
		Method:      http.MethodDelete,
		Path:        "/me/totp",
		Summary:     "Disable TOTP 2FA after password reverification",
	}, authhandlers.TotpDisable(deps))
	huma.Register(api, huma.Operation{
		OperationID: "me-totp-recovery-status",
		Method:      http.MethodGet,
		Path:        "/me/totp/recovery-codes",
		Summary:     "Return remaining TOTP recovery code count",
	}, authhandlers.TotpRecoveryCodesStatus(deps))
	huma.Register(api, huma.Operation{
		OperationID: "me-totp-recovery-regenerate",
		Method:      http.MethodPost,
		Path:        "/me/totp/recovery-codes",
		Summary:     "Regenerate TOTP recovery codes after password reverification",
	}, authhandlers.TotpRegenerateRecoveryCodes(deps))
}

// newDBInvocationLogger returns an ai.InvocationLogger that persists
// redacted records into ai_invocations. When the workspace has no
// enabled provider the record is dropped silently — LogInvoke is a
// best-effort audit path and must never break a user-visible AI
// response.
func newDBInvocationLogger(q *generated.Queries, publish func(context.Context, uint32)) ai.InvocationLogger {
	return func(ctx context.Context, rec ai.InvocationRecord) {
		if q == nil || rec.WorkspaceID == 0 {
			return
		}
		providerID, err := q.FindDefaultProviderIDForWorkspace(ctx, rec.WorkspaceID)
		if err != nil {
			return
		}
		var userID sql.NullInt32
		var agentID sql.NullInt32
		if rec.AgentID != 0 {
			agentID = sql.NullInt32{Int32: int32(rec.AgentID), Valid: true}
		}
		var response sql.NullString
		if rec.ResponseRedacted != "" {
			response = sql.NullString{String: rec.ResponseRedacted, Valid: true}
		}
		var tIn, tOut sql.NullInt32
		if rec.TokensInput > 0 {
			tIn = sql.NullInt32{Int32: int32(rec.TokensInput), Valid: true}
		}
		if rec.TokensOutput > 0 {
			tOut = sql.NullInt32{Int32: int32(rec.TokensOutput), Valid: true}
		}
		var cost sql.NullString
		if rec.CostCents > 0 {
			cost = sql.NullString{String: fmt.Sprintf("%d.%06d", rec.CostCents/100, (rec.CostCents%100)*10000), Valid: true}
		}
		status := generated.AiInvocationsStatusOk
		if rec.Status == "error" {
			status = generated.AiInvocationsStatusError
		} else if rec.Status == "blocked" {
			status = generated.AiInvocationsStatusBlocked
		}
		var ec sql.NullString
		if rec.ErrorCode != "" {
			ec = sql.NullString{String: rec.ErrorCode, Valid: true}
		}
		if _, err := q.LogAiInvocation(ctx, generated.LogAiInvocationParams{
			PublicID:         types.New(),
			WorkspaceID:      rec.WorkspaceID,
			ProviderID:       providerID,
			UserID:           userID,
			AgentID:          agentID,
			TaskID:           sql.NullInt32{},
			Purpose:          rec.Purpose,
			Model:            rec.Model,
			PromptRedacted:   rec.PromptRedacted,
			ResponseRedacted: response,
			TokensInput:      tIn,
			TokensOutput:     tOut,
			CostEstimate:     cost,
			Status:           status,
			ErrorCode:        ec,
			InvokedAt:        time.Now().UTC(),
		}); err == nil && publish != nil {
			publish(ctx, rec.WorkspaceID)
		}
	}
}

// scalarHTML is the self-contained HTML page that loads the Scalar API
// reference UI from their CDN. It points at the co-located /openapi.json
// route so the spec is always in sync with the running server.
const scalarHTML = `<!DOCTYPE html>
<html>
<head>
  <title>nodate-flow API Reference</title>
  <meta charset="utf-8" />
  <meta name="viewport" content="width=device-width, initial-scale=1" />
</head>
<body>
  <script id="api-reference" data-url="/openapi.json"></script>
  <script src="https://cdn.jsdelivr.net/npm/@scalar/api-reference"></script>
</body>
</html>`

// buildOpenAPIJSON merges the separate huma.API OpenAPI documents into a
// single spec and returns the marshaled JSON bytes. This is the same
// merge logic used by cmd/dump-openapi but executed at router build time
// so the running server can serve the spec without a filesystem artifact.
func buildOpenAPIJSON(apis []huma.API) []byte {
	merged := mergeAPIs(apis)
	buf, err := json.Marshal(merged)
	if err != nil {
		// Should never happen: Huma's OpenAPI types are always
		// serializable. Fall back to a minimal valid document so
		// /openapi.json never 500s.
		return []byte(`{"openapi":"3.1.0","info":{"title":"nodate-flow","version":"0.0.0"},"paths":{}}`)
	}
	return buf
}

// mergeAPIs merges every sub-API's OpenAPI document into a single
// OpenAPI 3.1 spec. The nodate-flow router splits operations across
// multiple humachi.New instances so each middleware chain lives in its
// own chi group, which means each group carries its own OpenAPI doc.
func mergeAPIs(apis []huma.API) *huma.OpenAPI {
	if len(apis) == 0 {
		return &huma.OpenAPI{OpenAPI: "3.1.0"}
	}
	root := apis[0].OpenAPI()
	if root.Paths == nil {
		root.Paths = map[string]*huma.PathItem{}
	}
	if root.Components == nil {
		root.Components = &huma.Components{}
	}
	for _, a := range apis[1:] {
		spec := a.OpenAPI()
		for path, item := range spec.Paths {
			if existing, ok := root.Paths[path]; ok {
				mergePathItem(existing, item)
			} else {
				root.Paths[path] = item
			}
		}
		if spec.Components == nil {
			continue
		}
		if spec.Components.Schemas != nil && root.Components.Schemas != nil {
			rootMap := root.Components.Schemas.Map()
			for name, schema := range spec.Components.Schemas.Map() {
				if _, ok := rootMap[name]; ok {
					continue
				}
				rootMap[name] = schema
			}
		}
	}
	return root
}

// mergePathItem copies operations from src into dst for HTTP verbs that
// dst does not already define.
func mergePathItem(dst, src *huma.PathItem) {
	if dst.Get == nil {
		dst.Get = src.Get
	}
	if dst.Put == nil {
		dst.Put = src.Put
	}
	if dst.Post == nil {
		dst.Post = src.Post
	}
	if dst.Delete == nil {
		dst.Delete = src.Delete
	}
	if dst.Patch == nil {
		dst.Patch = src.Patch
	}
	if dst.Head == nil {
		dst.Head = src.Head
	}
	if dst.Options == nil {
		dst.Options = src.Options
	}
	if dst.Trace == nil {
		dst.Trace = src.Trace
	}
}

// passthroughDB adapts *sql.DB to middleware.ACLDB.
type passthroughDB struct{ db *sql.DB }

func (p passthroughDB) QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row {
	return p.db.QueryRowContext(ctx, query, args...)
}
