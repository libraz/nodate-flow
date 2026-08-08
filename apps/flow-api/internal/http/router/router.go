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
	"strings"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"github.com/danielgtaylor/huma/v2/adapters/humachi"
	"github.com/go-chi/chi/v5"

	"github.com/libraz/nodate-flow/apps/flow-api/internal/ai"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/ai/agentruntime"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/ai/embed"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/ai/nlcommand"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/ai/nlconstraint"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/ai/nlquery"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/ai/providers"
	airelations "github.com/libraz/nodate-flow/apps/flow-api/internal/ai/relations"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/audit"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/auth"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/generated"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/generated/calendar"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/types"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/eventbus"
	activityhandlers "github.com/libraz/nodate-flow/apps/flow-api/internal/http/handlers/activity"
	aihandlers "github.com/libraz/nodate-flow/apps/flow-api/internal/http/handlers/ai"
	audithandlers "github.com/libraz/nodate-flow/apps/flow-api/internal/http/handlers/audit"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/http/handlers/calendars"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/http/handlers/dashboard"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/http/handlers/events"
	exporthandlers "github.com/libraz/nodate-flow/apps/flow-api/internal/http/handlers/export"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/http/handlers/favorites"
	importhandlers "github.com/libraz/nodate-flow/apps/flow-api/internal/http/handlers/imports"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/http/handlers/inbox"
	intakehandlers "github.com/libraz/nodate-flow/apps/flow-api/internal/http/handlers/intake"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/http/handlers/integrationmappings"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/http/handlers/internalapi"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/http/handlers/labels"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/http/handlers/lenses"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/http/handlers/notifications"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/http/handlers/pages"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/http/handlers/projects"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/http/handlers/reactions"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/http/handlers/relations"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/http/handlers/signals"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/http/handlers/tasks"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/http/handlers/timeboxes"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/http/handlers/timeline"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/http/handlers/webhooks"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/http/middleware"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/mcp"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/obs"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/storage"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/stream"
	"github.com/libraz/nodate-flow/packages/go-shared/apierr"
	"github.com/libraz/nodate-flow/packages/go-shared/authn"
	"github.com/libraz/nodate-flow/packages/go-shared/crypto"
	"github.com/libraz/nodate-flow/packages/go-shared/email"
	"github.com/libraz/nodate-flow/packages/go-shared/httputil"
	"github.com/libraz/nodate-flow/packages/go-shared/openapiutil"
	"github.com/libraz/nodate-flow/packages/go-shared/ratelimit"
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
	// DefaultWorkspaceID is the single-tenant fallback workspace public
	// id (UUID v7) for webhook-origin signals whose sender has no
	// integration_source_mappings row. Empty in tests, where the suite
	// runs many workspaces on one instance and the fallback is therefore
	// inapplicable by construction.
	DefaultWorkspaceID string
	// DisableRateLimit disables all per-IP rate limiters. Used by
	// integration tests where many parallel tenants register from
	// the same loopback address.
	DisableRateLimit bool
	TrustedProxyHops int
	// AiMock toggles the deterministic in-memory AI provider used by
	// development and tests. When true the orchestrator routes
	// every workspace to a fixture-backed Provider regardless of the
	// workspace.ai_providers rows.
	AiMock bool
	// AiDailyBudgetCents is the per-workspace daily LLM spend cap in
	// cents passed to ai.NewCostGuard. Zero falls back to
	// ai.DefaultDailyBudgetCents. Sourced from NF_FLOW_AI_DAILY_BUDGET_CENTS.
	AiDailyBudgetCents int64
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
	// JudgeEnqueuer is the optional signal_judge dispatch hook
	// invoked from /signals and the /webhooks/* handlers after a
	// signal row lands (ADR 0008 D3). Nil disables judge dispatch
	// for single-binary deployments that have not opted in.
	JudgeEnqueuer signals.JudgeEnqueuer

	// FlowAPISignalToken is the shared secret internal-only callers
	// (flow-worker, presence-discord) present in the Authorization
	// header when calling POST /signals or any /internal/* route.
	// Empty disables both service-token paths: /signals then accepts
	// only real-user bearers (JWT / PAT / MCP) and /internal/* rejects
	// every request with 401. The token is scoped to those two route
	// groups only — other endpoints reject it even when configured.
	FlowAPISignalToken string

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
	// WriteFloor is the ACL floor the operation's chi group applies to
	// mutating methods (one of the floor* constants). It is recorded by
	// the same call that mounts the middleware, so a group cannot claim a
	// floor it does not enforce.
	WriteFloor string
}

// aclFloor pairs the label a group records on its operations with the
// middleware that enforces it. Carrying both in one value means a caller
// picks the enforcement and the bookkeeping together instead of naming a
// floor and separately remembering to mount it.
//
// This narrows the mistake; it does not make it impossible. Deleting the
// Use call in mountGroup, or handing it an aclFloor literal with a label
// and no middleware, still produces a group that reports a floor and
// enforces nothing. The check that catches that is the chain test in
// acl_floor_chain_test.go, which drives requests through the middleware a
// group actually ended up with.
type aclFloor struct {
	// label identifies the floor in the operation inventory the static
	// checks walk. Empty for groups that enforce their ACL elsewhere.
	label string
	// mw enforces the floor. Nil only for floorNone.
	mw func(http.Handler) http.Handler
}

// Group ACL floors. A floor is the minimum role a mutating request must
// carry to reach any operation registered on the group.
var (
	// floorNone marks a group that mounts no role floor of its own. Every
	// such group is listed in the router's static check tests together
	// with the reason its operations are safe without one.
	floorNone = aclFloor{}
	// floorWorkspaceMember keeps guests (the read-only workspace role)
	// out of every mutating operation on the group.
	floorWorkspaceMember = aclFloor{
		label: "workspace:member",
		mw:    middleware.RequireWorkspaceRoleForWrites(middleware.WorkspaceRoleMember),
	}
	// floorWorkspaceAdmin restricts the whole group to workspace
	// admins / owners, reads included.
	floorWorkspaceAdmin = aclFloor{
		label: "workspace:admin",
		mw:    middleware.RequireWorkspaceRole(middleware.WorkspaceRoleAdmin),
	}
	// floorProjectCommenter / floorProjectEditor restrict the group to the
	// matching project role; they apply to reads as well because the
	// groups carrying them register only mutations.
	floorProjectCommenter = aclFloor{
		label: "project:commenter",
		mw:    middleware.RequireProjectRole(middleware.ProjectRoleCommenter),
	}
	floorProjectEditor = aclFloor{
		label: "project:editor",
		mw:    middleware.RequireProjectRole(middleware.ProjectRoleEditor),
	}
)

// groupAPI pairs a sub-API with the ACL floor of the chi group it was
// registered on.
type groupAPI struct {
	api   huma.API
	floor string
}

// plainGroups adapts a builder that returns bare huma.API values (the
// public and auth surfaces, which have no workspace role concept) into the
// groupAPI shape the snapshot uses.
func plainGroups(apis []huma.API) []groupAPI {
	out := make([]groupAPI, 0, len(apis))
	for _, a := range apis {
		out = append(out, groupAPI{api: a, floor: floorNone.label})
	}
	return out
}

