// Package router assembles the nodate-flow HTTP API router. It exists
// so that both cmd/api/main.go and the integration test harness
// (apps/flow-api/tests/helpers) can mount the exact same route set against a
// real *sql.DB without duplicating the wiring in two places.
//
// The router intentionally takes its dependencies as an explicit Deps
// struct rather than reading environment variables, so tests can
// construct it with a fixed test cipher and an empty default workspace
// id.
//
// Sub-router split (R6 Phase 0 / ADR 0007). The router composes three
// builder functions:
//
//   - buildAuthenticatedAPI: every route that requires a valid bearer
//     token. Auth middleware is attached by the builder itself; static
//     check tests assert that every operation registered through this
//     builder ends in a 401 when called without credentials.
//   - buildPublicShareAPI: the unauthenticated surface — /health,
//     /public/lenses/{token}, /share/cal/* (added in Step 0-2),
//     /invites/{token}/info / /invites/{token}/accept (added in Step 0-2),
//     and the webhook receivers that verify their own signatures. This
//     sub-router runs under its own per-IP rate limiter and never
//     consults a session.
//   - buildAuthAPI: placeholder for the auth/identity surface. flow-api
//     does not own login today (that lives in apps/auth-api/). The
//     builder is in place so future migrations can plug in without
//     touching BuildResult or the static check tests.
//
// The static check tests in router_test.go walk each sub-API's OpenAPI
// document and exercise every (method, path) pair to assert that the
// authenticated builder always produces 401 on unauthenticated requests
// and the public builder never does.
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

	"github.com/nodate-flow/nodate-flow/apps/flow-api/internal/ai"
	"github.com/nodate-flow/nodate-flow/apps/flow-api/internal/ai/agentruntime"
	"github.com/nodate-flow/nodate-flow/apps/flow-api/internal/ai/embed"
	"github.com/nodate-flow/nodate-flow/apps/flow-api/internal/ai/nlcommand"
	"github.com/nodate-flow/nodate-flow/apps/flow-api/internal/ai/nlconstraint"
	"github.com/nodate-flow/nodate-flow/apps/flow-api/internal/ai/nlquery"
	"github.com/nodate-flow/nodate-flow/apps/flow-api/internal/ai/providers"
	airelations "github.com/nodate-flow/nodate-flow/apps/flow-api/internal/ai/relations"
	"github.com/nodate-flow/nodate-flow/apps/flow-api/internal/audit"
	"github.com/nodate-flow/nodate-flow/apps/flow-api/internal/auth"
	"github.com/nodate-flow/nodate-flow/apps/flow-api/internal/db/generated"
	"github.com/nodate-flow/nodate-flow/apps/flow-api/internal/db/generated/calendar"
	"github.com/nodate-flow/nodate-flow/apps/flow-api/internal/db/types"
	"github.com/nodate-flow/nodate-flow/apps/flow-api/internal/eventbus"
	aihandlers "github.com/nodate-flow/nodate-flow/apps/flow-api/internal/http/handlers/ai"
	audithandlers "github.com/nodate-flow/nodate-flow/apps/flow-api/internal/http/handlers/audit"
	"github.com/nodate-flow/nodate-flow/apps/flow-api/internal/http/handlers/calendars"
	"github.com/nodate-flow/nodate-flow/apps/flow-api/internal/http/handlers/dashboard"
	exporthandlers "github.com/nodate-flow/nodate-flow/apps/flow-api/internal/http/handlers/export"
	"github.com/nodate-flow/nodate-flow/apps/flow-api/internal/http/handlers/favorites"
	importhandlers "github.com/nodate-flow/nodate-flow/apps/flow-api/internal/http/handlers/imports"
	"github.com/nodate-flow/nodate-flow/apps/flow-api/internal/http/handlers/inbox"
	intakehandlers "github.com/nodate-flow/nodate-flow/apps/flow-api/internal/http/handlers/intake"
	"github.com/nodate-flow/nodate-flow/apps/flow-api/internal/http/handlers/labels"
	"github.com/nodate-flow/nodate-flow/apps/flow-api/internal/http/handlers/lenses"
	"github.com/nodate-flow/nodate-flow/apps/flow-api/internal/http/handlers/notifications"
	"github.com/nodate-flow/nodate-flow/apps/flow-api/internal/http/handlers/pages"
	"github.com/nodate-flow/nodate-flow/apps/flow-api/internal/http/handlers/projects"
	"github.com/nodate-flow/nodate-flow/apps/flow-api/internal/http/handlers/reactions"
	"github.com/nodate-flow/nodate-flow/apps/flow-api/internal/http/handlers/relations"
	"github.com/nodate-flow/nodate-flow/apps/flow-api/internal/http/handlers/signals"
	"github.com/nodate-flow/nodate-flow/apps/flow-api/internal/http/handlers/tasks"
	"github.com/nodate-flow/nodate-flow/apps/flow-api/internal/http/handlers/timeboxes"
	"github.com/nodate-flow/nodate-flow/apps/flow-api/internal/http/handlers/timeline"
	"github.com/nodate-flow/nodate-flow/apps/flow-api/internal/http/handlers/webhooks"
	"github.com/nodate-flow/nodate-flow/apps/flow-api/internal/http/middleware"
	"github.com/nodate-flow/nodate-flow/apps/flow-api/internal/mcp"
	"github.com/nodate-flow/nodate-flow/apps/flow-api/internal/obs"
	"github.com/nodate-flow/nodate-flow/apps/flow-api/internal/storage"
	"github.com/nodate-flow/nodate-flow/apps/flow-api/internal/stream"
	"github.com/nodate-flow/nodate-flow/packages/go-shared/crypto"
	"github.com/nodate-flow/nodate-flow/packages/go-shared/email"
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
	// CalendarQueries is the dedicated sqlc subpackage handle that emits
	// every calendar-domain query. Callers should pass calendar.New(DB)
	// so it shares the same connection pool as Queries. Threaded into
	// calendars.Deps and mcp.Deps for any calendar-domain query.
	CalendarQueries *calendar.Queries
	// JWT is the access-token issuer used by RequireAuth.
	JWT *auth.JWTIssuer
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
	// DisableRateLimit disables all per-IP rate limiters. Used by
	// integration tests where many parallel tenants register from
	// the same loopback address.
	DisableRateLimit bool
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
	// EmailFrom is the envelope sender address used by calendar
	// invite-email dispatch. Sourced from NF_FLOW_SMTP_FROM.
	EmailFrom string
	// FlowWebURL is the origin of the flow-web frontend, used to build
	// magic-link accept URLs in outbound calendar invite emails. Empty
	// in tests.
	FlowWebURL string

	// EmbedOpenAIKey is the plaintext OpenAI API key for the embedding
	// pipeline. When non-empty, the router uses embed.OpenAIProvider
	// instead of the mock. Empty in tests (mock is used).
	EmbedOpenAIKey string
	// EmbedModel overrides the default embedding model. Empty means
	// text-embedding-3-small.
	EmbedModel string
	// EmbedBaseURL overrides the embeddings API base URL.
	EmbedBaseURL string
}

