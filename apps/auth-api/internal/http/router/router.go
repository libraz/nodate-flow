// Package router assembles the auth-api HTTP router. All authentication,
// user profile, and session management endpoints live here.
package router

import (
	"context"
	"database/sql"
	"net/http"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"github.com/danielgtaylor/huma/v2/adapters/humachi"
	"github.com/go-chi/chi/v5"

	"github.com/nodate-flow/nodate-flow/apps/auth-api/internal/audit"
	"github.com/nodate-flow/nodate-flow/apps/auth-api/internal/auth"
	"github.com/nodate-flow/nodate-flow/apps/auth-api/internal/auth/sessadapter"
	"github.com/nodate-flow/nodate-flow/apps/auth-api/internal/db/generated"
	adminhandlers "github.com/nodate-flow/nodate-flow/apps/auth-api/internal/http/handlers/admin"
	authhandlers "github.com/nodate-flow/nodate-flow/apps/auth-api/internal/http/handlers/auth"
	inthandlers "github.com/nodate-flow/nodate-flow/apps/auth-api/internal/http/handlers/integrations"
	wshandlers "github.com/nodate-flow/nodate-flow/apps/auth-api/internal/http/handlers/workspace"
	"github.com/nodate-flow/nodate-flow/apps/auth-api/internal/http/middleware"
	integrationspkg "github.com/nodate-flow/nodate-flow/apps/auth-api/internal/integrations"
	"github.com/nodate-flow/nodate-flow/apps/auth-api/internal/storage"
	"github.com/nodate-flow/nodate-flow/packages/go-shared/authn"
	"github.com/nodate-flow/nodate-flow/packages/go-shared/crypto"
	"github.com/nodate-flow/nodate-flow/packages/go-shared/email"
	"github.com/nodate-flow/nodate-flow/packages/go-shared/sessionstore"
)

// Deps is the dependency bundle Build needs.
type Deps struct {
	DB                *sql.DB
	Queries           *generated.Queries
	JWT               *auth.JWTIssuer
	OIDC              *auth.OIDCClient
	OIDCGithub        *auth.GithubOAuthClient
	OIDCMicrosoft     *auth.MicrosoftOIDCClient
	Sessions          sessionstore.Store
	Cipher            *crypto.Cipher
	CookieSecure      bool
	RegistrationOpen  bool
	MinPasswordLength int
	DisableRateLimit  bool

	// Rate-limit tunables (from config).
	RateLimitGlobalMax        int
	RateLimitGlobalWindowSec  int
	RateLimitAuthMax          int
	RateLimitAuthWindowSec    int
	RateLimitSessionMax       int
	RateLimitSessionWindowSec int
	EmailSender               email.Sender
	FlowWebURL                string
	AccountsWebURL            string

	// Integrations is the personal-OAuth provider registry (GitHub /
	// Slack / Google Calendar). Nil in tests; the handlers degrade
	// gracefully by returning INTEGRATION.OAUTH.PROVIDER_NOT_CONFIGURED.
	Integrations *integrationspkg.Registry
	// PublicBaseURL is the origin used to build OAuth callback URLs
	// (e.g. https://auth.example.com + /oauth/callback/github).
	PublicBaseURL string
	// WebBaseURL is where the OAuth callback handler bounces the user
	// back to after writing the user_integrations row.
	WebBaseURL string
	// Storage is the S3-compatible object store client used by the
	// avatar upload/download handlers. Nil when NF_S3_ENDPOINT is
	// unset; handlers degrade to AUTH.AVATAR.STORAGE_UNAVAILABLE.
	Storage *storage.Client
}

// Result is what BuildResult returns: the composed chi router plus the
// list of huma.API instances that were registered against it. The
// dump-openapi command merges each API's OpenAPI document into a single
// spec for TypeScript SDK generation.
type Result struct {
	Handler http.Handler
	APIs    []huma.API
}

// Build mounts every auth-api route onto a fresh chi router and returns
// it as an http.Handler. It is a thin wrapper around BuildResult for
// callers that only need the handler.
func Build(deps Deps) http.Handler {
	return BuildResult(deps).Handler
}