// snapshotOps reads each sub-API's OpenAPI document and emits one
// OperationRef per registered (verb, path) pair, tagged with its group's
// ACL floor. It MUST be called before mergeAPIs, because that function
// mutates apis[0]'s OpenAPI document in place to host every other
// sub-API's paths.
func snapshotOps(groups []groupAPI) []OperationRef {
	var ops []OperationRef
	for _, g := range groups {
		a := g.api
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
					WriteFloor:  g.floor,
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
	r.Use(middleware.ClientIP(deps.TrustedProxyHops))
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

	rawAuthMW := middleware.RequireAuth(middleware.AuthDeps{
		JWT:     deps.JWT,
		Queries: deps.Queries,
		DB:      shared.aclDB,
	})
	// Wrap RequireAuth with LoggerContext so every authenticated route
	// receives a request-scoped logger pre-populated with actor_id and
	// request_id once auth resolves. Workspace-scoped attrs (workspace_id
	// / workspace_public_id) are appended by RequireWorkspaceMember (and
	// the project / task ACL helpers) once the workspace is resolved,
	// so each builder gets a fully-tagged logger without having to thread
	// a "log-after-acl" wrapper through every group.
	//
	// The PAT / MCP workspace binding is enforced in the same chain rather
	// than next to the individual ACL middlewares, so a token minted for one
	// workspace cannot be replayed on any route in the table — including the
	// ones that resolve their workspace elsewhere, or not at all. See
	// middleware.RequireTokenWorkspaceBinding.
	loggerCtx := middleware.LoggerContext()
	tokenWorkspaceMW := middleware.RequireTokenWorkspaceBinding(shared.aclDB)
	authMW := func(next http.Handler) http.Handler {
		return rawAuthMW(middleware.RequireBearerTokenScope(tokenWorkspaceMW(loggerCtx(next))))
	}

	authedGroups := buildAuthenticatedAPI(r, deps, shared, authMW)
	publicGroups := plainGroups(buildPublicShareAPI(r, deps, shared))
	authGroups := plainGroups(buildAuthAPI(r, deps, shared))

	// Snapshot the per-builder operation set before mergeAPIs mutates
	// apis[0]'s OpenAPI document. The static check tests rely on these
	// pristine slices to walk only the routes that belong to their
	// builder; once merged, every path appears under apis[0].
	authedOps := snapshotOps(authedGroups)
	publicOps := snapshotOps(publicGroups)
	authOps := snapshotOps(authGroups)

	// Aggregate every sub-API for OpenAPI merging. The order matters
	// for /openapi.json: authenticated paths come first so the rendered
	// document keeps the historical layout.
	apis := make([]huma.API, 0, len(authedGroups)+len(publicGroups)+len(authGroups))
	for _, g := range authedGroups {
		apis = append(apis, g.api)
	}
	for _, g := range publicGroups {
		apis = append(apis, g.api)
	}
	for _, g := range authGroups {
		apis = append(apis, g.api)
	}

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
	invocationLogger := newDBInvocationLogger(deps.Queries, deps.AiInvocationPublisher)

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
	if embedClient != nil {
		embedClient.WithMetering(
			&embedBudgetGuard{Queries: deps.Queries},
			func(ctx context.Context, rec embed.InvocationRecord) {
				invocationLogger(ctx, ai.InvocationRecord{
					WorkspaceID:      rec.WorkspaceID,
					Purpose:          rec.Purpose,
					Model:            rec.Model,
					PromptRedacted:   rec.PromptRedacted,
					ResponseRedacted: rec.ResponseRedacted,
					TokensInput:      rec.TokensInput,
					TokensOutput:     rec.TokensOutput,
					CostMicros:       rec.CostMicros,
					CostCents:        rec.CostCents,
					Status:           rec.Status,
					ErrorCode:        rec.ErrorCode,
				})
			},
			obs.RecordAIInvocation,
			ai.Redact,
		)
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
		// Share the orchestrator's cost guard + invocation logger with the
		// NL endpoints so command-palette / NL query / NL constraint calls
		// enforce the same per-workspace daily budget and write redacted
		// ai_invocations rows. Without this the NL surfaces bill the
		// provider unbounded and untracked (audit C-2 / H-8 / M-6).
		nlBudget := ai.BudgetReaderFunc(func(ctx context.Context, wsID uint32) (int64, error) {
			return deps.Queries.SumAiCostTodayForWorkspace(ctx, generated.SumAiCostTodayForWorkspaceParams{
				WorkspaceID: wsID,
				InvokedAt:   ai.WorkspaceDayStart(ctx, deps.Queries, wsID),
			})
		})
		nlGuard := ai.NewCostGuard(nlBudget, deps.AiDailyBudgetCents)
		nlLogger := invocationLogger
		if nlQueryCompiler == nil {
			prov := nlquery.NewWorkspaceProvider(wsResolver, extractWS).
				WithMetering(nlGuard, func(ctx context.Context, rec nlquery.InvocationRecord) {
					nlLogger(ctx, ai.InvocationRecord{
						WorkspaceID:      rec.WorkspaceID,
						Purpose:          rec.Purpose,
						Model:            rec.Model,
						PromptRedacted:   rec.PromptRedacted,
						ResponseRedacted: rec.ResponseRedacted,
						TokensInput:      rec.TokensInput,
						TokensOutput:     rec.TokensOutput,
						CostMicros:       rec.CostMicros,
						CostCents:        rec.CostCents,
						Status:           rec.Status,
						ErrorCode:        rec.ErrorCode,
					})
				}, obs.RecordAIInvocation)
			nlQueryCompiler = nlquery.New(prov)
		}
		if nlConstraintCompiler == nil {
			prov := nlconstraint.NewWorkspaceProvider(wsResolver, extractWS).
				WithMetering(nlGuard, func(ctx context.Context, rec nlconstraint.InvocationRecord) {
					nlLogger(ctx, ai.InvocationRecord{
						WorkspaceID:      rec.WorkspaceID,
						Purpose:          rec.Purpose,
						Model:            rec.Model,
						PromptRedacted:   rec.PromptRedacted,
						ResponseRedacted: rec.ResponseRedacted,
						TokensInput:      rec.TokensInput,
						TokensOutput:     rec.TokensOutput,
						CostMicros:       rec.CostMicros,
						CostCents:        rec.CostCents,
						Status:           rec.Status,
						ErrorCode:        rec.ErrorCode,
					})
				}, obs.RecordAIInvocation)
			nlConstraintCompiler = nlconstraint.New(prov)
		}
		if nlCommandResolver == nil {
			// The prompt catalogue is read off the MCP tool registry
			// rather than written out here. A second hand-maintained copy
			// drifts without failing: the model shapes arguments from the
			// copy while the executor validates against the registry, so a
			// command resolves "successfully" into arguments nothing can
			// use. nlcommand owns which tools are reachable; mcp owns what
			// each one takes.
			descs := mcp.DescribeTools(nlcommand.AllowedToolNames())
			tools := make([]nlcommand.ToolSpec, 0, len(descs))
			for _, d := range descs {
				tools = append(tools, nlcommand.ToolSpec{
					Name:        d.Name,
					Description: d.Description,
					InputSchema: d.InputSchema,
				})
			}
			cmdProv := nlcommand.NewWorkspaceProvider(wsResolver, extractWS).
				WithMetering(nlGuard, func(ctx context.Context, rec nlcommand.InvocationRecord) {
					nlLogger(ctx, ai.InvocationRecord{
						WorkspaceID:      rec.WorkspaceID,
						Purpose:          rec.Purpose,
						Model:            rec.Model,
						PromptRedacted:   rec.PromptRedacted,
						ResponseRedacted: rec.ResponseRedacted,
						TokensInput:      rec.TokensInput,
						TokensOutput:     rec.TokensOutput,
						CostMicros:       rec.CostMicros,
						CostCents:        rec.CostCents,
						Status:           rec.Status,
						ErrorCode:        rec.ErrorCode,
					})
				}, obs.RecordAIInvocation)
			nlCommandResolver = nlcommand.New(cmdProv, tools)
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
			Guard:         ai.NewCostGuard(budget, deps.AiDailyBudgetCents),
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
				InvokedAt:   ai.WorkspaceDayStart(ctx, deps.Queries, wsID),
			})
		})
		aiOrch = &ai.Orchestrator{
			Resolver:      resolver,
			Guard:         ai.NewCostGuard(budget, deps.AiDailyBudgetCents),
			DB:            deps.DB,
			Queries:       deps.Queries,
			LogInvoke:     invocationLogger,
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
			JudgeEnqueuer:      deps.JudgeEnqueuer,
		},
		calDeps: calendars.Deps{
			Queries:         deps.Queries,
			CalendarQueries: deps.CalendarQueries,
			DB:              deps.DB,
			Audit:           auditRec,
			EmailSender:     deps.EmailSender,
			EmailFrom:       deps.EmailFrom,
			FlowWebURL:      deps.FlowWebURL,
			Storage:         deps.Storage,
		},
	}
}

// newSubAPI constructs a fresh humachi.API on the given chi router.
// Each huma.API needs its own huma.Config so it gets a fresh schema
// registry and its own *OpenAPI document; sharing one config between
// sub-APIs would point every group at the same registry and panic on
// duplicate anonymous "ListOutputBody" style schema names.
func newSubAPI(sub chi.Router) huma.API {
	return humachi.New(sub, newAPIConfig(true))
}

// newPublicSubAPI is newSubAPI for a group that carries no auth
// middleware. Its operations are documented without a security
// requirement, because a bearer changes nothing about what they return.
func newPublicSubAPI(sub chi.Router) huma.API {
	return humachi.New(sub, newAPIConfig(false))
}

// bearerSchemeName is the OpenAPI key under which the API's JWT bearer
// is declared. Both services use the same name so the merged document
// has one scheme rather than two spellings of it.
const bearerSchemeName = "bearerAuth"

// newAPIConfig returns a fresh huma.Config declaring the API's bearer
// security scheme and, for a group that sits behind RequireAuth,
// stamping that requirement onto every operation registered on it.
//
// Without this the published document names no authentication mechanism
// at all: the bundled reference UI can call nothing that needs a token,
// and an SDK generated from the spec has no way to send one.
//
// The requirement rides on each operation rather than on the document
// because the public and authenticated surfaces share one document. A
// document-wide requirement would tell readers that the share and invite
// endpoints want a token they in fact ignore. Attaching it through the
// group's config instead means the spec follows the middleware: an
// operation is documented as authenticated exactly when it was
// registered on a group that authenticates.
func newAPIConfig(authenticated bool) huma.Config {
	cfg := huma.DefaultConfig("nodate-flow", "0.0.0")
	cfg.Components.SecuritySchemes = map[string]*huma.SecurityScheme{
		bearerSchemeName: {
			Type:         "http",
			Scheme:       "bearer",
			BearerFormat: "JWT",
			Description:  "Access token issued by auth-api. Obtain one with POST /auth/login and rotate it with POST /auth/refresh.",
		},
	}
	if !authenticated {
		return cfg
	}
	cfg.OnAddOperation = append(cfg.OnAddOperation,
		func(_ *huma.OpenAPI, op *huma.Operation) {
			if op.Security == nil {
				op.Security = []map[string][]string{{bearerSchemeName: {}}}
			}
		})
	return cfg
}

// mountGroup mounts the group's ACL floor middleware, creates its
// huma.API, and records the floor label alongside it in apis. Call it
// after the group's other Use() calls (auth, workspace / project
// resolution) and before registering any operation.
//
// The label recorded here is what the operation inventory reports, and an
// inventory is a ledger: it says which floor was chosen, not that the
// choice is enforced. Removing the Use call below would leave every
// operation still reporting its floor. What rules that out is
// TestMountGroupMountsTheFloorItRecords, which drives requests through
// the middleware this function leaves on the group.
func mountGroup(sub chi.Router, floor aclFloor, apis *[]groupAPI) huma.API {
	if floor.mw != nil {
		sub.Use(floor.mw)
	}
	api := newSubAPI(sub)
	*apis = append(*apis, groupAPI{api: api, floor: floor.label})
	return api
}