// Result is what BuildResult returns: the composed chi router plus the
// list of huma.API instances that were registered against it. The
// dump-openapi command merges each API's OpenAPI document into a single
// spec for TypeScript SDK generation.
//
// AuthenticatedOps, PublicOps, and AuthOps capture the (method, path,
// operationID) triples registered through each builder. They are
// snapshotted before mergeAPIs mutates the first sub-API's OpenAPI
// document, so the static check tests
// (TestPublicSubRouterIsAuthFree / TestAuthenticatedSubRouterAlwaysAuthenticated)
// see only the operations that belong to their builder rather than
// the merged super-set. AuthOps is empty today (see buildAuthAPI).
type Result struct {
	Handler          http.Handler
	APIs             []huma.API
	AuthenticatedOps []OperationRef
	PublicOps        []OperationRef
	AuthOps          []OperationRef
}

// OperationRef identifies a single huma operation by its HTTP method,
// chi/Huma path template (with {param} placeholders intact), and
// stable OperationID. The router's static check tests use it to walk
// every route registered through a particular builder without paying
// the cost of OpenAPI merge mutation.
type OperationRef struct {
	Method      string
	Path        string
	OperationID string
}

// snapshotOps reads each huma.API's OpenAPI document and emits one
// OperationRef per registered (verb, path) pair. It MUST be called
// before mergeAPIs, because that function mutates apis[0]'s OpenAPI
// document in place to host every other sub-API's paths.
func snapshotOps(apis []huma.API) []OperationRef {
	var ops []OperationRef
	for _, a := range apis {
		spec := a.OpenAPI()
		if spec == nil || spec.Paths == nil {
			continue
		}
		for path, item := range spec.Paths {
			if item == nil {
				continue
			}
			verbs := map[string]*huma.Operation{
				http.MethodGet:     item.Get,
				http.MethodPost:    item.Post,
				http.MethodPut:     item.Put,
				http.MethodPatch:   item.Patch,
				http.MethodDelete:  item.Delete,
				http.MethodHead:    item.Head,
				http.MethodOptions: item.Options,
			}
			for method, op := range verbs {
				if op == nil {
					continue
				}
				ops = append(ops, OperationRef{
					Method:      method,
					Path:        path,
					OperationID: op.OperationID,
				})
			}
		}
	}
	return ops
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
	// Global per-IP rate limiter: defence-in-depth against floods
	// from a single source. The limit is generous (200 req/min)
	// because authenticated routes additionally enforce per-user
	// limits inside their groups. Disabled in integration tests
	// where many parallel tenants hit the same loopback address.
	if !deps.DisableRateLimit {
		globalRL := middleware.NewIPRateLimiter(middleware.RateLimitConfig{
			MaxRequests: 200,
			Window:      time.Minute,
		})
		r.Use(globalRL.Middleware())
	}

	shared := buildSharedDeps(deps)

	authMW := middleware.RequireAuth(middleware.AuthDeps{
		JWT:     deps.JWT,
		Queries: deps.Queries,
		DB:      shared.aclDB,
	})

	authedAPIs := buildAuthenticatedAPI(r, deps, shared, authMW)
	publicAPIs := buildPublicShareAPI(r, deps, shared)
	authAPIs := buildAuthAPI(r, deps, shared)

	// Snapshot the per-builder operation set before mergeAPIs mutates
	// apis[0]'s OpenAPI document. The static check tests rely on these
	// pristine slices to walk only the routes that belong to their
	// builder; once merged, every path appears under apis[0].
	authedOps := snapshotOps(authedAPIs)
	publicOps := snapshotOps(publicAPIs)
	authOps := snapshotOps(authAPIs)

	// Aggregate every sub-API for OpenAPI merging. The order matters
	// for /openapi.json: authenticated paths come first so the rendered
	// document keeps the historical layout.
	apis := make([]huma.API, 0, len(authedAPIs)+len(publicAPIs)+len(authAPIs))
	apis = append(apis, authedAPIs...)
	apis = append(apis, publicAPIs...)
	apis = append(apis, authAPIs...)

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

	return Result{
		Handler:          r,
		APIs:             apis,
		AuthenticatedOps: authedOps,
		PublicOps:        publicOps,
		AuthOps:          authOps,
	}
}

// sharedDeps groups the derived dependency objects that several builder
// functions need to share (auditor, ACL adapter, AI orchestrator, the
// per-feature Deps structs the handlers expect, etc.). It is built once
// per BuildResult call so the AI orchestrator and embed client are
// instantiated exactly once even though the router has multiple sub
// builders.
type sharedDeps struct {
	aclDB                passthroughDB
	auditRec             *audit.Recorder
	embedClient          *embed.Client
	aiOrch               *ai.Orchestrator
	nlQueryCompiler      *nlquery.Compiler
	nlConstraintCompiler *nlconstraint.Compiler
	nlCommandResolver    *nlcommand.Resolver
	prjDeps              projects.Deps
	taskDeps             tasks.Deps
	tlDeps               timeline.Deps
	inboxDeps            inbox.Deps
	notifDeps            notifications.Deps
	aiDeps               aihandlers.Deps
	signalDeps           signals.Deps
	calDeps              calendars.Deps
}

