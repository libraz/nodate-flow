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
	"net/http"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"github.com/danielgtaylor/huma/v2/adapters/humachi"
	"github.com/go-chi/chi/v5"

	"github.com/nodate-flow/nodate-flow/apps/api/internal/ai"
	"github.com/nodate-flow/nodate-flow/apps/api/internal/ai/providers"
	"github.com/nodate-flow/nodate-flow/apps/api/internal/auth"
	"github.com/nodate-flow/nodate-flow/apps/api/internal/crypto"
	"github.com/nodate-flow/nodate-flow/apps/api/internal/db/generated"
	aihandlers "github.com/nodate-flow/nodate-flow/apps/api/internal/http/handlers/ai"
	authhandlers "github.com/nodate-flow/nodate-flow/apps/api/internal/http/handlers/auth"
	"github.com/nodate-flow/nodate-flow/apps/api/internal/http/handlers/inbox"
	"github.com/nodate-flow/nodate-flow/apps/api/internal/http/handlers/projects"
	"github.com/nodate-flow/nodate-flow/apps/api/internal/http/handlers/signals"
	"github.com/nodate-flow/nodate-flow/apps/api/internal/http/handlers/tasks"
	"github.com/nodate-flow/nodate-flow/apps/api/internal/http/handlers/timeline"
	"github.com/nodate-flow/nodate-flow/apps/api/internal/http/handlers/workspaces"
	"github.com/nodate-flow/nodate-flow/apps/api/internal/http/middleware"
	"github.com/nodate-flow/nodate-flow/apps/api/internal/mcp"
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
	// DefaultWorkspaceID is the workspace public id (UUID v7) that
	// webhook-origin signals are routed to. Empty in tests.
	DefaultWorkspaceID string
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

	authDeps := authhandlers.Deps{DB: deps.DB, Queries: deps.Queries, JWT: deps.JWT}
	registerPublicAuthRoutes(api, authDeps)

	authMW := middleware.RequireAuth(middleware.AuthDeps{
		JWT:     deps.JWT,
		Queries: deps.Queries,
		DB:      passthroughDB{deps.DB},
	})
	aclDB := passthroughDB{deps.DB}
	wsDeps := workspaces.Deps{DB: deps.DB, Queries: deps.Queries}
	prjDeps := projects.Deps{DB: deps.DB, Queries: deps.Queries}
	taskDeps := tasks.Deps{DB: deps.DB, Queries: deps.Queries}
	tlDeps := timeline.Deps{DB: deps.DB, Queries: deps.Queries}
	inboxDeps := inbox.Deps{DB: deps.DB, Queries: deps.Queries}
	aiDeps := aihandlers.Deps{DB: deps.DB, Queries: deps.Queries, Cipher: deps.Cipher}

	// /auth/refresh, /auth/logout, /me, /workspaces{,list}.
	r.Group(func(sub chi.Router) {
		sub.Use(authMW)
		subAPI := newSubAPI(sub)
		registerProtectedAuthRoutes(subAPI, authDeps)
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
			OperationID: "projects-list",
			Method:      http.MethodGet,
			Path:        "/workspaces/{wsId}/projects",
			Summary:     "List projects in a workspace",
		}, projects.List(prjDeps))
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
		GhWebhookSecret:    deps.GhWebhookSecret,
		SlackSigningSecret: deps.SlackSigningSecret,
		DefaultWorkspaceID: deps.DefaultWorkspaceID,
	}
	r.Group(func(sub chi.Router) {
		sub.Use(authMW)
		subAPI := newSubAPI(sub)
		signals.RegisterCollection(subAPI, signalDeps)
		inbox.Register(subAPI, inboxDeps)
	})

	// MCP orchestrator: only available when a cipher is configured.
	var aiOrch *ai.Orchestrator
	if deps.Cipher != nil {
		resolver := providers.NewWorkspaceResolver(deps.Queries, deps.Cipher)
		budget := ai.BudgetReaderFunc(func(ctx context.Context, wsID uint32) (int64, error) {
			return deps.Queries.SumAiCostTodayForWorkspace(ctx, generated.SumAiCostTodayForWorkspaceParams{
				WorkspaceID: wsID,
				InvokedAt:   time.Now().UTC().Truncate(24 * time.Hour),
			})
		})
		aiOrch = &ai.Orchestrator{
			Resolver: resolver,
			Guard:    ai.NewCostGuard(budget, 0),
		}
	}
	r.Handle("/mcp", mcp.NewHandler(mcp.Deps{DB: deps.DB, Queries: deps.Queries, AI: aiOrch}))

	// Public webhooks (verify their own signatures).
	r.Post("/webhooks/github", signals.HandleGithubWebhook(signalDeps))
	r.Post("/webhooks/slack", signals.HandleSlackWebhook(signalDeps))

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

// registerProtectedAuthRoutes wires the bearer-protected auth endpoints.
func registerProtectedAuthRoutes(api huma.API, deps authhandlers.Deps) {
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
		OperationID: "me",
		Method:      http.MethodGet,
		Path:        "/me",
		Summary:     "Return the authenticated user's profile",
	}, authhandlers.Me(deps))
}

// passthroughDB adapts *sql.DB to middleware.ACLDB.
type passthroughDB struct{ db *sql.DB }

func (p passthroughDB) QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row {
	return p.db.QueryRowContext(ctx, query, args...)
}