// buildAuthenticatedAPI registers every route that requires a valid
// bearer token. The supplied authMW is attached to each chi group so
// every handler runs through the auth resolver before reaching the
// huma operation. The returned slice contains one huma.API per chi
// group, each carrying the OpenAPI document that the static check
// tests (TestAuthenticatedSubRouterAlwaysAuthenticated) walk to assert
// 401 on every (method, path) when called without credentials.
func buildAuthenticatedAPI(r chi.Router, deps Deps, shared *sharedDeps, authMW func(http.Handler) http.Handler) []groupAPI {
	apis := make([]groupAPI, 0, 8)

	// /workspaces/{wsId} member-level surface: project list, labels,
	// lenses, timeboxes, pages, AI, etc. Reads are open to every workspace
	// member; the member floor keeps guests — the read-only workspace role
	// — out of every mutation registered on this group.
	r.Group(func(sub chi.Router) {
		sub.Use(authMW)
		sub.Use(middleware.RequireWorkspaceMember(shared.aclDB))
		subAPI := mountGroup(sub, floorWorkspaceMember, &apis)

		huma.Register(subAPI, huma.Operation{
			OperationID: "projects-list",
			Method:      http.MethodGet,
			Path:        "/workspaces/{wsId}/projects",
			Summary:     "List projects in a workspace",
			Description: "Returns every project the caller can see in the workspace. Backs the project switcher and the project list panel.",
			Tags:        []string{"Workspace"},
		}, projects.List(shared.prjDeps))
		labelDeps := labels.Deps{DB: deps.DB, Queries: deps.Queries, Audit: shared.auditRec}
		labels.RegisterWorkspaceScoped(subAPI, labelDeps)
		lensDeps := lenses.Deps{DB: deps.DB, Queries: deps.Queries, Audit: shared.auditRec}
		lenses.RegisterWorkspaceScoped(subAPI, lensDeps)
		exportDeps := exporthandlers.Deps{DB: deps.DB, Queries: deps.Queries, Audit: shared.auditRec}
		exporthandlers.RegisterWorkspaceScoped(subAPI, exportDeps)
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
			Description: "Returns the workspace's accumulated LLM spend for the current UTC day in USD. Used to power the AI cost gauge and to trip the configured budget guard.",
			Tags:        []string{"Public"},
		}, aihandlers.CostToday(shared.aiDeps))
		huma.Register(subAPI, huma.Operation{
			OperationID: "ai-metrics",
			Method:      http.MethodGet,
			Path:        "/workspaces/{wsId}/ai/metrics",
			Summary:     "AI suggestion acceptance metrics over a trailing window",
			Description: "Returns suggestion acceptance / dismissal counts and rate over a trailing window. Used by the AI Settings page to show whether the assistant is helpful.",
			Tags:        []string{"Public"},
		}, aihandlers.Metrics(shared.aiDeps))
		huma.Register(subAPI, huma.Operation{
			OperationID: "ai-agent-pause",
			Method:      http.MethodPost,
			Path:        "/workspaces/{wsId}/ai/agents/{agentId}/pause",
			Summary:     "Toggle the kill switch on an AI agent",
			Description: "Flips the agent's paused flag so the runtime stops scheduling it. In-flight runs continue to completion; subsequent triggers no-op until the agent is unpaused.",
			Tags:        []string{"Public"},
		}, aihandlers.PauseAgent(shared.aiDeps))
		huma.Register(subAPI, huma.Operation{
			OperationID: "ai-agents-list",
			Method:      http.MethodGet,
			Path:        "/workspaces/{wsId}/ai/agents",
			Summary:     "List AI agents for a workspace",
			Description: "Lists every AI agent registered in the workspace with status, schedule, and last-run summary. Backs the Agents panel.",
			Tags:        []string{"Public"},
		}, aihandlers.ListAgents(shared.aiDeps))
		huma.Register(subAPI, huma.Operation{
			OperationID: "ai-agents-create",
			Method:      http.MethodPost,
			Path:        "/workspaces/{wsId}/ai/agents",
			Summary:     "Create a new AI agent",
			Description: "Registers a new AI agent in the workspace with name, prompt, schedule, and event triggers. The agent starts paused so the operator can review configuration before unpausing.",
			Tags:        []string{"Public"},
		}, aihandlers.CreateAgent(shared.aiDeps))
		huma.Register(subAPI, huma.Operation{
			OperationID: "ai-agent-schedule-update",
			Method:      http.MethodPatch,
			Path:        "/workspaces/{wsId}/ai/agents/{agentId}/schedule",
			Summary:     "Update an AI agent's schedule_kind trigger mode",
			Description: "Updates the agent's schedule_kind (cron / on-event / manual) and any associated cadence settings. Takes effect on the next scheduler tick.",
			Tags:        []string{"Public"},
		}, aihandlers.UpdateAgentSchedule(shared.aiDeps))
		huma.Register(subAPI, huma.Operation{
			OperationID: "ai-agent-event-triggers-update",
			Method:      http.MethodPatch,
			Path:        "/workspaces/{wsId}/ai/agents/{agentId}/event-triggers",
			Summary:     "Replace an AI agent's event_trigger_types list",
			Description: "Replaces the set of event types that automatically wake the agent. Empty list disables event triggering entirely; cron and manual triggers continue to work.",
			Tags:        []string{"Public"},
		}, aihandlers.UpdateAgentEventTriggers(shared.aiDeps))
		huma.Register(subAPI, huma.Operation{
			OperationID: "ai-agent-trigger",
			Method:      http.MethodPost,
			Path:        "/workspaces/{wsId}/ai/agents/{agentId}/trigger",
			Summary:     "Manually trigger one run of an AI agent",
			Description: "Enqueues one run of the agent (or runs synchronously when no queue is configured). Returns AI.AGENT.RUNTIME_DISABLED when neither AgentQueue nor AgentRunner is wired.",
			Tags:        []string{"Public"},
		}, aihandlers.TriggerAgent(shared.aiDeps, deps.AgentQueue, deps.AgentRunner))
		huma.Register(subAPI, huma.Operation{
			OperationID: "ai-models-list",
			Method:      http.MethodGet,
			Path:        "/workspaces/{wsId}/ai/models",
			Summary:     "List workspace AI models across all providers",
			Description: "Returns every model exposed by the workspace's enabled providers (OpenAI, Anthropic, local, etc.) so model pickers can render a unified list.",
			Tags:        []string{"Public"},
		}, aihandlers.ListModels(shared.aiDeps))
		huma.Register(subAPI, huma.Operation{
			OperationID: "ai-priority-suggestions-list",
			Method:      http.MethodGet,
			Path:        "/workspaces/{wsId}/ai/priority-suggestions",
			Summary:     "Suggest priority adjustments for open tasks in a workspace",
			Description: "Runs the deterministic priority-suggestion engine and returns proposals (task id, current priority, suggested priority, reason). Read-only; the client applies via /tasks/{id}.",
			Tags:        []string{"Public"},
		}, aihandlers.ListPrioritySuggestions(shared.aiDeps))
		huma.Register(subAPI, huma.Operation{
			OperationID: "ai-compile-lens",
			Method:      http.MethodPost,
			Path:        "/workspaces/{wsId}/ai/compile-lens",
			Summary:     "Compile natural-language prose into a validated Lens JSON (ADR 0004)",
			Description: "Asks the workspace LLM to translate a natural-language description into a validated Lens JSON. Returns the JSON plus the model's confidence so the client can confirm before saving.",
			Tags:        []string{"Public"},
		}, aihandlers.CompileLens(shared.aiDeps))
		huma.Register(subAPI, huma.Operation{
			OperationID: "ai-state-suggestions",
			Method:      http.MethodGet,
			Path:        "/workspaces/{wsId}/ai/state-suggestions",
			Summary:     "Workspace-wide deterministic state inference proposals",
			Description: "Returns deterministic state-transition proposals across every open task in the workspace. Read-only; the client applies via /tasks/{id}/transitions.",
			Tags:        []string{"Public"},
		}, aihandlers.ListStateSuggestions(shared.aiDeps))
		huma.Register(subAPI, huma.Operation{
			OperationID: "ai-reminders",
			Method:      http.MethodGet,
			Path:        "/workspaces/{wsId}/ai/reminders",
			Summary:     "Workspace-wide deterministic reminder engine proposals",
			Description: "Returns reminders the deterministic engine surfaces for the workspace (due-soon, stale, blocked-by). Used by the Reminders dock.",
			Tags:        []string{"Public"},
		}, aihandlers.ListReminders(shared.aiDeps))
		huma.Register(subAPI, huma.Operation{
			OperationID: "ai-auto-actions",
			Method:      http.MethodGet,
			Path:        "/workspaces/{wsId}/ai/auto-actions",
			Summary:     "Workspace-wide deterministic auto-action proposals",
			Description: "Returns auto-action proposals (auto-archive, auto-close, auto-reassign) the engine surfaces. Read-only; execution happens via the auto-action executor when settings.enabled.",
			Tags:        []string{"Public"},
		}, aihandlers.ListAutoActions(shared.aiDeps))
		huma.Register(subAPI, huma.Operation{
			OperationID: "ai-weekly-digest",
			Method:      http.MethodGet,
			Path:        "/workspaces/{wsId}/ai/weekly-digest",
			Summary:     "Deterministic weekly digest markdown for a workspace",
			Description: "Renders a deterministic weekly digest (Markdown) summarizing what shipped, what stalled, and what's coming for the workspace. Used by the email digest job and the in-app digest panel.",
			Tags:        []string{"Public"},
		}, aihandlers.WeeklyDigest(shared.aiDeps))
		huma.Register(subAPI, huma.Operation{
			OperationID: "ai-invocations-list",
			Method:      http.MethodGet,
			Path:        "/workspaces/{wsId}/ai/invocations",
			Summary:     "List redacted LLM call audit rows for the AI reasoning panel",
			Description: "Returns a cursor-paginated page of redacted ai_invocations rows so the AI reasoning panel can show prompts, models, and costs without leaking secrets.",
			Tags:        []string{"Public"},
		}, aihandlers.ListInvocations(shared.aiDeps))
		huma.Register(subAPI, huma.Operation{
			OperationID: "ai-resolve-command",
			Method:      http.MethodPost,
			Path:        "/workspaces/{wsId}/ai/resolve-command",
			Summary:     "Resolve a natural-language command into an MCP tool invocation",
			Description: "Asks the LLM to translate a natural-language instruction into a structured MCP tool call. Returns the tool name and arguments; execution is the client's call so it can confirm first.",
			Tags:        []string{"Public"},
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
		// The stream outlives the request that opened it, so the gate
		// it passed on connect is re-run on a timer against the same
		// two middlewares this group is mounted behind. A token that
		// has been revoked or expired, or an account removed from the
		// workspace, closes the stream instead of keeping it fed.
		reauthorize := middleware.StreamReauthorizer(authMW, middleware.RequireWorkspaceMember(shared.aclDB))
		sub.Get("/workspaces/{wsId}/stream", stream.SSEHandler(notifier, deps.StreamRemember, reauthorize))
		relationDeps := relations.Deps{DB: deps.DB, Queries: deps.Queries, Audit: shared.auditRec}
		relations.RegisterWorkspaceScoped(subAPI, relationDeps)
		intakeDeps := intakehandlers.Deps{DB: deps.DB, Queries: deps.Queries, Audit: shared.auditRec}
		intakehandlers.Register(subAPI, intakeDeps)
		importDeps := importhandlers.Deps{DB: deps.DB, Queries: deps.Queries, Audit: shared.auditRec}
		importhandlers.Register(subAPI, importDeps)
	})

	// Workspace-scoped routes whose mutations only ever touch rows owned by
	// the caller: their own MCP tokens, their own notifications. Every
	// statement here is bound by user_id = actor, so there is no shared
	// state for a workspace role floor to protect — and applying one would
	// only stop a guest from managing their own notification bell or
	// minting a token that is, in turn, limited to their own role.
	r.Group(func(sub chi.Router) {
		sub.Use(authMW)
		sub.Use(middleware.RequireWorkspaceMember(shared.aclDB))
		subAPI := mountGroup(sub, floorNone, &apis)
		aihandlers.RegisterMcpTokens(subAPI, shared.aiDeps)
		notifications.RegisterWorkspaceScoped(subAPI, shared.notifDeps)
	})

	// Workspace admin routes: AI providers, project create, webhooks.
	r.Group(func(sub chi.Router) {
		sub.Use(authMW)
		sub.Use(middleware.RequireWorkspaceMember(shared.aclDB))
		subAPI := mountGroup(sub, floorWorkspaceAdmin, &apis)
		huma.Register(subAPI, huma.Operation{
			OperationID: "projects-create",
			Method:      http.MethodPost,
			Path:        "/workspaces/{wsId}/projects",
			Summary:     "Create a project in a workspace",
			Description: "Creates a new project in the workspace. The caller becomes the first project member with admin role. Requires workspace admin role.",
			Tags:        []string{"Workspace"},
		}, projects.Create(shared.prjDeps))
		aihandlers.RegisterProviders(subAPI, shared.aiDeps)
		aihandlers.RegisterAutoActionSettings(subAPI, shared.aiDeps)
		aihandlers.RegisterAutoActionRules(subAPI, shared.aiDeps)
		webhookDeps := webhooks.Deps{DB: deps.DB, Queries: deps.Queries, Audit: shared.auditRec}
		webhooks.Register(subAPI, webhookDeps)
		mappingDeps := integrationmappings.Deps{DB: deps.DB, Queries: deps.Queries, Audit: shared.auditRec}
		integrationmappings.Register(subAPI, mappingDeps)
		auditHandlerDeps := audithandlers.Deps{DB: deps.DB, Queries: deps.Queries}
		audithandlers.Register(subAPI, auditHandlerDeps)
	})

	// /projects/{prjId}.
	r.Group(func(sub chi.Router) {
		sub.Use(authMW)
		sub.Use(middleware.RequireProjectMemberByGlobalID(shared.aclDB))
		subAPI := mountGroup(sub, floorNone, &apis)
		projects.RegisterGlobal(subAPI, shared.prjDeps)
		timeline.RegisterProjectScoped(subAPI, shared.tlDeps)
	})

	// Task collection routes.
	r.Group(func(sub chi.Router) {
		sub.Use(authMW)
		subAPI := mountGroup(sub, floorNone, &apis)
		tasks.RegisterCollection(subAPI, shared.taskDeps)
	})

	// Task-scoped routes, split into three chi groups by the minimum
	// project role each operation requires. All three mount
	// RequireTaskAccess first (which resolves the task / project /
	// workspace context and enforces Layer-4 read visibility, injecting
	// the caller's project role into the context); the write groups then
	// chain RequireProjectRole so that a project viewer / commenter who can
	// *see* a task cannot mutate it.
	//
	//   - reads: RequireTaskAccess only. Any role that can see the task
	//     (down to viewer) may call these.
	//   - commenter writes: RequireProjectRole(commenter). Conversational
	//     mutations (comments, reactions) that the product allows
	//     commenters to perform.
	//   - editor writes: RequireProjectRole(editor). Structural mutations
	//     (patch / delete / transitions / constraints / dependencies /
	//     actors / agents / labels / attachments / archive / step
	//     decomposition). Workspace owners / admins pass via the
	//     ProjectRoleElevated bypass inside RequireProjectRole.
	labelTaskDeps := labels.Deps{DB: deps.DB, Queries: deps.Queries, Audit: shared.auditRec}
	relationTaskDeps := relations.Deps{DB: deps.DB, Queries: deps.Queries, Audit: shared.auditRec}
	reactionDeps := reactions.Deps{DB: deps.DB, Queries: deps.Queries, Audit: shared.auditRec}
	stepsDeps := tasks.StepsDeps{DB: deps.DB, Queries: deps.Queries, AI: shared.aiOrch, Embedder: shared.embedClient, Audit: shared.auditRec}

	// Read group: RequireTaskAccess only.
	r.Group(func(sub chi.Router) {
		sub.Use(authMW)
		sub.Use(middleware.RequireTaskAccess(shared.aclDB))
		subAPI := mountGroup(sub, floorNone, &apis)
		tasks.RegisterTaskScopedReads(subAPI, shared.taskDeps)
		labels.RegisterTaskScopedReads(subAPI, labelTaskDeps)
		timeline.RegisterTaskScoped(subAPI, shared.tlDeps)
		relations.RegisterTaskScoped(subAPI, relationTaskDeps)
		reactions.RegisterTaskScopedReads(subAPI, reactionDeps)
	})

	// Commenter write group: RequireTaskAccess + project commenter.
	r.Group(func(sub chi.Router) {
		sub.Use(authMW)
		sub.Use(middleware.RequireTaskAccess(shared.aclDB))
		subAPI := mountGroup(sub, floorProjectCommenter, &apis)
		tasks.RegisterTaskScopedCommenterWrites(subAPI, shared.taskDeps)
		reactions.RegisterTaskScopedWrites(subAPI, reactionDeps)
	})

	// Editor write group: RequireTaskAccess + project editor.
	r.Group(func(sub chi.Router) {
		sub.Use(authMW)
		sub.Use(middleware.RequireTaskAccess(shared.aclDB))
		subAPI := mountGroup(sub, floorProjectEditor, &apis)
		tasks.RegisterTaskScopedEditorWrites(subAPI, shared.taskDeps)
		labels.RegisterTaskScopedWrites(subAPI, labelTaskDeps)
		tasks.RegisterSteps(subAPI, stepsDeps)
	})

	// Workspace timeline + event reversal (ADR 0008 D4 / J5). Both share
	// the same RequireWorkspaceMember middleware because reversal is a
	// workspace-scoped mutation of an audit-log row that is otherwise
	// only visible through the timeline.
	r.Group(func(sub chi.Router) {
		sub.Use(authMW)
		sub.Use(middleware.RequireWorkspaceMember(shared.aclDB))
		subAPI := mountGroup(sub, floorWorkspaceMember, &apis)
		timeline.RegisterWorkspaceScoped(subAPI, shared.tlDeps)
		eventsDeps := events.Deps{DB: deps.DB, Queries: deps.Queries}
		events.RegisterWorkspaceScoped(subAPI, eventsDeps)
		activityhandlers.Register(subAPI, activityhandlers.Deps{DB: deps.DB, Queries: deps.Queries})
	})

	// Signals collection. POST /signals lives in its own chi group so
	// the service-token middleware (RequireSignalsAuth) can be scoped
	// to just this route — other authenticated endpoints below
	// continue to require a real user bearer. When
	// NF_FLOW_API_SIGNAL_TOKEN is empty the middleware is a passthrough
	// to the standard JWT chain, so the route still 401s on
	// unauthenticated requests (verified by
	// TestAuthenticatedSubRouterAlwaysAuthenticated).
	r.Group(func(sub chi.Router) {
		sub.Use(middleware.RequireSignalsAuth(authMW, deps.FlowAPISignalToken))
		subAPI := mountGroup(sub, floorNone, &apis)
		signals.RegisterCollection(subAPI, shared.signalDeps)
	})

	// Internal service-token-only endpoints (/internal/*). Mounted
	// under a chi group whose only auth middleware is
	// RequireServiceTokenOnly — JWT / PAT / MCP bearers are rejected
	// outright so these routes can never be reached with a user token,
	// and an empty NF_FLOW_API_SIGNAL_TOKEN disables the group entirely
	// (every request 401s). Current consumers: presence-discord
	// snowflake → flow user lookup.
	r.Group(func(sub chi.Router) {
		sub.Use(middleware.RequireServiceTokenOnly(deps.FlowAPISignalToken))
		subAPI := mountGroup(sub, floorNone, &apis)
		internalapi.Register(subAPI, internalapi.Deps{DB: deps.DB, Queries: deps.Queries})
	})

	// Inbox + notifications + favorites + relations (auth only;
	// handlers resolve ws membership themselves).
	r.Group(func(sub chi.Router) {
		sub.Use(authMW)
		subAPI := mountGroup(sub, floorNone, &apis)
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
		subAPI := mountGroup(sub, floorWorkspaceMember, &apis)
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
		subAPI := mountGroup(sub, floorNone, &apis)

		huma.Register(subAPI, huma.Operation{
			OperationID: "calendars-list",
			Method:      http.MethodGet,
			Path:        "/workspaces/{wsId}/calendars",
			Summary:     "List calendars in a workspace",
			Description: "Returns calendars the caller is subscribed to within the workspace plus their color and visibility flags. Backs the calendar sidebar.",
			Tags:        []string{"Calendar"},
		}, calendars.ListCalendars(shared.calDeps))
		huma.Register(subAPI, huma.Operation{
			OperationID: "calendars-create",
			Method:      http.MethodPost,
			Path:        "/workspaces/{wsId}/calendars",
			Summary:     "Create a calendar",
			Description: "Creates a new calendar in the workspace owned by the caller. Personal calendars default to discoverable=false; team calendars default to true.",
			Tags:        []string{"Calendar"},
		}, calendars.CreateCalendar(shared.calDeps))
		huma.Register(subAPI, huma.Operation{
			OperationID: "discoverable-calendars-list",
			Method:      http.MethodGet,
			Path:        "/workspaces/{wsId}/discoverable-calendars",
			Summary:     "List teammate personal calendars the caller can subscribe to",
			Description: "Lists teammate personal calendars marked discoverable so the caller can opt in. Excludes calendars the caller is already subscribed to.",
			Tags:        []string{"Public"},
		}, calendars.ListDiscoverableCalendars(shared.calDeps))
		huma.Register(subAPI, huma.Operation{
			OperationID: "calendars-self-subscribe",
			Method:      http.MethodPost,
			Path:        "/workspaces/{wsId}/calendars/{calId}/subscribe",
			Summary:     "Subscribe the caller to a calendar visible in the workspace",
			Description: "Subscribes the caller to the named calendar at viewer role. Returns the new subscription record with default color override.",
			Tags:        []string{"Calendar"},
		}, calendars.SelfSubscribe(shared.calDeps))
		huma.Register(subAPI, huma.Operation{
			OperationID: "calendar-events-list",
			Method:      http.MethodGet,
			Path:        "/workspaces/{wsId}/calendar-events",
			Summary:     "List events across all calendars in a workspace",
			Description: "Returns events from every calendar the caller can see in the workspace within the supplied date range. Used by the unified workspace calendar view.",
			Tags:        []string{"Public"},
		}, calendars.ListCalendarEvents(shared.calDeps))
		huma.Register(subAPI, huma.Operation{
			OperationID: "me-calendar-events-list",
			Method:      http.MethodGet,
			Path:        "/me/calendar-events",
			Summary:     "List events across every workspace the caller belongs to",
			Description: "Returns the caller's events across every workspace within a date range so the global My Calendar view can render without per-workspace round trips.",
			Tags:        []string{"Calendar"},
		}, calendars.ListMyCalendarEvents(shared.calDeps))
		huma.Register(subAPI, huma.Operation{
			OperationID: "me-invites-list",
			Method:      http.MethodGet,
			Path:        "/me/invites",
			Summary:     "List pending event invites addressed to the caller",
			Description: "Returns calendar event invites that have been sent to the caller's email and that the caller has not yet accepted or declined. Backs the /me/invites page.",
			Tags:        []string{"CalendarInvite"},
		}, calendars.ListMyInvites(shared.calDeps))

		// Public share admin endpoints (workspace-scoped).
		huma.Register(subAPI, huma.Operation{
			OperationID: "public-shares-list",
			Method:      http.MethodGet,
			Path:        "/workspaces/{wsId}/public-shares",
			Summary:     "List public share pages in a workspace",
			Description: "Lists public share pages owned by the workspace with metadata only (token is masked). Backs the share-management panel.",
			Tags:        []string{"Public"},
		}, calendars.ListPublicShares(shared.calDeps))
		huma.Register(subAPI, huma.Operation{
			OperationID: "public-shares-create",
			Method:      http.MethodPost,
			Path:        "/workspaces/{wsId}/public-shares",
			Summary:     "Create a public share page (returns plaintext token once)",
			Description: "Mints a new public share page with a fresh URL token. The token is returned plaintext exactly once so the operator can copy the share URL; subsequent reads only return its hash.",
			Tags:        []string{"Public"},
		}, calendars.CreatePublicShare(shared.calDeps))
		huma.Register(subAPI, huma.Operation{
			OperationID: "public-shares-get",
			Method:      http.MethodGet,
			Path:        "/workspaces/{wsId}/public-shares/{shareId}",
			Summary:     "Get a public share page with its published events",
			Description: "Returns the share page's metadata plus the ordered list of events currently published on it. The plaintext token is not returned.",
			Tags:        []string{"Public"},
		}, calendars.GetPublicShare(shared.calDeps))
		huma.Register(subAPI, huma.Operation{
			OperationID: "public-shares-patch",
			Method:      http.MethodPatch,
			Path:        "/workspaces/{wsId}/public-shares/{shareId}",
			Summary:     "Update public share page metadata",
			Description: "Updates the share page title, description, and theme. Token / event list use dedicated endpoints.",
			Tags:        []string{"Public"},
		}, calendars.PatchPublicShare(shared.calDeps))
		huma.Register(subAPI, huma.Operation{
			OperationID: "public-shares-rotate",
			Method:      http.MethodPost,
			Path:        "/workspaces/{wsId}/public-shares/{shareId}/rotate",
			Summary:     "Rotate the URL token for a public share page",
			Description: "Replaces the share's URL token with a fresh one. The previous token immediately stops resolving. The new plaintext token is returned exactly once.",
			Tags:        []string{"Public"},
		}, calendars.RotatePublicShareToken(shared.calDeps))
		huma.Register(subAPI, huma.Operation{
			OperationID: "public-shares-delete",
			Method:      http.MethodDelete,
			Path:        "/workspaces/{wsId}/public-shares/{shareId}",
			Summary:     "Delete a public share page (admin or owner only)",
			Description: "Removes the share page so its URL stops resolving. Permitted to the share owner or workspace admins. Idempotent.",
			Tags:        []string{"Public"},
		}, calendars.DeletePublicShare(shared.calDeps))
		huma.Register(subAPI, huma.Operation{
			OperationID: "public-shares-events-attach",
			Method:      http.MethodPost,
			Path:        "/workspaces/{wsId}/public-shares/{shareId}/events",
			Summary:     "Attach events to a public share page",
			Description: "Publishes one or more events on the share page. Order defaults to creation order; reorder via /events/reorder.",
			Tags:        []string{"Public"},
		}, calendars.AttachEventsToShare(shared.calDeps))
		huma.Register(subAPI, huma.Operation{
			OperationID: "public-shares-events-detach",
			Method:      http.MethodDelete,
			Path:        "/workspaces/{wsId}/public-shares/{shareId}/events/{evtId}",
			Summary:     "Detach an event from a public share page",
			Description: "Unpublishes the named event from the share page without deleting the event itself. Idempotent.",
			Tags:        []string{"Public"},
		}, calendars.DetachEventFromShare(shared.calDeps))
		huma.Register(subAPI, huma.Operation{
			OperationID: "public-shares-events-reorder",
			Method:      http.MethodPatch,
			Path:        "/workspaces/{wsId}/public-shares/{shareId}/events/reorder",
			Summary:     "Batch-reorder the events published on a public share page",
			Description: "Replaces the order of published events in a single request after a drag-and-drop. Atomic so no client sees a partial reorder.",
			Tags:        []string{"Public"},
		}, calendars.ReorderShareEvents(shared.calDeps))
	})

	// Calendar-scoped routes. Use RequireAuth + RequireCalendarMember,
	// so the {calId} URL param is resolved up-front and the actor's
	// subscription / membership is verified before the handler runs.
	r.Group(func(sub chi.Router) {
		sub.Use(authMW)
		sub.Use(calMW)
		subAPI := mountGroup(sub, floorNone, &apis)

		// Single calendar CRUD.
		huma.Register(subAPI, huma.Operation{
			OperationID: "calendars-get",
			Method:      http.MethodGet,
			Path:        "/workspaces/{wsId}/calendars/{calId}",
			Summary:     "Get a calendar",
			Description: "Returns the calendar's metadata (name, color, timezone, owner, sharing flags) for the calendar settings panel.",
			Tags:        []string{"Calendar"},
		}, calendars.GetCalendar(shared.calDeps))
		huma.Register(subAPI, huma.Operation{
			OperationID: "calendars-patch",
			Method:      http.MethodPatch,
			Path:        "/workspaces/{wsId}/calendars/{calId}",
			Summary:     "Update a calendar",
			Description: "Updates editable calendar fields (name, description, color, timezone, discoverable). Requires calendar admin role.",
			Tags:        []string{"Calendar"},
		}, calendars.PatchCalendar(shared.calDeps))
		huma.Register(subAPI, huma.Operation{
			OperationID: "calendars-delete",
			Method:      http.MethodDelete,
			Path:        "/workspaces/{wsId}/calendars/{calId}",
			Summary:     "Delete a calendar",
			Description: "Soft-deletes the calendar. Subscriptions are revoked; event history stays queryable for audit. Requires calendar owner role.",
			Tags:        []string{"Calendar"},
		}, calendars.DeleteCalendar(shared.calDeps))

		// Events within a calendar.
		huma.Register(subAPI, huma.Operation{
			OperationID: "events-list",
			Method:      http.MethodGet,
			Path:        "/workspaces/{wsId}/calendars/{calId}/events",
			Summary:     "List events in a calendar",
			Description: "Returns events from the named calendar within the supplied date range. Recurring events are returned as a single master row carrying its recurrenceRule; the client expands concrete instances from that rule.",
			Tags:        []string{"Calendar"},
		}, calendars.ListEvents(shared.calDeps))
		huma.Register(subAPI, huma.Operation{
			OperationID: "events-create",
			Method:      http.MethodPost,
			Path:        "/workspaces/{wsId}/calendars/{calId}/events",
			Summary:     "Create an event",
			Description: "Creates an event in the calendar. Optionally accepts an attendee list which triggers RSVP requests. A recurrenceRule is validated for well-formedness (freq / interval / byDay / until / count bounds) and stored as-is; it is not expanded server-side — clients expand concrete instances from the stored rule.",
			Tags:        []string{"Calendar"},
		}, calendars.CreateEvent(shared.calDeps))
		huma.Register(subAPI, huma.Operation{
			OperationID: "events-get",
			Method:      http.MethodGet,
			Path:        "/workspaces/{wsId}/calendars/{calId}/events/{evtId}",
			Summary:     "Get an event",
			Description: "Returns one event with its full body, recurrence rule, attendees, and link metadata. Used by the event detail panel.",
			Tags:        []string{"Calendar"},
		}, calendars.GetEvent(shared.calDeps))
		huma.Register(subAPI, huma.Operation{
			OperationID: "events-patch",
			Method:      http.MethodPatch,
			Path:        "/workspaces/{wsId}/calendars/{calId}/events/{evtId}",
			Summary:     "Update an event",
			Description: "Updates editable event fields (title, time range, description, location, recurrence). Time changes trigger task-shift propagation through task_event_links.",
			Tags:        []string{"Calendar"},
		}, calendars.PatchEvent(shared.calDeps))
		huma.Register(subAPI, huma.Operation{
			OperationID: "events-delete",
			Method:      http.MethodDelete,
			Path:        "/workspaces/{wsId}/calendars/{calId}/events/{evtId}",
			Summary:     "Delete an event",
			Description: "Removes the event. Linked tasks remain but their task_event_link rows are tombstoned so propagation rules stop firing. Idempotent.",
			Tags:        []string{"Calendar"},
		}, calendars.DeleteEvent(shared.calDeps))
		huma.Register(subAPI, huma.Operation{
			OperationID: "events-smart-create",
			Method:      http.MethodPost,
			Path:        "/workspaces/{wsId}/calendars/{calId}/events/smart-create",
			Summary:     "Parse natural language text into an event proposal",
			Description: "Asks the workspace LLM to translate a free-text prompt (e.g. 'lunch with Sam tomorrow at noon') into a structured event proposal. Read-only; the client confirms before /create.",
			Tags:        []string{"Calendar"},
		}, calendars.SmartCreate(shared.calDeps))
		huma.Register(subAPI, huma.Operation{
			OperationID: "events-from-task",
			Method:      http.MethodPost,
			Path:        "/workspaces/{wsId}/calendars/{calId}/events/from-task",
			Summary:     "Create a calendar event from a task",
			Description: "Creates an event seeded from the supplied task (title, due_on, description) and links the two via task_event_links so propagation rules can keep them in sync.",
			Tags:        []string{"Calendar"},
		}, calendars.CreateEventFromTask(shared.calDeps))

		// Calendar members.
		huma.Register(subAPI, huma.Operation{
			OperationID: "calendars-self-subscription-patch",
			Method:      http.MethodPatch,
			Path:        "/workspaces/{wsId}/calendars/{calId}/subscription",
			Summary:     "Update the caller's own subscription preferences for a calendar",
			Description: "Updates the caller's own subscription preferences (color override, visibility, notification opt-out) without affecting other members.",
			Tags:        []string{"Calendar"},
		}, calendars.PatchOwnSubscription(shared.calDeps))
		huma.Register(subAPI, huma.Operation{
			OperationID: "members-add",
			Method:      http.MethodPost,
			Path:        "/workspaces/{wsId}/calendars/{calId}/members",
			Summary:     "Add a member to a calendar",
			Description: "Adds a workspace member to the calendar at the requested role (viewer / editor / admin). Calendar admin role required.",
			Tags:        []string{"Calendar"},
		}, calendars.AddMember(shared.calDeps))
		huma.Register(subAPI, huma.Operation{
			OperationID: "members-list",
			Method:      http.MethodGet,
			Path:        "/workspaces/{wsId}/calendars/{calId}/members",
			Summary:     "List members of a calendar",
			Description: "Lists every member of the calendar with their role and join time. Used by the calendar settings members panel.",
			Tags:        []string{"Calendar"},
		}, calendars.ListMembers(shared.calDeps))
		huma.Register(subAPI, huma.Operation{
			OperationID: "members-update-role",
			Method:      http.MethodPatch,
			Path:        "/workspaces/{wsId}/calendars/{calId}/members/{userId}",
			Summary:     "Update a member's role",
			Description: "Changes a calendar member's role. Refuses to demote the last admin so the calendar stays manageable.",
			Tags:        []string{"Calendar"},
		}, calendars.UpdateMemberRole(shared.calDeps))
		huma.Register(subAPI, huma.Operation{
			OperationID: "members-remove",
			Method:      http.MethodDelete,
			Path:        "/workspaces/{wsId}/calendars/{calId}/members/{userId}",
			Summary:     "Remove a member from a calendar",
			Description: "Removes the named member from the calendar. Their subscription rows are tombstoned. Refuses to remove the last admin.",
			Tags:        []string{"Calendar"},
		}, calendars.RemoveMember(shared.calDeps))

		// Event attendees.
		huma.Register(subAPI, huma.Operation{
			OperationID: "attendees-list",
			Method:      http.MethodGet,
			Path:        "/workspaces/{wsId}/calendars/{calId}/events/{evtId}/attendees",
			Summary:     "List attendees on an event",
			Description: "Returns the attendee list for the event with each attendee's RSVP state and edit permission.",
			Tags:        []string{"Calendar"},
		}, calendars.ListAttendees(shared.calDeps))
		huma.Register(subAPI, huma.Operation{
			OperationID: "attendees-add",
			Method:      http.MethodPost,
			Path:        "/workspaces/{wsId}/calendars/{calId}/events/{evtId}/attendees",
			Summary:     "Add attendees to an event",
			Description: "Adds one or more attendees (workspace members or external email addresses) to the event. External invitees receive a magic-link invite email.",
			Tags:        []string{"Calendar"},
		}, calendars.AddAttendees(shared.calDeps))
		huma.Register(subAPI, huma.Operation{
			OperationID: "attendees-remove",
			Method:      http.MethodDelete,
			Path:        "/workspaces/{wsId}/calendars/{calId}/events/{evtId}/attendees/{userId}",
			Summary:     "Remove an attendee from an event",
			Description: "Removes the named attendee from the event. Idempotent.",
			Tags:        []string{"Calendar"},
		}, calendars.RemoveAttendee(shared.calDeps))
		huma.Register(subAPI, huma.Operation{
			OperationID: "attendees-rsvp",
			Method:      http.MethodPatch,
			Path:        "/workspaces/{wsId}/calendars/{calId}/events/{evtId}/attendees/rsvp",
			Summary:     "Update own RSVP for an event",
			Description: "Updates the caller's own RSVP (yes / no / maybe). Other attendees' RSVPs are unaffected.",
			Tags:        []string{"Calendar"},
		}, calendars.UpdateRsvp(shared.calDeps))
		huma.Register(subAPI, huma.Operation{
			OperationID: "attendees-can-edit",
			Method:      http.MethodPatch,
			Path:        "/workspaces/{wsId}/calendars/{calId}/events/{evtId}/attendees/{userId}/can-edit",
			Summary:     "Toggle can_edit for an attendee",
			Description: "Grants or revokes the named attendee's permission to edit the event. The event owner can always edit; this gates other attendees.",
			Tags:        []string{"Calendar"},
		}, calendars.ToggleCanEdit(shared.calDeps))

		// Event invites (magic link).
		huma.Register(subAPI, huma.Operation{
			OperationID: "event-invites-create",
			Method:      http.MethodPost,
			Path:        "/workspaces/{wsId}/calendars/{calId}/events/{evtId}/attendees/{attendeeId}/invite",
			Summary:     "Create (or rotate) a magic-link invite for an attendee",
			Description: "Mints a fresh magic-link invite token for the attendee and emails it. Repeated calls rotate the token, invalidating any prior unused link.",
			Tags:        []string{"CalendarInvite"},
		}, calendars.CreateEventInvite(shared.calDeps))
		huma.Register(subAPI, huma.Operation{
			OperationID: "event-invites-list",
			Method:      http.MethodGet,
			Path:        "/workspaces/{wsId}/calendars/{calId}/events/{evtId}/invites",
			Summary:     "List active magic-link invites for an event",
			Description: "Lists outstanding magic-link invites for the event with attendee, expiry, and last-sent metadata. Tokens are masked.",
			Tags:        []string{"CalendarInvite"},
		}, calendars.ListEventInvites(shared.calDeps))
		huma.Register(subAPI, huma.Operation{
			OperationID: "event-invites-revoke",
			Method:      http.MethodDelete,
			Path:        "/workspaces/{wsId}/calendars/{calId}/events/{evtId}/invites/{inviteId}",
			Summary:     "Revoke a magic-link invite",
			Description: "Marks the invite token as revoked so future redemption attempts fail. Already-accepted RSVPs are unaffected.",
			Tags:        []string{"CalendarInvite"},
		}, calendars.RevokeEventInvite(shared.calDeps))

		// Event comments.
		huma.Register(subAPI, huma.Operation{
			OperationID: "comments-list",
			Method:      http.MethodGet,
			Path:        "/workspaces/{wsId}/calendars/{calId}/events/{evtId}/comments",
			Summary:     "List comments on an event",
			Description: "Returns the comment thread on the event in chronological order. Used by the event detail comment pane.",
			Tags:        []string{"Calendar"},
		}, calendars.ListComments(shared.calDeps))
		huma.Register(subAPI, huma.Operation{
			OperationID: "comments-create",
			Method:      http.MethodPost,
			Path:        "/workspaces/{wsId}/calendars/{calId}/events/{evtId}/comments",
			Summary:     "Add a comment to an event",
			Description: "Appends a comment from the caller to the event thread. Mentions in the body are routed through the notifications pipeline.",
			Tags:        []string{"Calendar"},
		}, calendars.CreateComment(shared.calDeps))
		huma.Register(subAPI, huma.Operation{
			OperationID: "comments-edit",
			Method:      http.MethodPatch,
			Path:        "/workspaces/{wsId}/calendars/{calId}/events/{evtId}/comments/{cId}",
			Summary:     "Edit a comment",
			Description: "Replaces the body of the named comment. Only the original author may edit; an edited_at timestamp is set.",
			Tags:        []string{"Calendar"},
		}, calendars.EditComment(shared.calDeps))
		huma.Register(subAPI, huma.Operation{
			OperationID: "comments-delete",
			Method:      http.MethodDelete,
			Path:        "/workspaces/{wsId}/calendars/{calId}/events/{evtId}/comments/{cId}",
			Summary:     "Delete a comment",
			Description: "Removes the named comment. Permitted to the comment author or any calendar admin.",
			Tags:        []string{"Calendar"},
		}, calendars.DeleteComment(shared.calDeps))

		// Event checklist.
		huma.Register(subAPI, huma.Operation{
			OperationID: "checklist-list",
			Method:      http.MethodGet,
			Path:        "/workspaces/{wsId}/calendars/{calId}/events/{evtId}/checklist",
			Summary:     "List checklist items for an event",
			Description: "Returns the checklist items attached to the event in display order, used by the event prep panel.",
			Tags:        []string{"Calendar"},
		}, calendars.ListChecklist(shared.calDeps))
		huma.Register(subAPI, huma.Operation{
			OperationID: "checklist-create",
			Method:      http.MethodPost,
			Path:        "/workspaces/{wsId}/calendars/{calId}/events/{evtId}/checklist",
			Summary:     "Add a checklist item to an event",
			Description: "Appends a checklist item to the event with optional assignee. Returns the persisted item including its display order.",
			Tags:        []string{"Calendar"},
		}, calendars.CreateChecklistItem(shared.calDeps))
		huma.Register(subAPI, huma.Operation{
			OperationID: "checklist-update",
			Method:      http.MethodPatch,
			Path:        "/workspaces/{wsId}/calendars/{calId}/events/{evtId}/checklist/{itemId}",
			Summary:     "Update a checklist item",
			Description: "Updates a checklist item's text, completion state, assignee, or display order.",
			Tags:        []string{"Calendar"},
		}, calendars.UpdateChecklistItem(shared.calDeps))
		huma.Register(subAPI, huma.Operation{
			OperationID: "checklist-delete",
			Method:      http.MethodDelete,
			Path:        "/workspaces/{wsId}/calendars/{calId}/events/{evtId}/checklist/{itemId}",
			Summary:     "Delete a checklist item",
			Description: "Removes the named checklist item. Idempotent.",
			Tags:        []string{"Calendar"},
		}, calendars.DeleteChecklistItem(shared.calDeps))

		// Calendar memos.
		huma.Register(subAPI, huma.Operation{
			OperationID: "memos-list",
			Method:      http.MethodGet,
			Path:        "/workspaces/{wsId}/calendars/{calId}/memos",
			Summary:     "List memos in a calendar",
			Description: "Returns memos pinned to the calendar (e.g. running notes for a recurring meeting). Used by the calendar memo sidebar.",
			Tags:        []string{"Calendar"},
		}, calendars.ListMemos(shared.calDeps))
		huma.Register(subAPI, huma.Operation{
			OperationID: "memos-create",
			Method:      http.MethodPost,
			Path:        "/workspaces/{wsId}/calendars/{calId}/memos",
			Summary:     "Create a memo",
			Description: "Creates a memo on the calendar with title and Markdown body. Returns the persisted memo including its assigned id.",
			Tags:        []string{"Calendar"},
		}, calendars.CreateMemo(shared.calDeps))
		huma.Register(subAPI, huma.Operation{
			OperationID: "memos-update",
			Method:      http.MethodPatch,
			Path:        "/workspaces/{wsId}/calendars/{calId}/memos/{memoId}",
			Summary:     "Update a memo",
			Description: "Updates the memo's title or Markdown body.",
			Tags:        []string{"Calendar"},
		}, calendars.UpdateMemo(shared.calDeps))
		huma.Register(subAPI, huma.Operation{
			OperationID: "memos-delete",
			Method:      http.MethodDelete,
			Path:        "/workspaces/{wsId}/calendars/{calId}/memos/{memoId}",
			Summary:     "Delete a memo",
			Description: "Removes the memo. Idempotent.",
			Tags:        []string{"Calendar"},
		}, calendars.DeleteMemo(shared.calDeps))

		// Event attachments.
		huma.Register(subAPI, huma.Operation{
			OperationID: "attachments-list",
			Method:      http.MethodGet,
			Path:        "/workspaces/{wsId}/calendars/{calId}/events/{evtId}/attachments",
			Summary:     "List attachments on an event",
			Description: "Returns metadata for files attached to the event (filename, size, MIME, uploader). Bytes are fetched separately via signed URLs.",
			Tags:        []string{"Calendar"},
		}, calendars.ListAttachments(shared.calDeps))
		huma.Register(subAPI, huma.Operation{
			OperationID: "attachments-presign",
			Method:      http.MethodPost,
			Path:        "/workspaces/{wsId}/calendars/{calId}/events/{evtId}/attachments/presign",
			Summary:     "Reserve an attachment row and (if needed) get a presigned PUT URL",
			Description: "Single entry point for adding an attachment to an event. The client supplies the file's SHA-256; the server runs content-addressed dedup and either bumps the ref count on an existing storage_objects row (deduplicated=true, no upload) or returns a presigned PUT URL the client streams the bytes to.",
			Tags:        []string{"Calendar"},
		}, calendars.PresignAttachment(shared.calDeps))
		huma.Register(subAPI, huma.Operation{
			OperationID: "attachments-download",
			Method:      http.MethodGet,
			Path:        "/workspaces/{wsId}/calendars/{calId}/events/{evtId}/attachments/{attId}/download",
			Summary:     "Get a presigned GET URL for downloading an attachment",
			Description: "Returns a short-lived presigned GET URL with Content-Disposition: attachment. Non-ASCII filenames are emitted in RFC 5987 form so they survive HTTP header transport.",
			Tags:        []string{"Calendar"},
		}, calendars.DownloadAttachment(shared.calDeps))
		huma.Register(subAPI, huma.Operation{
			OperationID: "attachments-confirm",
			Method:      http.MethodPost,
			Path:        "/workspaces/{wsId}/calendars/{calId}/events/{evtId}/attachments/{attId}/confirm",
			Summary:     "Confirm an uploaded attachment and enforce its real size",
			Description: "Called after the client finishes the presigned PUT. The server StatObjects the stored blob and rejects it (deleting the attachment row and, if now unreferenced, the blob) when the actual size exceeds the per-file ceiling — the presigned PUT binds only the SHA-256, not the length, so the client-declared byteSize cannot be trusted. Returns the object's true size on success.",
			Tags:        []string{"Calendar"},
		}, calendars.ConfirmAttachment(shared.calDeps))
		huma.Register(subAPI, huma.Operation{
			OperationID: "attachments-delete",
			Method:      http.MethodDelete,
			Path:        "/workspaces/{wsId}/calendars/{calId}/events/{evtId}/attachments/{attId}",
			Summary:     "Delete an attachment from an event",
			Description: "Marks the attachment as removed, decrements the storage_objects ref_count, and best-effort deletes the underlying blob if no references remain. Idempotent.",
			Tags:        []string{"Calendar"},
		}, calendars.DeleteAttachment(shared.calDeps))
	})

	return apis
}