// buildSharedDeps constructs the per-feature handler Deps structs and
// the AI orchestrator. The wiring decisions (mock vs. workspace
// resolver, embed provider, NL compilers) are concentrated here so the
// individual builder functions stay focused on huma.Register calls.
func buildSharedDeps(deps Deps) *sharedDeps {
	auditRec := audit.New(deps.Queries)
	aclDB := passthroughDB{deps.DB}

	// Write-time embedding client (ADR 0003). Uses the OpenAI provider
	// when NF_EMBED_OPENAI_KEY is set, otherwise the deterministic mock.
	var embedClient *embed.Client
	var nlQueryCompiler *nlquery.Compiler
	var nlConstraintCompiler *nlconstraint.Compiler
	var nlCommandResolver *nlcommand.Resolver
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
		// Encrypt the plaintext key at boot so the provider stores only
		// ciphertext and decrypts per-call (decrypt-use-zero pattern).
		// When no cipher is available, pass the raw bytes with a nil
		// decryptor which triggers the identity fallback.
		var keyCipher []byte
		var dec embed.Decryptor
		if deps.Cipher != nil {
			ct, encErr := deps.Cipher.Encrypt([]byte(deps.EmbedOpenAIKey))
			if encErr == nil {
				keyCipher = ct
				dec = deps.Cipher
			}
		}
		if keyCipher == nil {
			keyCipher = []byte(deps.EmbedOpenAIKey)
		}
		embedClient = embed.New(embed.NewOpenAIProvider(keyCipher, dec, opts...), deps.Queries)
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

	return &sharedDeps{
		aclDB:                aclDB,
		auditRec:             auditRec,
		embedClient:          embedClient,
		aiOrch:               aiOrch,
		nlQueryCompiler:      nlQueryCompiler,
		nlConstraintCompiler: nlConstraintCompiler,
		nlCommandResolver:    nlCommandResolver,
		prjDeps:              projects.Deps{DB: deps.DB, Queries: deps.Queries, Audit: auditRec},
		taskDeps:             tasks.Deps{DB: deps.DB, Queries: deps.Queries, Embedder: embedClient, NlConstraint: nlConstraintCompiler, Storage: deps.Storage, Audit: auditRec},
		tlDeps:               timeline.Deps{DB: deps.DB, Queries: deps.Queries},
		inboxDeps:            inbox.Deps{DB: deps.DB, Queries: deps.Queries},
		notifDeps:            notifications.Deps{DB: deps.DB, Queries: deps.Queries, Audit: auditRec},
		aiDeps:               aihandlers.Deps{DB: deps.DB, Queries: deps.Queries, Cipher: deps.Cipher, NlQuery: nlQueryCompiler, NlCommand: nlCommandResolver, Audit: auditRec},
		signalDeps: signals.Deps{
			DB:                 deps.DB,
			Queries:            deps.Queries,
			Audit:              auditRec,
			GhWebhookSecret:    deps.GhWebhookSecret,
			SlackSigningSecret: deps.SlackSigningSecret,
			GoogleChannelToken: deps.GoogleChannelToken,
			DefaultWorkspaceID: deps.DefaultWorkspaceID,
		},
		calDeps: calendars.Deps{
			Queries:         deps.Queries,
			CalendarQueries: deps.CalendarQueries,
			DB:              deps.DB,
			EmailSender:     deps.EmailSender,
			EmailFrom:       deps.EmailFrom,
			FlowWebURL:      deps.FlowWebURL,
		},
	}
}

// newSubAPI constructs a fresh humachi.API on the given chi router.
// Each huma.API needs its own huma.Config so it gets a fresh schema
// registry and its own *OpenAPI document; sharing one config between
// sub-APIs would point every group at the same registry and panic on
// duplicate anonymous "ListOutputBody" style schema names.
func newSubAPI(sub chi.Router) huma.API {
	return humachi.New(sub, huma.DefaultConfig("nodate-flow", "0.0.0"))
}

