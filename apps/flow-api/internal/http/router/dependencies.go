package router

import (
	"context"
	"database/sql"
	"time"

	"github.com/libraz/nodate-flow/apps/flow-api/internal/ai"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/ai/agentruntime"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/ai/embed"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/ai/nlcommand"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/ai/nlconstraint"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/ai/nlquery"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/ai/providers"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/audit"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/auth"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/bgloop"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/dbretry"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/generated"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/generated/calendar"
	aihandlers "github.com/libraz/nodate-flow/apps/flow-api/internal/http/handlers/ai"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/http/handlers/calendars"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/http/handlers/inbox"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/http/handlers/notifications"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/http/handlers/pages"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/http/handlers/projects"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/http/handlers/signals"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/http/handlers/tasks"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/http/handlers/timeline"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/http/middleware"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/mcp"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/mutationlog"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/obs"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/storage"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/stream"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/suggestionprivacy"
	"github.com/libraz/nodate-flow/packages/go-shared/crypto"
	"github.com/libraz/nodate-flow/packages/go-shared/email"
)

// Deps is the dependency bundle Build needs in order to wire every
// route in the API. All fields are required except Cipher and
// DefaultWorkspaceID.
type Deps struct {
	// AppName is the display name published in the OpenAPI title and the
	// reference page. Empty falls back to brand.DefaultAppName.
	AppName string
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
	// signal row lands. Nil disables judge dispatch
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
	// uploads / downloads. Nil in tests; the upload / download endpoints
	// return INTERNAL.UNEXPECTED.
	Storage *storage.Client
	// BlobSigner checks the signed URLs the blob relay is addressed by. It
	// must be the signer Storage mints with, or no URL would verify. Nil
	// leaves the relay unmounted.
	BlobSigner *storage.Signer

	// HookRunner is the runner the event hooks registered here submit
	// their work to, shared with the hooks cmd/api registers so one bound
	// and one shutdown drain cover them all. Nil in tests, where each
	// hook falls back to a private runner.
	HookRunner *bgloop.HookRunner

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

	// EmbedProvider is constructed by cmd/api after sealing the instance key.
	// The router receives only the provider, never the plaintext key.
	EmbedProvider embed.Provider
}