// BuildResult mounts every auth-api route onto a fresh chi router and
// returns the handler together with the list of huma.API instances used.
func BuildResult(deps Deps) Result {
	r := chi.NewRouter()
	r.Use(middleware.ClientIP())
	r.Use(middleware.SecurityHeaders())
	// Global per-IP rate limiter: defence-in-depth against floods
	// from a single source. Auth-specific endpoints (login, register)
	// have their own stricter per-group limiters below. Disabled in
	// integration tests where many parallel tenants hit the same
	// loopback address.
	if !deps.DisableRateLimit {
		globalRL := middleware.NewIPRateLimiter(middleware.RateLimitConfig{
			MaxRequests: deps.RateLimitGlobalMax,
			Window:      time.Duration(deps.RateLimitGlobalWindowSec) * time.Second,
		})
		r.Use(globalRL.Middleware())
	}

	newConfig := func() huma.Config {
		return huma.DefaultConfig("nodate-auth", "0.0.0")
	}
	var apis []huma.API
	api := humachi.New(r, newConfig())
	apis = append(apis, api)
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
		sessionStore = sessadapter.NewMySQLStore(deps.DB, deps.Queries)
	}
	auditRec := audit.New(deps.Queries)
	authDeps := authhandlers.Deps{
		DB:                deps.DB,
		Queries:           deps.Queries,
		Sessions:          sessionStore,
		JWT:               deps.JWT,
		OIDC:              deps.OIDC,
		OIDCGithub:        deps.OIDCGithub,
		OIDCMicrosoft:     deps.OIDCMicrosoft,
		Cipher:            deps.Cipher,
		CookieSecure:      deps.CookieSecure,
		RegistrationOpen:  deps.RegistrationOpen,
		MinPasswordLength: deps.MinPasswordLength,
		Audit:             auditRec,
		EmailSender:       deps.EmailSender,
		AccountsWebURL:    deps.AccountsWebURL,
		Storage:           deps.Storage,
		PublicBaseURL:     deps.PublicBaseURL,
	}

	// Auth capabilities — public, no rate limit, cacheable.
	huma.Register(api, huma.Operation{
		OperationID: "auth-capabilities",
		Method:      http.MethodGet,
		Path:        "/auth/capabilities",
		Summary:     "List available authentication methods",
	}, authhandlers.Capabilities(authDeps))

	// Avatar proxy — public (see note above the health endpoint).
	huma.Register(api, huma.Operation{
		OperationID: "me-avatar-proxy",
		Method:      http.MethodGet,
		Path:        "/avatars/{userId}",
		Summary:     "Stream a user's avatar image",
	}, authhandlers.AvatarProxy(authDeps))

	// Public auth endpoints (login / register) behind per-IP rate limiter.
	r.Group(func(sub chi.Router) {
		if !deps.DisableRateLimit {
			authRateLimiter := middleware.NewIPRateLimiter(middleware.RateLimitConfig{
				MaxRequests: deps.RateLimitAuthMax,
				Window:      time.Duration(deps.RateLimitAuthWindowSec) * time.Second,
			})
			sub.Use(authRateLimiter.Middleware())
		}
		subAPI := newSubAPI(sub)
		huma.Register(subAPI, huma.Operation{
			OperationID: "auth-register",
			Method:      http.MethodPost,
			Path:        "/auth/register",
			Summary:     "Register a new local-password account",
		}, authhandlers.Register(authDeps))
		huma.Register(subAPI, huma.Operation{
			OperationID: "auth-login",
			Method:      http.MethodPost,
			Path:        "/auth/login",
			Summary:     "Log in with email and password",
		}, authhandlers.Login(authDeps))
		huma.Register(subAPI, huma.Operation{
			OperationID: "auth-oidc-google-start",
			Method:      http.MethodGet,
			Path:        "/auth/oidc/google/start",
			Summary:     "Start a Google OIDC login flow",
		}, authhandlers.OIDCGoogleStart(authDeps))
		huma.Register(subAPI, huma.Operation{
			OperationID: "auth-oidc-google-callback",
			Method:      http.MethodGet,
			Path:        "/auth/oidc/google/callback",
			Summary:     "Complete a Google OIDC login flow",
		}, authhandlers.OIDCGoogleCallback(authDeps))
		huma.Register(subAPI, huma.Operation{
			OperationID: "auth-oidc-github-start",
			Method:      http.MethodGet,
			Path:        "/auth/oidc/github/start",
			Summary:     "Start a GitHub OAuth login flow",
		}, authhandlers.OIDCGithubStart(authDeps))
		huma.Register(subAPI, huma.Operation{
			OperationID: "auth-oidc-github-callback",
			Method:      http.MethodGet,
			Path:        "/auth/oidc/github/callback",
			Summary:     "Complete a GitHub OAuth login flow",
		}, authhandlers.OIDCGithubCallback(authDeps))
		huma.Register(subAPI, huma.Operation{
			OperationID: "auth-oidc-microsoft-start",
			Method:      http.MethodGet,
			Path:        "/auth/oidc/microsoft/start",
			Summary:     "Start a Microsoft OIDC login flow",
		}, authhandlers.OIDCMicrosoftStart(authDeps))
		huma.Register(subAPI, huma.Operation{
			OperationID: "auth-oidc-microsoft-callback",
			Method:      http.MethodGet,
			Path:        "/auth/oidc/microsoft/callback",
			Summary:     "Complete a Microsoft OIDC login flow",
		}, authhandlers.OIDCMicrosoftCallback(authDeps))
		huma.Register(subAPI, huma.Operation{
			OperationID: "auth-magic-link-request",
			Method:      http.MethodPost,
			Path:        "/auth/magic-link/request",
			Summary:     "Request a passwordless magic link",
		}, authhandlers.MagicLinkRequest(authDeps))
		huma.Register(subAPI, huma.Operation{
			OperationID: "auth-magic-link-verify",
			Method:      http.MethodGet,
			Path:        "/auth/magic-link/verify",
			Summary:     "Verify a magic link token and issue session",
		}, authhandlers.MagicLinkVerify(authDeps))
	})

	// Cookie-auth endpoints (refresh / logout / totp).
	r.Group(func(sub chi.Router) {
		if !deps.DisableRateLimit {
			cookieRateLimiter := middleware.NewIPRateLimiter(middleware.RateLimitConfig{
				MaxRequests: deps.RateLimitSessionMax,
				Window:      time.Duration(deps.RateLimitSessionWindowSec) * time.Second,
			})
			sub.Use(cookieRateLimiter.Middleware())
		}
		subAPI := newSubAPI(sub)
		huma.Register(subAPI, huma.Operation{
			OperationID: "auth-refresh",
			Method:      http.MethodPost,
			Path:        "/auth/refresh",
			Summary:     "Rotate refresh token and issue a new access token",
		}, authhandlers.Refresh(authDeps))
		huma.Register(subAPI, huma.Operation{
			OperationID: "auth-logout",
			Method:      http.MethodPost,
			Path:        "/auth/logout",
			Summary:     "Revoke a session",
		}, authhandlers.Logout(authDeps))
		huma.Register(subAPI, huma.Operation{
			OperationID: "auth-login-totp",
			Method:      http.MethodPost,
			Path:        "/auth/login/totp",
			Summary:     "Complete a TOTP step-up login",
		}, authhandlers.LoginTotp(authDeps))
	})

	// Bearer-protected auth endpoints.
	jwtResolver := &authn.JWTResolver{JWT: deps.JWT.JWTIssuer, DB: passthroughDB{deps.DB}}
	authMW := authn.RequireAuth(jwtResolver)
	adminACL := middleware.RequireInstanceAdmin(passthroughDB{deps.DB})
	adminDeps := adminhandlers.Deps{
		DB:      deps.DB,
		Queries: deps.Queries,
		Audit:   auditRec,
	}

	r.Group(func(sub chi.Router) {
		sub.Use(authMW)
		subAPI := newSubAPI(sub)

		// /me profile
		huma.Register(subAPI, huma.Operation{
			OperationID: "me",
			Method:      http.MethodGet,
			Path:        "/me",
			Summary:     "Return the authenticated user's profile",
		}, authhandlers.Me(authDeps))
		huma.Register(subAPI, huma.Operation{
			OperationID: "me-patch",
			Method:      http.MethodPatch,
			Path:        "/me",
			Summary:     "Patch the authenticated user's profile",
		}, authhandlers.PatchMe(authDeps))

		// Avatar upload / delete. The public GET counterpart is
		// registered outside this group, next to /auth/capabilities.
		huma.Register(subAPI, huma.Operation{
			OperationID: "me-avatar-upload",
			Method:      http.MethodPost,
			Path:        "/me/avatar",
			Summary:     "Upload a new avatar image for the authenticated user",
		}, authhandlers.AvatarUpload(authDeps))
		huma.Register(subAPI, huma.Operation{
			OperationID: "me-avatar-delete",
			Method:      http.MethodDelete,
			Path:        "/me/avatar",
			Summary:     "Remove the authenticated user's avatar",
		}, authhandlers.AvatarDelete(authDeps))

		// Sessions
		huma.Register(subAPI, huma.Operation{
			OperationID: "me-sessions-list",
			Method:      http.MethodGet,
			Path:        "/me/sessions",
			Summary:     "List the authenticated user's active sessions",
		}, authhandlers.ListSessions(authDeps))
		huma.Register(subAPI, huma.Operation{
			OperationID: "me-sessions-revoke",
			Method:      http.MethodDelete,
			Path:        "/me/sessions/{sessionId}",
			Summary:     "Revoke a single session",
		}, authhandlers.RevokeOneSession(authDeps))
		huma.Register(subAPI, huma.Operation{
			OperationID: "me-sessions-revoke-others",
			Method:      http.MethodDelete,
			Path:        "/me/sessions",
			Summary:     "Revoke every session except the current one",
		}, authhandlers.RevokeAllOtherSessions(authDeps))

		// Password
		huma.Register(subAPI, huma.Operation{
			OperationID: "me-password-change",
			Method:      http.MethodPost,
			Path:        "/me/password",
			Summary:     "Change the authenticated user's password",
		}, authhandlers.ChangePassword(authDeps))

		// TOTP
		huma.Register(subAPI, huma.Operation{
			OperationID: "me-totp-status",
			Method:      http.MethodGet,
			Path:        "/me/totp",
			Summary:     "Return TOTP 2FA status",
		}, authhandlers.TotpStatus(authDeps))
		huma.Register(subAPI, huma.Operation{
			OperationID: "me-totp-enroll",
			Method:      http.MethodPost,
			Path:        "/me/totp/enroll",
			Summary:     "Begin TOTP 2FA enrollment",
		}, authhandlers.TotpEnroll(authDeps))
		huma.Register(subAPI, huma.Operation{
			OperationID: "me-totp-confirm",
			Method:      http.MethodPost,
			Path:        "/me/totp/confirm",
			Summary:     "Confirm TOTP 2FA enrollment",
		}, authhandlers.TotpConfirm(authDeps))
		huma.Register(subAPI, huma.Operation{
			OperationID: "me-totp-disable",
			Method:      http.MethodDelete,
			Path:        "/me/totp",
			Summary:     "Disable TOTP 2FA",
		}, authhandlers.TotpDisable(authDeps))
		huma.Register(subAPI, huma.Operation{
			OperationID: "me-totp-recovery-status",
			Method:      http.MethodGet,
			Path:        "/me/totp/recovery-codes",
			Summary:     "Return remaining recovery code count",
		}, authhandlers.TotpRecoveryCodesStatus(authDeps))
		huma.Register(subAPI, huma.Operation{
			OperationID: "me-totp-recovery-regenerate",
			Method:      http.MethodPost,
			Path:        "/me/totp/recovery-codes",
			Summary:     "Regenerate TOTP recovery codes",
		}, authhandlers.TotpRegenerateRecoveryCodes(authDeps))
	})

	// Admin setup endpoint (auth-only, no admin check — for bootstrap).
	r.Group(func(sub chi.Router) {
		sub.Use(authMW)
		subAPI := newSubAPI(sub)
		adminhandlers.RegisterSetup(subAPI, adminDeps)
	})

	// Admin endpoints (auth + instance admin required).
	r.Group(func(sub chi.Router) {
		sub.Use(authMW)
		sub.Use(adminACL)
		subAPI := newSubAPI(sub)
		adminhandlers.Register(subAPI, adminDeps)
	})

	// Workspace handler dependencies.
	wsDeps := wshandlers.Deps{
		DB:      deps.DB,
		Queries: deps.Queries,
		Audit:   auditRec,
	}
	inviteDeps := wshandlers.InviteDeps{
		Deps:        wsDeps,
		EmailSender: deps.EmailSender,
		WebURL:      deps.FlowWebURL,
	}
	wsACL := middleware.RequireWorkspaceMember(passthroughDB{deps.DB})

	// Workspace auth-only endpoints (create, list, accept invite).
	r.Group(func(sub chi.Router) {
		sub.Use(authMW)
		subAPI := newSubAPI(sub)
		huma.Register(subAPI, huma.Operation{
			OperationID: "workspaces-create",
			Method:      http.MethodPost,
			Path:        "/workspaces",
			Summary:     "Create a workspace",
		}, wshandlers.Create(wsDeps))
		huma.Register(subAPI, huma.Operation{
			OperationID: "workspaces-list",
			Method:      http.MethodGet,
			Path:        "/workspaces",
			Summary:     "List workspaces for the authenticated user",
		}, wshandlers.List(wsDeps))
		huma.Register(subAPI, huma.Operation{
			OperationID: "invites-accept",
			Method:      http.MethodPost,
			Path:        "/invites/{token}/accept",
			Summary:     "Accept a workspace invite",
		}, wshandlers.AcceptInvite(inviteDeps))
	})

	// Workspace member read endpoints.
	r.Group(func(sub chi.Router) {
		sub.Use(authMW)
		sub.Use(wsACL)
		subAPI := newSubAPI(sub)
		huma.Register(subAPI, huma.Operation{
			OperationID: "workspaces-get",
			Method:      http.MethodGet,
			Path:        "/workspaces/{wsId}",
			Summary:     "Get workspace details",
		}, wshandlers.Get(wsDeps))
		huma.Register(subAPI, huma.Operation{
			OperationID: "workspaces-members-list",
			Method:      http.MethodGet,
			Path:        "/workspaces/{wsId}/members",
			Summary:     "List workspace members",
		}, wshandlers.ListMembers(wsDeps))
		huma.Register(subAPI, huma.Operation{
			OperationID: "workspaces-users-list",
			Method:      http.MethodGet,
			Path:        "/workspaces/{wsId}/users",
			Summary:     "List workspace users (actor picker)",
		}, wshandlers.ListUsers(wsDeps))
	})

	// Workspace admin write endpoints.
	r.Group(func(sub chi.Router) {
		sub.Use(authMW)
		sub.Use(wsACL)
		sub.Use(middleware.RequireWorkspaceRole(middleware.WorkspaceRoleAdmin))
		subAPI := newSubAPI(sub)
		huma.Register(subAPI, huma.Operation{
			OperationID: "workspaces-patch",
			Method:      http.MethodPatch,
			Path:        "/workspaces/{wsId}",
			Summary:     "Update workspace details",
		}, wshandlers.Patch(wsDeps))
		huma.Register(subAPI, huma.Operation{
			OperationID: "workspaces-members-add",
			Method:      http.MethodPost,
			Path:        "/workspaces/{wsId}/members",
			Summary:     "Add a member to a workspace",
		}, wshandlers.InviteMember(wsDeps))
		huma.Register(subAPI, huma.Operation{
			OperationID: "workspaces-members-update-role",
			Method:      http.MethodPatch,
			Path:        "/workspaces/{wsId}/members/{userId}",
			Summary:     "Update a member's role",
		}, wshandlers.UpdateMemberRole(wsDeps))
		huma.Register(subAPI, huma.Operation{
			OperationID: "workspaces-members-remove",
			Method:      http.MethodDelete,
			Path:        "/workspaces/{wsId}/members/{userId}",
			Summary:     "Remove a member from a workspace",
		}, wshandlers.RemoveMember(wsDeps))
		huma.Register(subAPI, huma.Operation{
			OperationID: "workspaces-invites-create",
			Method:      http.MethodPost,
			Path:        "/workspaces/{wsId}/invites",
			Summary:     "Create an invite link",
		}, wshandlers.CreateInvite(inviteDeps))
		huma.Register(subAPI, huma.Operation{
			OperationID: "workspaces-invites-list",
			Method:      http.MethodGet,
			Path:        "/workspaces/{wsId}/invites",
			Summary:     "List invite links",
		}, wshandlers.ListInvites(inviteDeps))
		huma.Register(subAPI, huma.Operation{
			OperationID: "workspaces-invites-revoke",
			Method:      http.MethodDelete,
			Path:        "/workspaces/{wsId}/invites/{inviteId}",
			Summary:     "Revoke an invite link",
		}, wshandlers.RevokeInvite(inviteDeps))
	})

	// Workspace owner-only endpoints.
	r.Group(func(sub chi.Router) {
		sub.Use(authMW)
		sub.Use(wsACL)
		sub.Use(middleware.RequireWorkspaceRole(middleware.WorkspaceRoleOwner))
		subAPI := newSubAPI(sub)
		huma.Register(subAPI, huma.Operation{
			OperationID: "workspaces-disable",
			Method:      http.MethodDelete,
			Path:        "/workspaces/{wsId}",
			Summary:     "Disable a workspace",
		}, wshandlers.Disable(wsDeps))
	})

	// Public invite info (rate-limited, no auth).
	r.Group(func(sub chi.Router) {
		if !deps.DisableRateLimit {
			inviteRateLimiter := middleware.NewIPRateLimiter(middleware.RateLimitConfig{
				MaxRequests: deps.RateLimitSessionMax,
				Window:      time.Duration(deps.RateLimitSessionWindowSec) * time.Second,
			})
			sub.Use(inviteRateLimiter.Middleware())
		}
		subAPI := newSubAPI(sub)
		huma.Register(subAPI, huma.Operation{
			OperationID: "invites-info",
			Method:      http.MethodGet,
			Path:        "/invites/{token}/info",
			Summary:     "Preview invite details (public)",
		}, wshandlers.InviteInfo(inviteDeps))
	})

	// /me/integrations (personal OAuth connections).
	integrationsDeps := inthandlers.Deps{
		DB:            deps.DB,
		Queries:       deps.Queries,
		Cipher:        deps.Cipher,
		Registry:      deps.Integrations,
		PublicBaseURL: deps.PublicBaseURL,
		WebBaseURL:    deps.WebBaseURL,
	}

	// OAuth callback — unauthenticated (user arrives from provider).
	huma.Register(api, huma.Operation{
		OperationID: "oauth-integration-callback",
		Method:      http.MethodGet,
		Path:        "/oauth/callback/{provider}",
		Summary:     "Complete a personal OAuth integration flow",
	}, inthandlers.Callback(integrationsDeps))

	// /me/integrations (auth-protected).
	r.Group(func(sub chi.Router) {
		sub.Use(authMW)
		subAPI := newSubAPI(sub)
		huma.Register(subAPI, huma.Operation{
			OperationID: "me-integrations-list",
			Method:      http.MethodGet,
			Path:        "/me/integrations",
			Summary:     "List the authenticated user's personal OAuth integrations",
		}, inthandlers.List(integrationsDeps))
		huma.Register(subAPI, huma.Operation{
			OperationID: "me-integrations-connect",
			Method:      http.MethodPost,
			Path:        "/me/integrations/{provider}/connect",
			Summary:     "Start a personal OAuth connect flow",
		}, inthandlers.Connect(integrationsDeps))
		huma.Register(subAPI, huma.Operation{
			OperationID: "me-integrations-disconnect",
			Method:      http.MethodDelete,
			Path:        "/me/integrations/{id}",
			Summary:     "Disconnect a personal OAuth integration",
		}, inthandlers.Disconnect(integrationsDeps))
	})

	return Result{Handler: r, APIs: apis}
}

type healthOutput struct {
	Body struct {
		Status string `json:"status"`
	}
}

// passthroughDB adapts *sql.DB to authn.ResolverDB.
type passthroughDB struct{ db *sql.DB }

func (p passthroughDB) QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row {
	return p.db.QueryRowContext(ctx, query, args...)
}