// buildAuthenticatedAPI registers every route that requires a valid
// bearer token. The supplied authMW is attached to each chi group so
// every handler runs through the auth resolver before reaching the
// huma operation. The returned slice contains one huma.API per chi
// group, each carrying the OpenAPI document that the static check
// tests (TestAuthenticatedSubRouterAlwaysAuthenticated) walk to assert
// 401 on every (method, path) when called without credentials.
func buildAuthenticatedAPI(r chi.Router, deps Deps, shared *sharedDeps, authMW func(http.Handler) http.Handler) []huma.API {
	apis := make([]huma.API, 0, 8)

	// /workspaces/{wsId} member-level reads: project list, lenses, AI, etc.
	r.Group(func(sub chi.Router) {
		sub.Use(authMW)
		sub.Use(middleware.RequireWorkspaceMember(shared.aclDB))
		subAPI := newSubAPI(sub)
		apis = append(apis, subAPI)

		huma.Register(subAPI, huma.Operation{
			OperationID: "projects-list",
			Method:      http.MethodGet,
			Path:        "/workspaces/{wsId}/projects",
			Summary:     "List projects in a workspace",
		}, projects.List(shared.prjDeps))
		labelDeps := labels.Deps{DB: deps.DB, Queries: deps.Queries, Audit: shared.auditRec}
		labels.RegisterWorkspaceScoped(subAPI, labelDeps)
		lensDeps := lenses.Deps{DB: deps.DB, Queries: deps.Queries, Audit: shared.auditRec}
		lenses.RegisterWorkspaceScoped(subAPI, lensDeps)
		exportDeps := exporthandlers.Deps{DB: deps.DB, Queries: deps.Queries, Audit: shared.auditRec}
		exporthandlers.RegisterWorkspaceScoped(subAPI, sub, exportDeps)
		tbDeps := timeboxes.Deps{DB: deps.DB, Queries: deps.Queries, Audit: shared.auditRec}
		timeboxes.RegisterWorkspaceScoped(subAPI, tbDeps)
		dashDeps := dashboard.Deps{DB: deps.DB, Queries: deps.Queries, Audit: shared.auditRec}
		dashboard.RegisterWorkspaceScoped(subAPI, dashDeps)
		pageDeps := pages.Deps{DB: deps.DB, Queries: deps.Queries, Audit: shared.auditRec}
		pages.RegisterWorkspaceScoped(subAPI, pageDeps)
		huma.Register(subAPI, huma.Operation{
			OperationID: "ai-cost-today",
			Method:      http.MethodGet,
			Path:        "/workspaces/{wsId}/ai/cost-today",
			Summary:     "Today's accumulated LLM spend (USD) for a workspace",
		}, aihandlers.CostToday(shared.aiDeps))
		huma.Register(subAPI, huma.Operation{
			OperationID: "ai-metrics",
			Method:      http.MethodGet,
			Path:        "/workspaces/{wsId}/ai/metrics",
			Summary:     "AI suggestion acceptance metrics over a trailing window",
		}, aihandlers.Metrics(shared.aiDeps))
		huma.Register(subAPI, huma.Operation{
			OperationID: "ai-agent-pause",
			Method:      http.MethodPost,
			Path:        "/workspaces/{wsId}/ai/agents/{agentId}/pause",
			Summary:     "Toggle the kill switch on an AI agent",
		}, aihandlers.PauseAgent(shared.aiDeps))
		huma.Register(subAPI, huma.Operation{
			OperationID: "ai-agents-list",
			Method:      http.MethodGet,
			Path:        "/workspaces/{wsId}/ai/agents",
			Summary:     "List AI agents for a workspace",
		}, aihandlers.ListAgents(shared.aiDeps))
		huma.Register(subAPI, huma.Operation{
			OperationID: "ai-agents-create",
			Method:      http.MethodPost,
			Path:        "/workspaces/{wsId}/ai/agents",
			Summary:     "Create a new AI agent",
		}, aihandlers.CreateAgent(shared.aiDeps))
		huma.Register(subAPI, huma.Operation{
			OperationID: "ai-agent-schedule-update",
			Method:      http.MethodPatch,
			Path:        "/workspaces/{wsId}/ai/agents/{agentId}/schedule",
			Summary:     "Update an AI agent's schedule_kind trigger mode",
		}, aihandlers.UpdateAgentSchedule(shared.aiDeps))
		huma.Register(subAPI, huma.Operation{
			OperationID: "ai-agent-event-triggers-update",
			Method:      http.MethodPatch,
			Path:        "/workspaces/{wsId}/ai/agents/{agentId}/event-triggers",
			Summary:     "Replace an AI agent's event_trigger_types list",
		}, aihandlers.UpdateAgentEventTriggers(shared.aiDeps))
		huma.Register(subAPI, huma.Operation{
			OperationID: "ai-agent-trigger",
			Method:      http.MethodPost,
			Path:        "/workspaces/{wsId}/ai/agents/{agentId}/trigger",
			Summary:     "Manually trigger one run of an AI agent",
		}, aihandlers.TriggerAgent(shared.aiDeps, deps.AgentQueue, deps.AgentRunner))
		huma.Register(subAPI, huma.Operation{
			OperationID: "ai-models-list",
			Method:      http.MethodGet,
			Path:        "/workspaces/{wsId}/ai/models",
			Summary:     "List workspace AI models across all providers",
		}, aihandlers.ListModels(shared.aiDeps))
		huma.Register(subAPI, huma.Operation{
			OperationID: "ai-priority-suggestions-list",
			Method:      http.MethodGet,
			Path:        "/workspaces/{wsId}/ai/priority-suggestions",
			Summary:     "Suggest priority adjustments for open tasks in a workspace",
		}, aihandlers.ListPrioritySuggestions(shared.aiDeps))
		huma.Register(subAPI, huma.Operation{
			OperationID: "ai-compile-lens",
			Method:      http.MethodPost,
			Path:        "/workspaces/{wsId}/ai/compile-lens",
			Summary:     "Compile natural-language prose into a validated Lens JSON (ADR 0004)",
		}, aihandlers.CompileLens(shared.aiDeps))
		huma.Register(subAPI, huma.Operation{
			OperationID: "ai-state-suggestions",
			Method:      http.MethodGet,
			Path:        "/workspaces/{wsId}/ai/state-suggestions",
			Summary:     "Workspace-wide deterministic state inference proposals",
		}, aihandlers.ListStateSuggestions(shared.aiDeps))
		huma.Register(subAPI, huma.Operation{
			OperationID: "ai-reminders",
			Method:      http.MethodGet,
			Path:        "/workspaces/{wsId}/ai/reminders",
			Summary:     "Workspace-wide deterministic reminder engine proposals",
		}, aihandlers.ListReminders(shared.aiDeps))
		huma.Register(subAPI, huma.Operation{
			OperationID: "ai-auto-actions",
			Method:      http.MethodGet,
			Path:        "/workspaces/{wsId}/ai/auto-actions",
			Summary:     "Workspace-wide deterministic auto-action proposals",
		}, aihandlers.ListAutoActions(shared.aiDeps))
		huma.Register(subAPI, huma.Operation{
			OperationID: "ai-weekly-digest",
			Method:      http.MethodGet,
			Path:        "/workspaces/{wsId}/ai/weekly-digest",
			Summary:     "Deterministic weekly digest markdown for a workspace",
		}, aihandlers.WeeklyDigest(shared.aiDeps))
		huma.Register(subAPI, huma.Operation{
			OperationID: "ai-invocations-list",
			Method:      http.MethodGet,
			Path:        "/workspaces/{wsId}/ai/invocations",
			Summary:     "List redacted LLM call audit rows for the AI reasoning panel",
		}, aihandlers.ListInvocations(shared.aiDeps))
		huma.Register(subAPI, huma.Operation{
			OperationID: "ai-resolve-command",
			Method:      http.MethodPost,
			Path:        "/workspaces/{wsId}/ai/resolve-command",
			Summary:     "Resolve a natural-language command into an MCP tool invocation",
		}, aihandlers.ResolveCommand(shared.aiDeps))

		// Workspace-scoped archived task listing.
		tasks.RegisterWorkspaceScoped(subAPI, shared.taskDeps)

		// AI-powered smart task creation (propose + apply).
		smartCreateDeps := tasks.SmartCreateDeps{DB: deps.DB, Queries: deps.Queries, AI: shared.aiOrch, Embedder: shared.embedClient, Audit: shared.auditRec}
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
		notifications.RegisterWorkspaceScoped(subAPI, shared.notifDeps)
		relationDeps := relations.Deps{DB: deps.DB, Queries: deps.Queries, Audit: shared.auditRec}
		relations.RegisterWorkspaceScoped(subAPI, relationDeps)
		intakeDeps := intakehandlers.Deps{DB: deps.DB, Queries: deps.Queries, Audit: shared.auditRec}
		intakehandlers.Register(subAPI, intakeDeps)
		importDeps := importhandlers.Deps{DB: deps.DB, Queries: deps.Queries, Audit: shared.auditRec}
		importhandlers.Register(subAPI, importDeps)
	})

	// Per-user MCP tokens (workspace member, not admin).
	r.Group(func(sub chi.Router) {
		sub.Use(authMW)
		sub.Use(middleware.RequireWorkspaceMember(shared.aclDB))
		subAPI := newSubAPI(sub)
		apis = append(apis, subAPI)
		aihandlers.RegisterMcpTokens(subAPI, shared.aiDeps)
	})

	// Workspace admin routes: AI providers, project create, webhooks.
	r.Group(func(sub chi.Router) {
		sub.Use(authMW)
		sub.Use(middleware.RequireWorkspaceMember(shared.aclDB))
		sub.Use(middleware.RequireWorkspaceRole(middleware.WorkspaceRoleAdmin))
		subAPI := newSubAPI(sub)
		apis = append(apis, subAPI)
		huma.Register(subAPI, huma.Operation{
			OperationID: "projects-create",
			Method:      http.MethodPost,
			Path:        "/workspaces/{wsId}/projects",
			Summary:     "Create a project in a workspace",
		}, projects.Create(shared.prjDeps))
		aihandlers.RegisterProviders(subAPI, shared.aiDeps)
		aihandlers.RegisterAutoActionSettings(subAPI, shared.aiDeps)
		aihandlers.RegisterAutoActionRules(subAPI, shared.aiDeps)
		webhookDeps := webhooks.Deps{DB: deps.DB, Queries: deps.Queries, Audit: shared.auditRec}
		webhooks.Register(subAPI, webhookDeps)
		auditHandlerDeps := audithandlers.Deps{DB: deps.DB, Queries: deps.Queries}
		audithandlers.Register(subAPI, auditHandlerDeps)
	})

	// /projects/{prjId}.
	r.Group(func(sub chi.Router) {
		sub.Use(authMW)
		sub.Use(middleware.RequireProjectMemberByGlobalId(shared.aclDB))
		subAPI := newSubAPI(sub)
		apis = append(apis, subAPI)
		projects.RegisterGlobal(subAPI, shared.prjDeps)
		timeline.RegisterProjectScoped(subAPI, shared.tlDeps)
	})

	// Task collection routes.
	r.Group(func(sub chi.Router) {
		sub.Use(authMW)
		subAPI := newSubAPI(sub)
		apis = append(apis, subAPI)
		tasks.RegisterCollection(subAPI, shared.taskDeps)
	})

	// Task-scoped routes + task timeline.
	r.Group(func(sub chi.Router) {
		sub.Use(authMW)
		sub.Use(middleware.RequireTaskAccess(shared.aclDB))
		subAPI := newSubAPI(sub)
		apis = append(apis, subAPI)
		tasks.RegisterTaskScoped(subAPI, shared.taskDeps)
		labelTaskDeps := labels.Deps{DB: deps.DB, Queries: deps.Queries, Audit: shared.auditRec}
		labels.RegisterTaskScoped(subAPI, labelTaskDeps)
		timeline.RegisterTaskScoped(subAPI, shared.tlDeps)
		relationTaskDeps := relations.Deps{DB: deps.DB, Queries: deps.Queries, Audit: shared.auditRec}
		relations.RegisterTaskScoped(subAPI, relationTaskDeps)
		reactionDeps := reactions.Deps{DB: deps.DB, Queries: deps.Queries, Audit: shared.auditRec}
		reactions.RegisterTaskScoped(subAPI, reactionDeps)

		// AI-powered step decomposition (propose + apply).
		stepsDeps := tasks.StepsDeps{DB: deps.DB, Queries: deps.Queries, AI: shared.aiOrch, Embedder: shared.embedClient, Audit: shared.auditRec}
		tasks.RegisterSteps(subAPI, stepsDeps)
	})

	// Workspace timeline.
	r.Group(func(sub chi.Router) {
		sub.Use(authMW)
		sub.Use(middleware.RequireWorkspaceMember(shared.aclDB))
		subAPI := newSubAPI(sub)
		apis = append(apis, subAPI)
		timeline.RegisterWorkspaceScoped(subAPI, shared.tlDeps)
	})

	// Signals + inbox (auth only; handlers resolve ws membership themselves).
	r.Group(func(sub chi.Router) {
		sub.Use(authMW)
		subAPI := newSubAPI(sub)
		apis = append(apis, subAPI)
		signals.RegisterCollection(subAPI, shared.signalDeps)
		inbox.Register(subAPI, shared.inboxDeps)
		notifications.Register(subAPI, shared.notifDeps)
		favDeps := favorites.Deps{DB: deps.DB, Queries: deps.Queries, Audit: shared.auditRec}
		favorites.Register(subAPI, favDeps)
		relationAuthDeps := relations.Deps{DB: deps.DB, Queries: deps.Queries, Audit: shared.auditRec}
		relations.RegisterAuthScoped(subAPI, relationAuthDeps)
	})

	// MCP server uses the orchestrator built above. The SSE event hook
	// is registered so workspace events are broadcast to connected MCP
	// clients in real time. The /mcp endpoint enforces auth itself
	// (Bearer mcp_ tokens) so it lives outside the Huma sub-API surface.
	mcpHandler := mcp.NewHandler(mcp.Deps{DB: deps.DB, Queries: deps.Queries, CalendarQueries: deps.CalendarQueries, AI: shared.aiOrch, Embedder: shared.embedClient, NlQuery: shared.nlQueryCompiler})
	eventbus.AddNotifyHook(mcpHandler.RegisterEventHook())
	r.Handle("/mcp", mcpHandler)

	// Relation auto-detect pipeline (INTEL-3). Fires on task.created
	// and task.updated events, creates relation_suggestions via
	// embedding similarity in a background goroutine.
	if shared.embedClient != nil {
		relationPipeline := &airelations.Pipeline{DB: deps.DB, Queries: deps.Queries, Embedder: shared.embedClient}
		eventbus.AddNotifyHook(relationPipeline.Hook())
	}

	// Workspace-scoped AI inbox triage. Registered in
	// its own group so the auth + workspace-member middleware applies
	// without leaking the orchestrator to the v1 inbox routes.
	r.Group(func(sub chi.Router) {
		sub.Use(authMW)
		sub.Use(middleware.RequireWorkspaceMember(shared.aclDB))
		subAPI := newSubAPI(sub)
		apis = append(apis, subAPI)
		triageDeps := inbox.TriageDeps{Deps: shared.inboxDeps, AI: shared.aiOrch}
		inbox.RegisterTriage(subAPI, triageDeps)
		inbox.RegisterAiSuggestions(subAPI, triageDeps)
	})

	// Calendar surface (relocated from time-api per ADR 0007). The
	// authenticated calendar routes split into two membership tiers:
	// workspace-member level (calendar list/create, public-share admin,
	// /me/* inbox endpoints) and calendar-member level (single calendar
	// CRUD, events, attendees, etc.). Each tier mounts its own chi group
	// so the middleware stack stays minimal for the workspace-only side.
	calMW := middleware.RequireCalendarMember(shared.aclDB)

	// Workspace-scoped calendar routes. RequireAuth only — calendar
	// membership is enforced at the query layer (ListCalendarsForUser
	// scopes by user_id) or at the handler level (resolveWorkspace).
	r.Group(func(sub chi.Router) {
		sub.Use(authMW)
		subAPI := newSubAPI(sub)
		apis = append(apis, subAPI)

		huma.Register(subAPI, huma.Operation{
			OperationID: "calendars-list",
			Method:      http.MethodGet,
			Path:        "/workspaces/{wsId}/calendars",
			Summary:     "List calendars in a workspace",
		}, calendars.ListCalendars(shared.calDeps))
		huma.Register(subAPI, huma.Operation{
			OperationID: "calendars-create",
			Method:      http.MethodPost,
			Path:        "/workspaces/{wsId}/calendars",
			Summary:     "Create a calendar",
		}, calendars.CreateCalendar(shared.calDeps))
		huma.Register(subAPI, huma.Operation{
			OperationID: "calendars-subscribe-system",
			Method:      http.MethodPost,
			Path:        "/workspaces/{wsId}/calendars/subscribe-system",
			Summary:     "Subscribe the caller to the holiday feed for a country",
		}, calendars.SubscribeSystemCalendar(shared.calDeps))
		huma.Register(subAPI, huma.Operation{
			OperationID: "discoverable-calendars-list",
			Method:      http.MethodGet,
			Path:        "/workspaces/{wsId}/discoverable-calendars",
			Summary:     "List teammate personal calendars the caller can subscribe to",
		}, calendars.ListDiscoverableCalendars(shared.calDeps))
		huma.Register(subAPI, huma.Operation{
			OperationID: "calendars-self-subscribe",
			Method:      http.MethodPost,
			Path:        "/workspaces/{wsId}/calendars/{calId}/subscribe",
			Summary:     "Subscribe the caller to a calendar visible in the workspace",
		}, calendars.SelfSubscribe(shared.calDeps))
		huma.Register(subAPI, huma.Operation{
			OperationID: "calendar-events-list",
			Method:      http.MethodGet,
			Path:        "/workspaces/{wsId}/calendar-events",
			Summary:     "List events across all calendars in a workspace",
		}, calendars.ListCalendarEvents(shared.calDeps))
		huma.Register(subAPI, huma.Operation{
			OperationID: "me-calendar-events-list",
			Method:      http.MethodGet,
			Path:        "/me/calendar-events",
			Summary:     "List events across every workspace the caller belongs to",
		}, calendars.ListMyCalendarEvents(shared.calDeps))
		huma.Register(subAPI, huma.Operation{
			OperationID: "me-invites-list",
			Method:      http.MethodGet,
			Path:        "/me/invites",
			Summary:     "List pending event invites addressed to the caller",
		}, calendars.ListMyInvites(shared.calDeps))

		// Public share admin endpoints (workspace-scoped).
		huma.Register(subAPI, huma.Operation{
			OperationID: "public-shares-list",
			Method:      http.MethodGet,
			Path:        "/workspaces/{wsId}/public-shares",
			Summary:     "List public share pages in a workspace",
		}, calendars.ListPublicShares(shared.calDeps))
		huma.Register(subAPI, huma.Operation{
			OperationID: "public-shares-create",
			Method:      http.MethodPost,
			Path:        "/workspaces/{wsId}/public-shares",
			Summary:     "Create a public share page (returns plaintext token once)",
		}, calendars.CreatePublicShare(shared.calDeps))
		huma.Register(subAPI, huma.Operation{
			OperationID: "public-shares-get",
			Method:      http.MethodGet,
			Path:        "/workspaces/{wsId}/public-shares/{shareId}",
			Summary:     "Get a public share page with its published events",
		}, calendars.GetPublicShare(shared.calDeps))
		huma.Register(subAPI, huma.Operation{
			OperationID: "public-shares-patch",
			Method:      http.MethodPatch,
			Path:        "/workspaces/{wsId}/public-shares/{shareId}",
			Summary:     "Update public share page metadata",
		}, calendars.PatchPublicShare(shared.calDeps))
		huma.Register(subAPI, huma.Operation{
			OperationID: "public-shares-rotate",
			Method:      http.MethodPost,
			Path:        "/workspaces/{wsId}/public-shares/{shareId}/rotate",
			Summary:     "Rotate the URL token for a public share page",
		}, calendars.RotatePublicShareToken(shared.calDeps))
		huma.Register(subAPI, huma.Operation{
			OperationID: "public-shares-delete",
			Method:      http.MethodDelete,
			Path:        "/workspaces/{wsId}/public-shares/{shareId}",
			Summary:     "Delete a public share page (admin or owner only)",
		}, calendars.DeletePublicShare(shared.calDeps))
		huma.Register(subAPI, huma.Operation{
			OperationID: "public-shares-events-attach",
			Method:      http.MethodPost,
			Path:        "/workspaces/{wsId}/public-shares/{shareId}/events",
			Summary:     "Attach events to a public share page",
		}, calendars.AttachEventsToShare(shared.calDeps))
		huma.Register(subAPI, huma.Operation{
			OperationID: "public-shares-events-detach",
			Method:      http.MethodDelete,
			Path:        "/workspaces/{wsId}/public-shares/{shareId}/events/{evtId}",
			Summary:     "Detach an event from a public share page",
		}, calendars.DetachEventFromShare(shared.calDeps))
		huma.Register(subAPI, huma.Operation{
			OperationID: "public-shares-events-reorder",
			Method:      http.MethodPatch,
			Path:        "/workspaces/{wsId}/public-shares/{shareId}/events/reorder",
			Summary:     "Batch-reorder the events published on a public share page",
		}, calendars.ReorderShareEvents(shared.calDeps))
	})

	// Calendar-scoped routes. Use RequireAuth + RequireCalendarMember,
	// so the {calId} URL param is resolved up-front and the actor's
	// subscription / membership is verified before the handler runs.
	r.Group(func(sub chi.Router) {
		sub.Use(authMW)
		sub.Use(calMW)
		subAPI := newSubAPI(sub)
		apis = append(apis, subAPI)

		// Single calendar CRUD.
		huma.Register(subAPI, huma.Operation{
			OperationID: "calendars-get",
			Method:      http.MethodGet,
			Path:        "/workspaces/{wsId}/calendars/{calId}",
			Summary:     "Get a calendar",
		}, calendars.GetCalendar(shared.calDeps))
		huma.Register(subAPI, huma.Operation{
			OperationID: "calendars-patch",
			Method:      http.MethodPatch,
			Path:        "/workspaces/{wsId}/calendars/{calId}",
			Summary:     "Update a calendar",
		}, calendars.PatchCalendar(shared.calDeps))
		huma.Register(subAPI, huma.Operation{
			OperationID: "calendars-delete",
			Method:      http.MethodDelete,
			Path:        "/workspaces/{wsId}/calendars/{calId}",
			Summary:     "Delete a calendar",
		}, calendars.DeleteCalendar(shared.calDeps))

		// Events within a calendar.
		huma.Register(subAPI, huma.Operation{
			OperationID: "events-list",
			Method:      http.MethodGet,
			Path:        "/workspaces/{wsId}/calendars/{calId}/events",
			Summary:     "List events in a calendar",
		}, calendars.ListEvents(shared.calDeps))
		huma.Register(subAPI, huma.Operation{
			OperationID: "events-create",
			Method:      http.MethodPost,
			Path:        "/workspaces/{wsId}/calendars/{calId}/events",
			Summary:     "Create an event",
		}, calendars.CreateEvent(shared.calDeps))
		huma.Register(subAPI, huma.Operation{
			OperationID: "events-get",
			Method:      http.MethodGet,
			Path:        "/workspaces/{wsId}/calendars/{calId}/events/{evtId}",
			Summary:     "Get an event",
		}, calendars.GetEvent(shared.calDeps))
		huma.Register(subAPI, huma.Operation{
			OperationID: "events-patch",
			Method:      http.MethodPatch,
			Path:        "/workspaces/{wsId}/calendars/{calId}/events/{evtId}",
			Summary:     "Update an event",
		}, calendars.PatchEvent(shared.calDeps))
		huma.Register(subAPI, huma.Operation{
			OperationID: "events-delete",
			Method:      http.MethodDelete,
			Path:        "/workspaces/{wsId}/calendars/{calId}/events/{evtId}",
			Summary:     "Delete an event",
		}, calendars.DeleteEvent(shared.calDeps))
		huma.Register(subAPI, huma.Operation{
			OperationID: "events-smart-create",
			Method:      http.MethodPost,
			Path:        "/workspaces/{wsId}/calendars/{calId}/events/smart-create",
			Summary:     "Parse natural language text into an event proposal",
		}, calendars.SmartCreate(shared.calDeps))
		huma.Register(subAPI, huma.Operation{
			OperationID: "events-from-task",
			Method:      http.MethodPost,
			Path:        "/workspaces/{wsId}/calendars/{calId}/events/from-task",
			Summary:     "Create a calendar event from a task",
		}, calendars.CreateEventFromTask(shared.calDeps))

		// Calendar members.
		huma.Register(subAPI, huma.Operation{
			OperationID: "calendars-self-subscription-patch",
			Method:      http.MethodPatch,
			Path:        "/workspaces/{wsId}/calendars/{calId}/subscription",
			Summary:     "Update the caller's own subscription preferences for a calendar",
		}, calendars.PatchOwnSubscription(shared.calDeps))
		huma.Register(subAPI, huma.Operation{
			OperationID: "members-add",
			Method:      http.MethodPost,
			Path:        "/workspaces/{wsId}/calendars/{calId}/members",
			Summary:     "Add a member to a calendar",
		}, calendars.AddMember(shared.calDeps))
		huma.Register(subAPI, huma.Operation{
			OperationID: "members-list",
			Method:      http.MethodGet,
			Path:        "/workspaces/{wsId}/calendars/{calId}/members",
			Summary:     "List members of a calendar",
		}, calendars.ListMembers(shared.calDeps))
		huma.Register(subAPI, huma.Operation{
			OperationID: "members-update-role",
			Method:      http.MethodPatch,
			Path:        "/workspaces/{wsId}/calendars/{calId}/members/{userId}",
			Summary:     "Update a member's role",
		}, calendars.UpdateMemberRole(shared.calDeps))
		huma.Register(subAPI, huma.Operation{
			OperationID: "members-remove",
			Method:      http.MethodDelete,
			Path:        "/workspaces/{wsId}/calendars/{calId}/members/{userId}",
			Summary:     "Remove a member from a calendar",
		}, calendars.RemoveMember(shared.calDeps))

		// Event attendees.
		huma.Register(subAPI, huma.Operation{
			OperationID: "attendees-list",
			Method:      http.MethodGet,
			Path:        "/workspaces/{wsId}/calendars/{calId}/events/{evtId}/attendees",
			Summary:     "List attendees on an event",
		}, calendars.ListAttendees(shared.calDeps))
		huma.Register(subAPI, huma.Operation{
			OperationID: "attendees-add",
			Method:      http.MethodPost,
			Path:        "/workspaces/{wsId}/calendars/{calId}/events/{evtId}/attendees",
			Summary:     "Add attendees to an event",
		}, calendars.AddAttendees(shared.calDeps))
		huma.Register(subAPI, huma.Operation{
			OperationID: "attendees-remove",
			Method:      http.MethodDelete,
			Path:        "/workspaces/{wsId}/calendars/{calId}/events/{evtId}/attendees/{userId}",
			Summary:     "Remove an attendee from an event",
		}, calendars.RemoveAttendee(shared.calDeps))
		huma.Register(subAPI, huma.Operation{
			OperationID: "attendees-rsvp",
			Method:      http.MethodPatch,
			Path:        "/workspaces/{wsId}/calendars/{calId}/events/{evtId}/attendees/rsvp",
			Summary:     "Update own RSVP for an event",
		}, calendars.UpdateRsvp(shared.calDeps))
		huma.Register(subAPI, huma.Operation{
			OperationID: "attendees-can-edit",
			Method:      http.MethodPatch,
			Path:        "/workspaces/{wsId}/calendars/{calId}/events/{evtId}/attendees/{userId}/can-edit",
			Summary:     "Toggle can_edit for an attendee",
		}, calendars.ToggleCanEdit(shared.calDeps))

		// Event invites (magic link).
		huma.Register(subAPI, huma.Operation{
			OperationID: "event-invites-create",
			Method:      http.MethodPost,
			Path:        "/workspaces/{wsId}/calendars/{calId}/events/{evtId}/attendees/{attendeeId}/invite",
			Summary:     "Create (or rotate) a magic-link invite for an attendee",
		}, calendars.CreateEventInvite(shared.calDeps))
		huma.Register(subAPI, huma.Operation{
			OperationID: "event-invites-list",
			Method:      http.MethodGet,
			Path:        "/workspaces/{wsId}/calendars/{calId}/events/{evtId}/invites",
			Summary:     "List active magic-link invites for an event",
		}, calendars.ListEventInvites(shared.calDeps))
		huma.Register(subAPI, huma.Operation{
			OperationID: "event-invites-revoke",
			Method:      http.MethodDelete,
			Path:        "/workspaces/{wsId}/calendars/{calId}/events/{evtId}/invites/{inviteId}",
			Summary:     "Revoke a magic-link invite",
		}, calendars.RevokeEventInvite(shared.calDeps))

		// Event comments.
		huma.Register(subAPI, huma.Operation{
			OperationID: "comments-list",
			Method:      http.MethodGet,
			Path:        "/workspaces/{wsId}/calendars/{calId}/events/{evtId}/comments",
			Summary:     "List comments on an event",
		}, calendars.ListComments(shared.calDeps))
		huma.Register(subAPI, huma.Operation{
			OperationID: "comments-create",
			Method:      http.MethodPost,
			Path:        "/workspaces/{wsId}/calendars/{calId}/events/{evtId}/comments",
			Summary:     "Add a comment to an event",
		}, calendars.CreateComment(shared.calDeps))
		huma.Register(subAPI, huma.Operation{
			OperationID: "comments-edit",
			Method:      http.MethodPatch,
			Path:        "/workspaces/{wsId}/calendars/{calId}/events/{evtId}/comments/{cId}",
			Summary:     "Edit a comment",
		}, calendars.EditComment(shared.calDeps))
		huma.Register(subAPI, huma.Operation{
			OperationID: "comments-delete",
			Method:      http.MethodDelete,
			Path:        "/workspaces/{wsId}/calendars/{calId}/events/{evtId}/comments/{cId}",
			Summary:     "Delete a comment",
		}, calendars.DeleteComment(shared.calDeps))

		// Event checklist.
		huma.Register(subAPI, huma.Operation{
			OperationID: "checklist-list",
			Method:      http.MethodGet,
			Path:        "/workspaces/{wsId}/calendars/{calId}/events/{evtId}/checklist",
			Summary:     "List checklist items for an event",
		}, calendars.ListChecklist(shared.calDeps))
		huma.Register(subAPI, huma.Operation{
			OperationID: "checklist-create",
			Method:      http.MethodPost,
			Path:        "/workspaces/{wsId}/calendars/{calId}/events/{evtId}/checklist",
			Summary:     "Add a checklist item to an event",
		}, calendars.CreateChecklistItem(shared.calDeps))
		huma.Register(subAPI, huma.Operation{
			OperationID: "checklist-update",
			Method:      http.MethodPatch,
			Path:        "/workspaces/{wsId}/calendars/{calId}/events/{evtId}/checklist/{itemId}",
			Summary:     "Update a checklist item",
		}, calendars.UpdateChecklistItem(shared.calDeps))
		huma.Register(subAPI, huma.Operation{
			OperationID: "checklist-delete",
			Method:      http.MethodDelete,
			Path:        "/workspaces/{wsId}/calendars/{calId}/events/{evtId}/checklist/{itemId}",
			Summary:     "Delete a checklist item",
		}, calendars.DeleteChecklistItem(shared.calDeps))

		// Calendar memos.
		huma.Register(subAPI, huma.Operation{
			OperationID: "memos-list",
			Method:      http.MethodGet,
			Path:        "/workspaces/{wsId}/calendars/{calId}/memos",
			Summary:     "List memos in a calendar",
		}, calendars.ListMemos(shared.calDeps))
		huma.Register(subAPI, huma.Operation{
			OperationID: "memos-create",
			Method:      http.MethodPost,
			Path:        "/workspaces/{wsId}/calendars/{calId}/memos",
			Summary:     "Create a memo",
		}, calendars.CreateMemo(shared.calDeps))
		huma.Register(subAPI, huma.Operation{
			OperationID: "memos-update",
			Method:      http.MethodPatch,
			Path:        "/workspaces/{wsId}/calendars/{calId}/memos/{memoId}",
			Summary:     "Update a memo",
		}, calendars.UpdateMemo(shared.calDeps))
		huma.Register(subAPI, huma.Operation{
			OperationID: "memos-delete",
			Method:      http.MethodDelete,
			Path:        "/workspaces/{wsId}/calendars/{calId}/memos/{memoId}",
			Summary:     "Delete a memo",
		}, calendars.DeleteMemo(shared.calDeps))

		// Event attachments.
		huma.Register(subAPI, huma.Operation{
			OperationID: "attachments-list",
			Method:      http.MethodGet,
			Path:        "/workspaces/{wsId}/calendars/{calId}/events/{evtId}/attachments",
			Summary:     "List attachments on an event",
		}, calendars.ListAttachments(shared.calDeps))
		huma.Register(subAPI, huma.Operation{
			OperationID: "attachments-create",
			Method:      http.MethodPost,
			Path:        "/workspaces/{wsId}/calendars/{calId}/events/{evtId}/attachments",
			Summary:     "Record attachment metadata for an event",
		}, calendars.CreateAttachment(shared.calDeps))
		huma.Register(subAPI, huma.Operation{
			OperationID: "attachments-delete",
			Method:      http.MethodDelete,
			Path:        "/workspaces/{wsId}/calendars/{calId}/events/{evtId}/attachments/{attId}",
			Summary:     "Delete an attachment from an event",
		}, calendars.DeleteAttachment(shared.calDeps))
	})

	return apis
}

