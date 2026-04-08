// Command api is the entry point for the nodate-flow HTTP API server.
package main

import (
	"context"
	"database/sql"
	"log/slog"
	"net/http"
	"os"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"github.com/danielgtaylor/huma/v2/adapters/humachi"
	"github.com/go-chi/chi/v5"

	"github.com/nodate-flow/nodate-flow/apps/api/internal/auth"
	"github.com/nodate-flow/nodate-flow/apps/api/internal/config"
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

// HealthOutput is the response body for the health endpoint.
type HealthOutput struct {
	Body struct {
		Status string `json:"status" example:"ok"`
	}
}

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	slog.SetDefault(logger)

	cfg, err := config.Load()
	if err != nil {
		logger.Error("config load failed", "err", err)
		os.Exit(1)
	}

	// DB handle is left nil at scaffold time; production wiring will set it.
	var db *sql.DB
	queries := generated.New(db)

	// Cipher is optional at scaffold time: if NF_SECRET_KEY is unset the
	// AI provider endpoints will return AI.PROVIDER.NOT_CONFIGURED for
	// any write call, but the rest of the api boots normally so MVP
	// flows that don't touch LLMs keep working.
	var cipher *crypto.Cipher
	if c, cerr := crypto.NewFromEnv(); cerr == nil {
		cipher = c
	} else {
		logger.Warn("ai cipher disabled", "err", cerr)
	}

	jwtIssuer, err := auth.NewJWTIssuer(nil, "nodate-flow", "api", 15*time.Minute)
	if err != nil {
		logger.Error("jwt issuer init failed", "err", err)
		os.Exit(1)
	}

	router := chi.NewRouter()
	humaConfig := huma.DefaultConfig("nodate-flow", "0.0.0")
	api := humachi.New(router, humaConfig)

	huma.Register(api, huma.Operation{
		OperationID: "health",
		Method:      http.MethodGet,
		Path:        "/health",
		Summary:     "Health check",
	}, func(_ context.Context, _ *struct{}) (*HealthOutput, error) {
		out := &HealthOutput{}
		out.Body.Status = "ok"
		return out, nil
	})

	deps := authhandlers.Deps{
		DB:      db,
		Queries: queries,
		JWT:     jwtIssuer,
	}
	registerPublicAuthRoutes(api, deps)

	authMW := middleware.RequireAuth(middleware.AuthDeps{
		JWT:     deps.JWT,
		Queries: deps.Queries,
		DB:      passthroughDB{deps.DB},
	})
	// Protected routes are registered on a sub-API created from a chi
	// sub-router that has the bearer auth middleware attached.
	aclDB := passthroughDB{deps.DB}
	wsDeps := workspaces.Deps{DB: deps.DB, Queries: deps.Queries}
	prjDeps := projects.Deps{DB: deps.DB, Queries: deps.Queries}

	// Auth + /me + workspace create/list (no wsId in URL).
	router.Group(func(r chi.Router) {
		r.Use(authMW)
		protectedAPI := humachi.New(r, humaConfig)
		registerProtectedAuthRoutes(protectedAPI, deps)
		// Workspace create/list have no {wsId} so no workspace ACL needed.
		huma.Register(protectedAPI, huma.Operation{
			OperationID: "workspaces-create",
			Method:      http.MethodPost,
			Path:        "/workspaces",
			Summary:     "Create a workspace",
		}, workspaces.Create(wsDeps))
		huma.Register(protectedAPI, huma.Operation{
			OperationID: "workspaces-list",
			Method:      http.MethodGet,
			Path:        "/workspaces",
			Summary:     "List workspaces visible to the caller",
		}, workspaces.List(wsDeps))
	})

	// Routes scoped to a workspace via {wsId}: ACL resolves the workspace.
	router.Group(func(r chi.Router) {
		r.Use(authMW)
		r.Use(middleware.RequireWorkspaceMember(aclDB))
		wsAPI := humachi.New(r, humaConfig)
		huma.Register(wsAPI, huma.Operation{
			OperationID: "workspaces-get",
			Method:      http.MethodGet,
			Path:        "/workspaces/{wsId}",
			Summary:     "Fetch a workspace",
		}, workspaces.Get(wsDeps))
		huma.Register(wsAPI, huma.Operation{
			OperationID: "workspaces-members-list",
			Method:      http.MethodGet,
			Path:        "/workspaces/{wsId}/members",
			Summary:     "List members of a workspace",
		}, workspaces.ListMembers(wsDeps))
		huma.Register(wsAPI, huma.Operation{
			OperationID: "projects-list",
			Method:      http.MethodGet,
			Path:        "/workspaces/{wsId}/projects",
			Summary:     "List projects in a workspace",
		}, projects.List(prjDeps))
	})

	// AI providers + per-user MCP tokens. Both are workspace-scoped.
	aiDeps := aihandlers.Deps{DB: deps.DB, Queries: deps.Queries, Cipher: cipher}
	router.Group(func(r chi.Router) {
		r.Use(authMW)
		r.Use(middleware.RequireWorkspaceMember(aclDB))
		mcpAPI := humachi.New(r, humaConfig)
		aihandlers.RegisterMcpTokens(mcpAPI, aiDeps)
	})

	// Workspace routes that additionally require admin/owner role.
	router.Group(func(r chi.Router) {
		r.Use(authMW)
		r.Use(middleware.RequireWorkspaceMember(aclDB))
		r.Use(middleware.RequireWorkspaceRole(middleware.WorkspaceRoleAdmin))
		wsAdminAPI := humachi.New(r, humaConfig)
		huma.Register(wsAdminAPI, huma.Operation{
			OperationID: "workspaces-patch",
			Method:      http.MethodPatch,
			Path:        "/workspaces/{wsId}",
			Summary:     "Patch a workspace",
		}, workspaces.Patch(wsDeps))
		huma.Register(wsAdminAPI, huma.Operation{
			OperationID: "workspaces-members-invite",
			Method:      http.MethodPost,
			Path:        "/workspaces/{wsId}/members",
			Summary:     "Invite a user to a workspace",
		}, workspaces.InviteMember(wsDeps))
		huma.Register(wsAdminAPI, huma.Operation{
			OperationID: "workspaces-members-update-role",
			Method:      http.MethodPatch,
			Path:        "/workspaces/{wsId}/members/{userId}",
			Summary:     "Change a member's role",
		}, workspaces.UpdateMemberRole(wsDeps))
		huma.Register(wsAdminAPI, huma.Operation{
			OperationID: "workspaces-members-remove",
			Method:      http.MethodDelete,
			Path:        "/workspaces/{wsId}/members/{userId}",
			Summary:     "Remove a member from a workspace",
		}, workspaces.RemoveMember(wsDeps))
		huma.Register(wsAdminAPI, huma.Operation{
			OperationID: "projects-create",
			Method:      http.MethodPost,
			Path:        "/workspaces/{wsId}/projects",
			Summary:     "Create a project in a workspace",
		}, projects.Create(prjDeps))
		aihandlers.RegisterProviders(wsAdminAPI, aiDeps)
	})

	// Owner-only workspace routes.
	router.Group(func(r chi.Router) {
		r.Use(authMW)
		r.Use(middleware.RequireWorkspaceMember(aclDB))
		r.Use(middleware.RequireWorkspaceRole(middleware.WorkspaceRoleOwner))
		wsOwnerAPI := humachi.New(r, humaConfig)
		huma.Register(wsOwnerAPI, huma.Operation{
			OperationID: "workspaces-disable",
			Method:      http.MethodDelete,
			Path:        "/workspaces/{wsId}",
			Summary:     "Soft-disable a workspace",
		}, workspaces.Disable(wsDeps))
	})

	// Routes scoped to a project via {prjId} alone (no {wsId} in URL).
	router.Group(func(r chi.Router) {
		r.Use(authMW)
		r.Use(middleware.RequireProjectMemberByGlobalId(aclDB))
		prjAPI := humachi.New(r, humaConfig)
		projects.RegisterGlobal(prjAPI, prjDeps)
	})

	// Tasks: collection routes (POST/GET /tasks) under bare auth — the
	// handlers resolve workspace / project membership themselves because
	// there is no path parameter to bind ACL middleware against.
	taskDeps := tasks.Deps{DB: deps.DB, Queries: deps.Queries}
	router.Group(func(r chi.Router) {
		r.Use(authMW)
		taskCollAPI := humachi.New(r, humaConfig)
		tasks.RegisterCollection(taskCollAPI, taskDeps)
	})

	// Tasks: per-task routes under /tasks/{id} (and nested resources).
	// RequireTaskAccess resolves workspace / project / task contexts.
	router.Group(func(r chi.Router) {
		r.Use(authMW)
		r.Use(middleware.RequireTaskAccess(aclDB))
		taskAPI := humachi.New(r, humaConfig)
		tasks.RegisterTaskScoped(taskAPI, taskDeps)
		timeline.RegisterTaskScoped(taskAPI, timeline.Deps{DB: deps.DB, Queries: deps.Queries})
	})

	// Workspace timeline: under RequireWorkspaceMember.
	router.Group(func(r chi.Router) {
		r.Use(authMW)
		r.Use(middleware.RequireWorkspaceMember(aclDB))
		wsTLAPI := humachi.New(r, humaConfig)
		timeline.RegisterWorkspaceScoped(wsTLAPI, timeline.Deps{DB: deps.DB, Queries: deps.Queries})
	})

	// Signals: manual injection + inbox under bare auth.
	signalDeps := signals.Deps{
		DB:                 deps.DB,
		Queries:            deps.Queries,
		GhWebhookSecret:    cfg.GhWebhookSecret,
		SlackSigningSecret: cfg.SlackSigningSecret,
		DefaultWorkspaceID: cfg.DefaultWorkspaceID,
	}
	router.Group(func(r chi.Router) {
		r.Use(authMW)
		signalsAPI := humachi.New(r, humaConfig)
		signals.RegisterCollection(signalsAPI, signalDeps)
		inbox.Register(signalsAPI, inbox.Deps{DB: deps.DB, Queries: deps.Queries})
	})

	// MCP Streamable HTTP endpoint. Authentication is performed inside
	// the handler because the MCP token carries its own scopes and
	// workspace binding that the chi auth middleware does not know about.
	router.Handle("/mcp", mcp.NewHandler(mcp.Deps{DB: deps.DB, Queries: deps.Queries}))

	// Webhooks are PUBLIC: they verify their own signature and must
	// not run inside the Huma pipeline (we need raw body access).
	router.Post("/webhooks/github", signals.HandleGithubWebhook(signalDeps))
	router.Post("/webhooks/slack", signals.HandleSlackWebhook(signalDeps))

	addr := ":" + cfg.Port
	logger.Info("listening", "addr", addr)
	if err := http.ListenAndServe(addr, router); err != nil { //nolint:gosec // dev scaffold
		logger.Error("server exited", "err", err)
		os.Exit(1)
	}
}

// registerPublicAuthRoutes wires the unauthenticated auth endpoints onto
// the root API.
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

// registerProtectedAuthRoutes wires endpoints that require a valid bearer
// token. It must be called against a sub-API whose underlying chi router has
// the RequireAuth middleware attached, otherwise the routes will run
// unauthenticated.
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

// passthroughDB adapts a *sql.DB to the middleware.ACLDB interface so the
// auth middleware can run a single direct query for the JWT path.
type passthroughDB struct{ db *sql.DB }

func (p passthroughDB) QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row {
	return p.db.QueryRowContext(ctx, query, args...)
}