// buildPublicShareAPI registers the unauthenticated public surface.
// These routes deliberately skip RequireAuth and instead rely on
// signature verification (webhooks), opaque share tokens (public
// lenses, /share/cal/*), or no authentication at all (/health).
//
// The surface is split by what an operation does, because reading a
// shared page and redeeming an invite want opposite limits: a shared
// link is meant to be opened by many people, while an invite is
// redeemed once. Sharing one budget between them forced the strict
// choice on both. See publicRenderRateLimiter and the accept group
// below.
//
// The public calendar share and event-invite endpoints
// (/share/cal/{token}, /invites/{token}/info, /invites/{token}/accept)
// were relocated here from the retired time-api binary per ADR 0007
// and are registered inside this builder.
// Budgets for the unauthenticated surface. Both windows are 15 minutes;
// what differs is how many requests fit in one and what a bucket counts.
const (
	publicRateLimitWindow = 15 * time.Minute
	// Redeeming an invite: a write, performed once per attendee.
	publicAcceptMaxRequests = 30
	// Opening a shared page: a read, per share per caller. Enough for a
	// page load plus reloads and a few tab restores.
	publicRenderMaxRequests = 60
)

// publicRenderRateLimiter limits the read-only public renders, keyed by
// the share token together with the caller's address.
//
// Keying on the address alone is what this replaces, and it did not
// survive contact with how these links are used. A public share exists
// to be handed to a group of people, and a group of people is very often
// one egress address: an office, a school, a mobile carrier's NAT. Under
// a per-address bucket the first few openings spent everyone's budget
// and the rest of the building got 429 on a page that was published
// precisely so they could read it — with no setting an operator could
// reach to fix it.
//
// Adding the token to the key separates them: colleagues opening
// different shared links no longer draw on one another's budget, and a
// single share is still bounded per caller. What the bucket cannot do is
// stand in for guessing protection, and it never could — share tokens
// are opaque and long, so no achievable request rate meaningfully
// improves the odds of hitting one. Volume from a single address is
// bounded by the global per-IP limiter, which stays in front of this.
//
// The token is read off the end of the path rather than from
// chi.URLParam: a group middleware runs before the mux matches a route,
// so the named parameters do not exist yet. Both routes in this group
// end in the token, and a request that reaches here with nothing after
// the prefix simply keys on the empty token, which still bounds it.
func publicRenderRateLimiter() func(http.Handler) http.Handler {
	limiter := ratelimit.New(ratelimit.Config{
		MaxRequests: publicRenderMaxRequests,
		Window:      publicRateLimitWindow,
	})
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
			ip := authn.ClientIPFromContext(req.Context())
			token := pathTail(req.URL.Path)
			if ip == "" && token == "" {
				next.ServeHTTP(w, req)
				return
			}
			res := limiter.Allow(token + "@" + ip)
			httputil.SetRateLimitHeaders(w, res)
			if !res.Allowed {
				w.Header().Set("Retry-After", ratelimit.FormatRetryAfter(res.RetryAfter))
				httputil.WriteJSONError(w, http.StatusTooManyRequests, apierr.CodeRateLimitExceeded, "429 Too Many Requests")
				return
			}
			next.ServeHTTP(w, req)
		})
	}
}