// buildPublicShareAPI registers the unauthenticated public surface.
// These routes deliberately skip RequireAuth and instead rely on
// signature verification (webhooks), opaque share tokens (public
// lenses, future /share/cal/* and /invites/{token}/*), or no
// authentication at all (/health). The sub-router gets its own per-IP
// rate limiter so public abuse cannot exhaust the global budget.
//
// The public calendar share and event-invite endpoints
// (/share/cal/{token}, /invites/{token}/info, /invites/{token}/accept)
// were relocated here from the retired time-api binary per ADR 0007
// and are registered inside this builder.
func buildPublicShareAPI(r chi.Router, deps Deps, shared *sharedDeps) []huma.API {
	apis := make([]huma.API, 0, 2)

	// /health is auth-free but deliberately exempt from the public
	// per-IP rate limiter: kubelet liveness/readiness probes hit it
	// every few seconds and would otherwise be 429'd. The global
	// per-IP limiter still applies; that one is sized for normal
	// request volume.
	r.Group(func(sub chi.Router) {
		subAPI := newSubAPI(sub)
		apis = append(apis, subAPI)

		huma.Register(subAPI, huma.Operation{
			OperationID: "health",
			Method:      http.MethodGet,
			Path:        "/health",
			Summary:     "Health check",
		}, func(_ context.Context, _ *struct{}) (*healthOutput, error) {
			out := &healthOutput{}
			out.Body.Status = "ok"
			return out, nil
		})
	})

	// Public share / invite Huma operations live behind a tight
	// per-IP rate limiter so the unauthenticated surface cannot be
	// abused to drain the global budget. Step 0-2 (ADR 0007) will
	// add /share/cal/{token}, /invites/{token}/info,
	// /invites/{token}/accept under this same group.
	r.Group(func(sub chi.Router) {
		// Independent per-IP rate limiter for the public surface. The
		// limit (30 req / 15 min) is intentionally tight so a single
		// IP cannot grind the share endpoints. The global limiter
		// still applies on top of this; both have to allow the
		// request through.
		publicRL := middleware.NewIPRateLimiter(middleware.RateLimitConfig{
			MaxRequests: 30,
			Window:      15 * time.Minute,
		})
		sub.Use(publicRL.Middleware())
		subAPI := newSubAPI(sub)
		apis = append(apis, subAPI)

		publicLensDeps := lenses.Deps{DB: deps.DB, Queries: deps.Queries, Audit: shared.auditRec}
		lenses.RegisterPublic(subAPI, publicLensDeps)

		// Calendar public share render and event-invite accept
		// (relocated from time-api per ADR 0007). Both are
		// unauthenticated; the opaque token is the capability.
		calDeps := shared.calDeps
		huma.Register(subAPI, huma.Operation{
			OperationID: "public-shares-render",
			Method:      http.MethodGet,
			Path:        "/share/cal/{token}",
			Summary:     "Render a public calendar share by URL token",
		}, calendars.RenderPublicShare(calDeps))
		huma.Register(subAPI, huma.Operation{
			OperationID: "event-invites-accept",
			Method:      http.MethodPost,
			Path:        "/public/invites/accept",
			Summary:     "Accept a calendar event invite via magic-link token",
		}, calendars.AcceptEventInvite(calDeps))
	})

	// Webhook receivers are technically unauthenticated but verify
	// their own HMAC / signing secrets inside the handler. They are
	// registered as raw chi routes (not Huma operations) because the
	// payload shapes are dictated by external services and do not fit
	// the Huma DTO model.
	r.Post("/webhooks/github", signals.HandleGithubWebhook(shared.signalDeps))
	r.Post("/webhooks/slack", signals.HandleSlackWebhook(shared.signalDeps))
	r.Post("/webhooks/google", signals.HandleGoogleWebhook(shared.signalDeps))

	return apis
}

// buildAuthAPI is the placeholder builder for the auth/identity surface
// of flow-api. flow-api does not own login, refresh, or session
// endpoints today (those live in apps/auth-api/). The builder exists so
// future migrations — for example, folding auth-api into flow-api or
// adding /me/invites flows from the time-api retirement (ADR 0007) —
// have a stable insertion point that the static check tests already
// exempt from the public-router assertion.
//
// The builder returns an empty slice today; once an auth route ships
// here it should attach its own middleware (rate limiter sized for
// login flows, CSRF if needed) and register operations on a fresh
// huma.API.
func buildAuthAPI(_ chi.Router, _ Deps, _ *sharedDeps) []huma.API {
	return nil
}

type healthOutput struct {
	Body struct {
		Status string `json:"status"`
	}
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