// sharedDeps groups the derived dependency objects that several builder
// functions need to share (auditor, ACL adapter, AI orchestrator, the
// per-feature Deps structs the handlers expect, etc.). It is built once
// per BuildResult call so the AI orchestrator and embed client are
// instantiated exactly once even though the router has multiple sub
// builders.
type sharedDeps struct {
	aclDB    passthroughDB
	auditRec *audit.Recorder
	// mutationRec records a workspace-visible change in both the event
	// log and the audit log, so a handler cannot write one without the
	// other. Feature packages that have moved onto it take this instead
	// of auditRec; the two coexist while the rest of the handlers still
	// hold a bare audit recorder.
	mutationRec          *mutationlog.Recorder
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
// newPageDeps builds the pages handlers' dependencies. Page generation
// runs on the shared AI orchestrator, the same one every other AI
// surface uses; with none configured the generator stays a nil interface
// so the handler answers AI.PROVIDER.NOT_CONFIGURED.
func newPageDeps(deps Deps, shared *sharedDeps) pages.Deps {
	d := pages.Deps{DB: deps.DB, Queries: deps.Queries, Mutations: shared.mutationRec}
	if shared.aiOrch != nil {
		d.Generator = shared.aiOrch
	}
	return d
}

func buildSharedDeps(deps Deps) *sharedDeps {
	auditRec := audit.New(deps.Queries)
	mutationRec := mutationlog.New(deps.DB, deps.Queries)
	aclDB := passthroughDB{deps.DB}
	invocationLogger := newDBInvocationLogger(deps.DB, deps.AiInvocationPublisher)

	// Write-time embedding client. Uses the OpenAI provider
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
	case deps.EmbedProvider != nil:
		embedClient = embed.New(deps.EmbedProvider, deps.Queries)
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
		// provider with no daily cap and leave no invocation row behind,
		// so spend is neither stopped nor attributable to a workspace.
		nlBudget := ai.BudgetReaderFunc(func(ctx context.Context, wsID uint32) (int64, error) {
			return deps.Queries.SumAiCostTodayForWorkspace(ctx, ai.DailyCostParams(ctx, deps.Queries, wsID))
		})
		nlGuard := ai.NewCostGuard(nlBudget, deps.AiDailyBudgetCents)
		nlLogger := invocationLogger
		if nlQueryCompiler == nil {
			prov := nlquery.NewWorkspaceProvider(wsResolver, extractWS).
				WithMetering(nlGuard, nlLogger, obs.RecordAIInvocation)
			nlQueryCompiler = nlquery.New(prov)
		}
		if nlConstraintCompiler == nil {
			prov := nlconstraint.NewWorkspaceProvider(wsResolver, extractWS).
				WithMetering(nlGuard, nlLogger, obs.RecordAIInvocation)
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
				WithMetering(nlGuard, nlLogger, obs.RecordAIInvocation)
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
			Resolver:          mockResolver,
			Guard:             ai.NewCostGuard(budget, deps.AiDailyBudgetCents),
			DB:                dbretry.AutoCommit(deps.DB),
			Queries:           deps.Queries,
			OnInvocation:      obs.RecordAIInvocation,
			ProposalCache:     ai.NewProposalCache(10 * time.Minute),
			SuggestionPrivacy: &suggestionprivacy.Store{DB: deps.DB},
		}
	case deps.Cipher != nil:
		resolver := providers.NewWorkspaceResolver(deps.Queries, deps.Cipher)
		budget := ai.BudgetReaderFunc(func(ctx context.Context, wsID uint32) (int64, error) {
			return deps.Queries.SumAiCostTodayForWorkspace(ctx, ai.DailyCostParams(ctx, deps.Queries, wsID))
		})
		aiOrch = &ai.Orchestrator{
			Resolver:          resolver,
			Guard:             ai.NewCostGuard(budget, deps.AiDailyBudgetCents),
			DB:                dbretry.AutoCommit(deps.DB),
			Queries:           deps.Queries,
			LogInvoke:         invocationLogger,
			OnInvocation:      obs.RecordAIInvocation,
			ProposalCache:     ai.NewProposalCache(10 * time.Minute),
			SuggestionPrivacy: &suggestionprivacy.Store{DB: deps.DB},
		}
	}

	embedModel := ""
	embedEndpoint := ""
	if embedClient != nil {
		embedModel = embedClient.Model()
		if endpointProvider, ok := embedClient.Provider.(interface{ PublicEndpoint() string }); ok {
			embedEndpoint = endpointProvider.PublicEndpoint()
		}
	}

	return &sharedDeps{
		aclDB:                aclDB,
		auditRec:             auditRec,
		mutationRec:          mutationRec,
		embedClient:          embedClient,
		aiOrch:               aiOrch,
		nlQueryCompiler:      nlQueryCompiler,
		nlConstraintCompiler: nlConstraintCompiler,
		nlCommandResolver:    nlCommandResolver,
		prjDeps:              projects.Deps{DB: deps.DB, Queries: deps.Queries, Audit: auditRec},
		taskDeps:             tasks.Deps{DB: deps.DB, Queries: deps.Queries, CalendarQueries: deps.CalendarQueries, Embedder: embedClient, NlConstraint: nlConstraintCompiler, Storage: deps.Storage, Mutations: mutationRec},
		tlDeps:               timeline.Deps{DB: deps.DB, Queries: deps.Queries},
		inboxDeps:            inbox.Deps{DB: deps.DB, Queries: deps.Queries, Mutations: mutationRec},
		notifDeps:            notifications.Deps{DB: deps.DB, Queries: deps.Queries, Audit: auditRec},
		aiDeps:               aihandlers.Deps{DB: deps.DB, Queries: deps.Queries, Cipher: deps.Cipher, NlQuery: nlQueryCompiler, NlCommand: nlCommandResolver, Audit: auditRec, Mutations: mutationRec, EmbedConfigured: embedClient != nil, EmbedModel: embedModel, EmbedEndpoint: embedEndpoint},
		signalDeps: signals.Deps{
			DB:                 deps.DB,
			Queries:            deps.Queries,
			Mutations:          mutationRec,
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
			Mutations:       mutationRec,
			EmailSender:     deps.EmailSender,
			EmailFrom:       deps.EmailFrom,
			FlowWebURL:      deps.FlowWebURL,
			Storage:         deps.Storage,
			Embedder:        embedClient,
		},
	}
}

// passthroughDB adapts *sql.DB to middleware.ACLDB.
type passthroughDB struct{ db *sql.DB }

func (p passthroughDB) QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row {
	return p.db.QueryRowContext(ctx, query, args...)
}