// pathTail returns the last segment of a URL path, ignoring a trailing
// slash. It is how publicRenderRateLimiter reads the share token before
// the mux has matched a route.
func pathTail(p string) string {
	p = strings.TrimSuffix(p, "/")
	if i := strings.LastIndexByte(p, '/'); i >= 0 {
		return p[i+1:]
	}
	return p
}

func buildPublicShareAPI(r chi.Router, deps Deps, shared *sharedDeps) []huma.API {
	apis := make([]huma.API, 0, 3)

	// /health is auth-free but deliberately exempt from the public
	// per-IP rate limiter: kubelet liveness/readiness probes hit it
	// every few seconds and would otherwise be 429'd. The global
	// per-IP limiter still applies; that one is sized for normal
	// request volume.
	r.Group(func(sub chi.Router) {
		subAPI := newPublicSubAPI(sub)
		apis = append(apis, subAPI)

		huma.Register(subAPI, huma.Operation{
			OperationID: "health",
			Method:      http.MethodGet,
			Path:        "/health",
			Summary:     "Health check",
			Description: "Liveness probe for orchestration. Always returns 200 with a static {\"status\":\"ok\"} body, no auth, no database access. Exempt from the public per-IP rate limiter so kubelet probes are not throttled.",
			Tags:        []string{"Public"},
		}, func(_ context.Context, _ *struct{}) (*healthOutput, error) {
			out := &healthOutput{}
			out.Body.Status = "ok"
			return out, nil
		})
	})

	// Read-only public renders: the shared calendar page and the public
	// lens. These are the operations a link is handed around for, so
	// their limiter is keyed per share rather than per address (see
	// publicRenderRateLimiter).
	r.Group(func(sub chi.Router) {
		if !deps.DisableRateLimit {
			sub.Use(publicRenderRateLimiter())
		}
		subAPI := newPublicSubAPI(sub)
		apis = append(apis, subAPI)

		publicLensDeps := lenses.Deps{DB: deps.DB, Queries: deps.Queries, Audit: shared.auditRec}
		lenses.RegisterPublic(subAPI, publicLensDeps)

		// Calendar public share render (relocated from time-api per
		// ADR 0007). Unauthenticated; the opaque token is the capability.
		huma.Register(subAPI, huma.Operation{
			OperationID: "public-shares-render",
			Method:      http.MethodGet,
			Path:        "/share/cal/{token}",
			Summary:     "Render a public calendar share by URL token",
			Description: "Returns the public share page contents (title, description, theme, ordered events) addressed by its opaque URL token. Public, rate-limited; no auth required.",
			Tags:        []string{"CalendarShare"},
		}, calendars.RenderPublicShare(shared.calDeps))
	})

	// Redeeming an invite is a write, and one an attendee performs once.
	// It keeps the tight per-IP budget: a wrong token here has a
	// consequence, and nothing legitimate needs to try repeatedly.
	r.Group(func(sub chi.Router) {
		if !deps.DisableRateLimit {
			acceptRL := middleware.NewIPRateLimiter(middleware.RateLimitConfig{
				MaxRequests: publicAcceptMaxRequests,
				Window:      publicRateLimitWindow,
			})
			sub.Use(acceptRL.Middleware())
		}
		subAPI := newPublicSubAPI(sub)
		apis = append(apis, subAPI)

		huma.Register(subAPI, huma.Operation{
			OperationID: "event-invites-accept",
			Method:      http.MethodPost,
			Path:        "/public/invites/accept",
			Summary:     "Accept a calendar event invite via magic-link token",
			Description: "Consumes a magic-link invite token: marks the attendee's RSVP as accepted and registers the resulting account context. Public, rate-limited; the opaque token is the only capability.",
			Tags:        []string{"CalendarInvite"},
		}, calendars.AcceptEventInvite(shared.calDeps))
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
func NewDBInvocationLogger(q *generated.Queries, publish func(context.Context, uint32)) ai.InvocationLogger {
	return newDBInvocationLogger(q, publish)
}

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
			agentID = sql.NullInt32{Int32: int32(rec.AgentID), Valid: true} //#nosec G115 -- agent id is agents.id (BIGINT UNSIGNED), fits int32 within realistic deployments
		}
		var response sql.NullString
		if rec.ResponseRedacted != "" {
			response = sql.NullString{String: rec.ResponseRedacted, Valid: true}
		}
		var tIn, tOut sql.NullInt32
		if rec.TokensInput > 0 {
			tIn = sql.NullInt32{Int32: int32(rec.TokensInput), Valid: true} //#nosec G115 -- LLM token counts cap well below int32 (~2B per call would exhaust any provider context window)
		}
		if rec.TokensOutput > 0 {
			tOut = sql.NullInt32{Int32: int32(rec.TokensOutput), Valid: true} //#nosec G115 -- LLM token counts cap well below int32
		}
		model := rec.Model
		var cost sql.NullString
		// The cost recorded is the cost the provider reported. It is not
		// re-derived from the model name here: every provider prices its
		// own call, applying the deliberately conservative rate to models
		// missing from the price table, and a provider that charges
		// nothing reports zero on purpose. Guessing again at this layer
		// could only overwrite that zero, and did — local model names are
		// absent from the price table, so free inference was logged, and
		// billed against the workspace's daily budget, at the table's
		// highest rate.
		costMicros := rec.CostMicros
		if costMicros == 0 && rec.CostCents > 0 {
			costMicros = rec.CostCents * 10_000
		}
		if costMicros > 0 {
			cost = sql.NullString{String: fmt.Sprintf("%d.%06d", costMicros/1_000_000, costMicros%1_000_000), Valid: true}
		}
		status := generated.AiInvocationsStatusOk
		switch rec.Status {
		case "error":
			status = generated.AiInvocationsStatusError
		case "blocked":
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
			Model:            model,
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

type embedBudgetGuard struct {
	Queries *generated.Queries
}

func (g *embedBudgetGuard) Check(ctx context.Context, workspaceID uint32) error {
	if g == nil || g.Queries == nil {
		return nil
	}
	budgetCents := int64(100)
	settings, err := g.Queries.GetAiSettings(ctx, workspaceID)
	if err == nil {
		budgetCents = int64(settings.EmbedBudgetCentsDay)
	} else if err != sql.ErrNoRows {
		return err
	}
	spent, err := g.Queries.SumEmbedCostTodayForWorkspace(ctx, generated.SumEmbedCostTodayForWorkspaceParams{
		WorkspaceID: workspaceID,
		InvokedAt:   time.Now().UTC().Truncate(24 * time.Hour),
	})
	if err != nil {
		return err
	}
	if spent >= budgetCents {
		return ai.ErrDailyBudgetExceeded
	}
	return nil
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
	openapiutil.PatchErrorModelSchema(merged)
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
	openapiutil.PatchErrorModelSchema(root)
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
