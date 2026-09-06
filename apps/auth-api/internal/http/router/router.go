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

	"github.com/libraz/nodate-flow/apps/auth-api/internal/audit"
	"github.com/libraz/nodate-flow/apps/auth-api/internal/auth"
	"github.com/libraz/nodate-flow/apps/auth-api/internal/auth/sessadapter"
	"github.com/libraz/nodate-flow/apps/auth-api/internal/db/generated"
	"github.com/libraz/nodate-flow/apps/auth-api/internal/http/errormodel"
	adminhandlers "github.com/libraz/nodate-flow/apps/auth-api/internal/http/handlers/admin"
	authhandlers "github.com/libraz/nodate-flow/apps/auth-api/internal/http/handlers/auth"
	inthandlers "github.com/libraz/nodate-flow/apps/auth-api/internal/http/handlers/integrations"
	wshandlers "github.com/libraz/nodate-flow/apps/auth-api/internal/http/handlers/workspace"
	"github.com/libraz/nodate-flow/apps/auth-api/internal/http/middleware"
	integrationspkg "github.com/libraz/nodate-flow/apps/auth-api/internal/integrations"
	"github.com/libraz/nodate-flow/apps/auth-api/internal/storage"
	"github.com/libraz/nodate-flow/packages/go-shared/authn"
	"github.com/libraz/nodate-flow/packages/go-shared/crypto"
	"github.com/libraz/nodate-flow/packages/go-shared/email"
	"github.com/libraz/nodate-flow/packages/go-shared/sessionstore"
)

// Deps is the dependency bundle Build needs.
type Deps struct {
	DB                        *sql.DB
	Queries                   *generated.Queries
	JWT                       *auth.JWTIssuer
	OIDC                      *auth.OIDCClient
	OIDCGithub                *auth.GithubOAuthClient
	OIDCMicrosoft             *auth.MicrosoftOIDCClient
	MicrosoftAllowedTenantIDs []string
	Sessions                  sessionstore.Store
	// SingleUse records redeemed one-time token identifiers. Nil selects
	// the in-process default, which is correct for a single replica.
	SingleUse         authn.SingleUseStore
	Cipher            *crypto.Cipher
	CookieSecure      bool
	RegistrationOpen  bool
	MinPasswordLength int
	DisableRateLimit  bool
	TrustedProxyHops  int

	// OAuthAllowedDomains / OAuthAllowedEmails carry the environment half
	// of the opt-in OAuth/OIDC sign-in allowlist (normalized in
	// config.Load). The sign-in check unions them with the enabled
	// allowlist rows, so empty (both) means no restriction only while the
	// database names no entry either.
	OAuthAllowedDomains []string
	OAuthAllowedEmails  []string

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

// bearerSchemeName is the OpenAPI key under which the API's JWT bearer
// is declared. flow-api uses the same name so the merged document has
// one scheme rather than two spellings of it.
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
// because the sign-in endpoints and the account endpoints share one
// document, and the former must not be described as needing the token
// they exist to hand out. Attaching it through the group's config means
// the spec follows the middleware: an operation is documented as
// authenticated exactly when it was registered on a group that
// authenticates.
func newAPIConfig(authenticated bool) huma.Config {
	cfg := huma.DefaultConfig("nodate-auth", "0.0.0")
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

// BuildResult mounts every auth-api route onto a fresh chi router and
// returns the handler together with the list of huma.API instances used.
func BuildResult(deps Deps) Result {
	// Before any operation is registered: Huma's stock validation
	// envelope echoes the rejected value back to the caller, which for a
	// password, a one-time code or a TOTP secret means answering a
	// refusal with the credential. See internal/http/errormodel.
	errormodel.Install()

	r := chi.NewRouter()
	r.Use(middleware.ClientIP(deps.TrustedProxyHops))
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

	var apis []huma.API
	// The root API hosts only auth-free operations (health, capabilities,
	// the avatar proxy, the OAuth callback); everything needing a token
	// is registered on a group built with newAuthedSubAPI.
	api := humachi.New(r, newAPIConfig(false))
	apis = append(apis, api)
	newSubAPI := func(sub chi.Router) huma.API {
		a := humachi.New(sub, newAPIConfig(false))
		apis = append(apis, a)
		return a
	}
	newAuthedSubAPI := func(sub chi.Router) huma.API {
		a := humachi.New(sub, newAPIConfig(true))
		apis = append(apis, a)
		return a
	}

	// Health endpoint.
	huma.Register(api, huma.Operation{
		OperationID: "health",
		Method:      http.MethodGet,
		Path:        "/health",
		Summary:     "Health check",
		Description: "Liveness probe for orchestration. Always returns 200 with a static {\"status\":\"ok\"} body, no auth, no database access.",
	}, func(_ context.Context, _ *struct{}) (*healthOutput, error) {
		out := &healthOutput{}
		out.Body.Status = "ok"
		return out, nil
	})

	sessionStore := deps.Sessions
	if sessionStore == nil {
		sessionStore = sessadapter.NewMySQLStore(deps.DB, deps.Queries)
	}
	// One-time token identifiers (today the OIDC state jti) are recorded
	// here so a token cannot be redeemed twice inside its lifetime. The
	// in-process default is atomic within a replica; a deployment that
	// runs several auth-api replicas needs a shared implementation of
	// authn.SingleUseStore injected through Deps.
	singleUse := deps.SingleUse
	if singleUse == nil {
		singleUse = authn.NewMemorySingleUseStore()
	}
	auditRec := audit.New(deps.Queries)
	authDeps := authhandlers.Deps{
		SingleUse:                 singleUse,
		DB:                        deps.DB,
		Queries:                   deps.Queries,
		Sessions:                  sessionStore,
		JWT:                       deps.JWT,
		OIDC:                      deps.OIDC,
		OIDCGithub:                deps.OIDCGithub,
		OIDCMicrosoft:             deps.OIDCMicrosoft,
		MicrosoftAllowedTenantIDs: deps.MicrosoftAllowedTenantIDs,
		Cipher:                    deps.Cipher,
		CookieSecure:              deps.CookieSecure,
		RegistrationOpen:          deps.RegistrationOpen,
		OAuthAllowedDomains:       deps.OAuthAllowedDomains,
		OAuthAllowedEmails:        deps.OAuthAllowedEmails,
		MinPasswordLength:         deps.MinPasswordLength,
		Audit:                     auditRec,
		EmailSender:               deps.EmailSender,
		AccountsWebURL:            deps.AccountsWebURL,
		Storage:                   deps.Storage,
		PublicBaseURL:             deps.PublicBaseURL,
	}

	// Auth capabilities — public, no rate limit, cacheable.
	huma.Register(api, huma.Operation{
		OperationID: "auth-capabilities",
		Method:      http.MethodGet,
		Path:        "/auth/capabilities",
		Summary:     "List available authentication methods",
		Description: "Returns which login methods (password, OIDC providers, magic-link) the deployment has configured. Used by the login UI to render only enabled providers. Public, cacheable, no auth.",
	}, authhandlers.Capabilities(authDeps))

	// Avatar proxy — public (see note above the health endpoint).
	huma.Register(api, huma.Operation{
		OperationID: "me-avatar-proxy",
		Method:      http.MethodGet,
		Path:        "/avatars/{userId}",
		Summary:     "Stream a user's avatar image",
		Description: "Streams a user's avatar bytes from object storage with caching headers. Public so <img src> works without bearer tokens; the userId path is the only thing the caller needs to know.",
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
			Description: "Creates a new local user account from email + password. Refuses when registration is closed (RegistrationOpen=false) and validates against MinPasswordLength. On success issues an access token and sets the refresh cookie. Rate-limited.",
		}, authhandlers.Register(authDeps))
		huma.Register(subAPI, huma.Operation{
			OperationID: "auth-login",
			Method:      http.MethodPost,
			Path:        "/auth/login",
			Summary:     "Log in with email and password",
			Description: "Authenticates a local-password user. When TOTP is enabled returns a step-up challenge instead of a session; otherwise issues an access token and sets the refresh cookie. Rate-limited per IP.",
		}, authhandlers.Login(authDeps))
		huma.Register(subAPI, huma.Operation{
			OperationID: "auth-oidc-google-start",
			Method:      http.MethodGet,
			Path:        "/auth/oidc/google/start",
			Summary:     "Start a Google OIDC login flow",
			Description: "Generates a state nonce and 302-redirects the user to Google's OIDC authorization endpoint. Pairs with /auth/oidc/google/callback.",
		}, authhandlers.OIDCGoogleStart(authDeps))
		huma.Register(subAPI, huma.Operation{
			OperationID: "auth-oidc-google-callback",
			Method:      http.MethodGet,
			Path:        "/auth/oidc/google/callback",
			Summary:     "Complete a Google OIDC login flow",
			Description: "Receives the authorization code from Google, exchanges it for ID tokens, upserts the user, and bounces back to the web app with a session cookie. Validates the state nonce minted by /start.",
		}, authhandlers.OIDCGoogleCallback(authDeps))
		huma.Register(subAPI, huma.Operation{
			OperationID: "auth-oidc-github-start",
			Method:      http.MethodGet,
			Path:        "/auth/oidc/github/start",
			Summary:     "Start a GitHub OAuth login flow",
			Description: "Generates a state nonce and 302-redirects the user to GitHub's OAuth authorize endpoint. Pairs with /auth/oidc/github/callback.",
		}, authhandlers.OIDCGithubStart(authDeps))
		huma.Register(subAPI, huma.Operation{
			OperationID: "auth-oidc-github-callback",
			Method:      http.MethodGet,
			Path:        "/auth/oidc/github/callback",
			Summary:     "Complete a GitHub OAuth login flow",
			Description: "Receives the authorization code from GitHub, exchanges it for an access token, upserts the user from the verified primary email, and bounces back to the web app with a session cookie.",
		}, authhandlers.OIDCGithubCallback(authDeps))
		huma.Register(subAPI, huma.Operation{
			OperationID: "auth-oidc-microsoft-start",
			Method:      http.MethodGet,
			Path:        "/auth/oidc/microsoft/start",
			Summary:     "Start a Microsoft OIDC login flow",
			Description: "Generates a state nonce and 302-redirects the user to Microsoft's OIDC authorization endpoint. Pairs with /auth/oidc/microsoft/callback.",
		}, authhandlers.OIDCMicrosoftStart(authDeps))
		huma.Register(subAPI, huma.Operation{
			OperationID: "auth-oidc-microsoft-callback",
			Method:      http.MethodGet,
			Path:        "/auth/oidc/microsoft/callback",
			Summary:     "Complete a Microsoft OIDC login flow",
			Description: "Receives the authorization code from Microsoft, exchanges it for ID tokens, upserts the user, and bounces back to the web app with a session cookie. Validates the state nonce minted by /start.",
		}, authhandlers.OIDCMicrosoftCallback(authDeps))
		huma.Register(subAPI, huma.Operation{
			OperationID: "auth-magic-link-request",
			Method:      http.MethodPost,
			Path:        "/auth/magic-link/request",
			Summary:     "Request a passwordless magic link",
			Description: "Issues a single-use signed token by email so the recipient can log in without a password. Always returns 200 to avoid revealing whether the address is registered. Rate-limited per IP.",
		}, authhandlers.MagicLinkRequest(authDeps))
		huma.Register(subAPI, huma.Operation{
			OperationID: "auth-magic-link-verify",
			Method:      http.MethodGet,
			Path:        "/auth/magic-link/verify",
			Summary:     "Verify a magic link token and issue session",
			Description: "Consumes the one-time token from /auth/magic-link/request, marks it spent, and issues an access token plus refresh cookie. Tokens expire quickly and can be redeemed only once.",
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
			Description: "Reads the refresh cookie, rotates it (old one is invalidated), and returns a fresh access token. Used by the web client and CLI to silently extend sessions without re-prompting for credentials.",
		}, authhandlers.Refresh(authDeps))
		huma.Register(subAPI, huma.Operation{
			OperationID: "auth-logout",
			Method:      http.MethodPost,
			Path:        "/auth/logout",
			Summary:     "Revoke a session",
			Description: "Revokes the current session referenced by the refresh cookie and clears the cookie on the response. Idempotent: succeeds even if the cookie is missing or already invalid.",
		}, authhandlers.Logout(authDeps))
		huma.Register(subAPI, huma.Operation{
			OperationID: "auth-login-totp",
			Method:      http.MethodPost,
			Path:        "/auth/login/totp",
			Summary:     "Complete a TOTP step-up login",
			Description: "Second leg of the TOTP-protected login: takes the challenge id returned by /auth/login plus a 6-digit TOTP code, and on success issues an access token and refresh cookie. Recovery codes are accepted in place of TOTP codes.",
		}, authhandlers.LoginTotp(authDeps))
	})

	// Bearer-protected auth endpoints.
	jwtResolver := &authn.JWTResolver{JWT: deps.JWT.JWTIssuer, DB: passthroughDB{deps.DB}}
	rawAuthMW := authn.RequireAuth(jwtResolver)
	// Wrap RequireAuth with LoggerContext so every authenticated route
	// gets a request-scoped logger pre-populated with actor_id and
	// request_id once auth resolves. Workspace-scoped attrs are added
	// by RequireWorkspaceMember which calls enrichLoggerWithWorkspace.
	loggerCtx := middleware.LoggerContext()
	authMW := func(next http.Handler) http.Handler {
		return rawAuthMW(loggerCtx(next))
	}
	adminACL := middleware.RequireInstanceAdmin(deps.Queries)
	adminDeps := adminhandlers.Deps{
		DB:      deps.DB,
		Queries: deps.Queries,
		Audit:   auditRec,
		Storage: deps.Storage,
	}

	r.Group(func(sub chi.Router) {
		sub.Use(authMW)
		subAPI := newAuthedSubAPI(sub)

		// /me profile
		huma.Register(subAPI, huma.Operation{
			OperationID: "me",
			Method:      http.MethodGet,
			Path:        "/me",
			Summary:     "Return the authenticated user's profile",
			Description: "Returns the caller's profile (id, email, display name, locale, avatar URL, instance role) plus the list of workspaces they belong to. Used by both web apps to render the user shell.",
		}, authhandlers.Me(authDeps))
		huma.Register(subAPI, huma.Operation{
			OperationID: "me-patch",
			Method:      http.MethodPatch,
			Path:        "/me",
			Summary:     "Patch the authenticated user's profile",
			Description: "Updates the caller's editable profile fields (display name, locale, timezone). Email changes go through a separate verified flow and are not allowed here.",
		}, authhandlers.PatchMe(authDeps))

		// Avatar upload / delete. The public GET counterpart is
		// registered outside this group, next to /auth/capabilities.
		huma.Register(subAPI, huma.Operation{
			OperationID: "me-avatar-upload",
			Method:      http.MethodPost,
			Path:        "/me/avatar",
			Summary:     "Upload a new avatar image for the authenticated user",
			Description: "Accepts a multipart upload, validates the image type and size, stores it in object storage, and updates the caller's avatar reference. Returns AUTH.AVATAR.STORAGE_UNAVAILABLE when storage is not configured.",
		}, authhandlers.AvatarUpload(authDeps))
		huma.Register(subAPI, huma.Operation{
			OperationID: "me-avatar-delete",
			Method:      http.MethodDelete,
			Path:        "/me/avatar",
			Summary:     "Remove the authenticated user's avatar",
			Description: "Deletes the caller's stored avatar object and clears the avatar reference on the user row. Idempotent: returns 200 even if no avatar was set.",
		}, authhandlers.AvatarDelete(authDeps))

		// Sessions
		huma.Register(subAPI, huma.Operation{
			OperationID: "me-sessions-list",
			Method:      http.MethodGet,
			Path:        "/me/sessions",
			Summary:     "List the authenticated user's active sessions",
			Description: "Lists every active session belonging to the caller (device label, IP, user agent, last seen) so the security UI can show signed-in devices and offer revocation.",
		}, authhandlers.ListSessions(authDeps))
		huma.Register(subAPI, huma.Operation{
			OperationID: "me-sessions-revoke",
			Method:      http.MethodDelete,
			Path:        "/me/sessions/{sessionId}",
			Summary:     "Revoke a single session",
			Description: "Revokes the named session belonging to the caller. The next refresh attempt with that session will fail; access tokens already issued continue to work until they expire.",
		}, authhandlers.RevokeOneSession(authDeps))
		huma.Register(subAPI, huma.Operation{
			OperationID: "me-sessions-revoke-others",
			Method:      http.MethodDelete,
			Path:        "/me/sessions",
			Summary:     "Revoke every session except the current one",
			Description: "Bulk-revokes every session belonging to the caller except the one carrying the current refresh cookie. Used by 'sign out everywhere else' UI.",
		}, authhandlers.RevokeAllOtherSessions(authDeps))

		// Password
		huma.Register(subAPI, huma.Operation{
			OperationID: "me-password-change",
			Method:      http.MethodPost,
			Path:        "/me/password",
			Summary:     "Change the authenticated user's password",
			Description: "Requires the current password to match before storing a new bcrypt hash. The new password is validated against MinPasswordLength. Does not revoke other sessions automatically.",
		}, authhandlers.ChangePassword(authDeps))

		// TOTP
		huma.Register(subAPI, huma.Operation{
			OperationID: "me-totp-status",
			Method:      http.MethodGet,
			Path:        "/me/totp",
			Summary:     "Return TOTP 2FA status",
			Description: "Reports whether TOTP is enrolled and confirmed for the caller, plus the count of remaining recovery codes. Used by the security panel to show enrollment state.",
		}, authhandlers.TotpStatus(authDeps))
		huma.Register(subAPI, huma.Operation{
			OperationID: "me-totp-enroll",
			Method:      http.MethodPost,
			Path:        "/me/totp/enroll",
			Summary:     "Begin TOTP 2FA enrollment",
			Description: "After password reverification, generates a fresh TOTP secret and otpauth:// provisioning URL for the caller to scan. The secret is stored as pending until /me/totp/confirm validates a code.",
		}, authhandlers.TotpEnroll(authDeps))
		huma.Register(subAPI, huma.Operation{
			OperationID: "me-totp-confirm",
			Method:      http.MethodPost,
			Path:        "/me/totp/confirm",
			Summary:     "Confirm TOTP 2FA enrollment",
			Description: "After password reverification, validates a 6-digit code against the pending secret from /me/totp/enroll. On success activates TOTP for the caller and returns a fresh batch of recovery codes (shown once).",
		}, authhandlers.TotpConfirm(authDeps))
		huma.Register(subAPI, huma.Operation{
			OperationID: "me-totp-disable",
			Method:      http.MethodDelete,
			Path:        "/me/totp",
			Summary:     "Disable TOTP 2FA",
			Description: "Removes the TOTP secret and recovery codes for the caller. Requires the current password to authorize the change.",
		}, authhandlers.TotpDisable(authDeps))
		huma.Register(subAPI, huma.Operation{
			OperationID: "me-totp-recovery-status",
			Method:      http.MethodGet,
			Path:        "/me/totp/recovery-codes",
			Summary:     "Return remaining recovery code count",
			Description: "Reports how many unused recovery codes the caller has left. Does not return the codes themselves; those are only shown at generation time.",
		}, authhandlers.TotpRecoveryCodesStatus(authDeps))
		huma.Register(subAPI, huma.Operation{
			OperationID: "me-totp-recovery-regenerate",
			Method:      http.MethodPost,
			Path:        "/me/totp/recovery-codes",
			Summary:     "Regenerate TOTP recovery codes",
			Description: "Replaces the caller's recovery code batch with a fresh set, invalidating any unused old codes. Returns the new codes (shown once) and requires the current password.",
		}, authhandlers.TotpRegenerateRecoveryCodes(authDeps))
	})

	// Admin setup endpoint (auth-only, no admin check — for bootstrap).
	r.Group(func(sub chi.Router) {
		sub.Use(authMW)
		subAPI := newAuthedSubAPI(sub)
		adminhandlers.RegisterSetup(subAPI, adminDeps)
	})

	// Admin endpoints (auth + instance admin required).
	r.Group(func(sub chi.Router) {
		sub.Use(authMW)
		sub.Use(adminACL)
		subAPI := newAuthedSubAPI(sub)
		adminhandlers.Register(subAPI, adminDeps)
	})

	// Workspace handler dependencies.
	wsDeps := wshandlers.Deps{
		DB:      deps.DB,
		Queries: deps.Queries,
		Audit:   auditRec,
		Storage: deps.Storage,
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
		subAPI := newAuthedSubAPI(sub)
		huma.Register(subAPI, huma.Operation{
			OperationID: "workspaces-create",
			Method:      http.MethodPost,
			Path:        "/workspaces",
			Summary:     "Create a workspace",
			Description: "Creates a new workspace owned by the caller. The caller is added as the first owner-role member. Used during onboarding and from the workspace-switcher 'New workspace' action.",
		}, wshandlers.Create(wsDeps))
		huma.Register(subAPI, huma.Operation{
			OperationID: "workspaces-list",
			Method:      http.MethodGet,
			Path:        "/workspaces",
			Summary:     "List workspaces for the authenticated user",
			Description: "Lists every workspace the caller belongs to with their role. Used by the workspace switcher and during the post-login bootstrap to choose a default workspace.",
		}, wshandlers.List(wsDeps))
		huma.Register(subAPI, huma.Operation{
			OperationID: "invites-accept",
			Method:      http.MethodPost,
			Path:        "/invites/{token}/accept",
			Summary:     "Accept a workspace invite",
			Description: "Consumes a workspace invite token: adds the caller as a member with the role declared on the invite and marks the invite redeemed. The caller must already have an authenticated account.",
		}, wshandlers.AcceptInvite(inviteDeps))
	})

	// Workspace member read endpoints.
	r.Group(func(sub chi.Router) {
		sub.Use(authMW)
		sub.Use(wsACL)
		subAPI := newAuthedSubAPI(sub)
		huma.Register(subAPI, huma.Operation{
			OperationID: "workspaces-get",
			Method:      http.MethodGet,
			Path:        "/workspaces/{wsId}",
			Summary:     "Get workspace details",
			Description: "Returns the workspace metadata (name, slug, plan, settings) along with the caller's role in it. Requires workspace membership.",
		}, wshandlers.Get(wsDeps))
		huma.Register(subAPI, huma.Operation{
			OperationID: "workspaces-members-list",
			Method:      http.MethodGet,
			Path:        "/workspaces/{wsId}/members",
			Summary:     "List workspace members",
			Description: "Lists every active member of the workspace with their role and join time. Used by the admin members panel; requires workspace membership.",
		}, wshandlers.ListMembers(wsDeps))
		huma.Register(subAPI, huma.Operation{
			OperationID: "workspaces-users-list",
			Method:      http.MethodGet,
			Path:        "/workspaces/{wsId}/users",
			Summary:     "List workspace users (actor picker)",
			Description: "Lightweight listing of workspace members for actor / assignee pickers. Returns only id, display name, and avatar URL — no role or session info — and is the source of truth for in-product mention pickers.",
		}, wshandlers.ListUsers(wsDeps))
	})

	// Workspace admin write endpoints.
	r.Group(func(sub chi.Router) {
		sub.Use(authMW)
		sub.Use(wsACL)
		sub.Use(middleware.RequireWorkspaceRole(middleware.WorkspaceRoleAdmin))
		subAPI := newAuthedSubAPI(sub)
		huma.Register(subAPI, huma.Operation{
			OperationID: "workspaces-patch",
			Method:      http.MethodPatch,
			Path:        "/workspaces/{wsId}",
			Summary:     "Update workspace details",
			Description: "Updates editable workspace fields (name, slug, settings). Requires workspace admin role; owner-only fields go through dedicated endpoints.",
		}, wshandlers.Patch(wsDeps))
		huma.Register(subAPI, huma.Operation{
			OperationID: "workspaces-members-add",
			Method:      http.MethodPost,
			Path:        "/workspaces/{wsId}/members",
			Summary:     "Add a member to a workspace",
			Description: "Grants workspace membership at the requested role immediately. No email is sent and the recipient is never asked: the address is added as it was typed, and if it has no account yet a placeholder is created that whoever later signs in with that address adopts. Use POST /workspaces/{wsId}/invites instead when the recipient should have to accept. Requires workspace admin role.",
		}, wshandlers.AddMember(wsDeps))
		huma.Register(subAPI, huma.Operation{
			OperationID: "workspaces-members-update-role",
			Method:      http.MethodPatch,
			Path:        "/workspaces/{wsId}/members/{userId}",
			Summary:     "Update a member's role",
			Description: "Changes a member's role within the workspace. Promoting to owner or demoting the last owner is rejected to keep at least one owner present.",
		}, wshandlers.UpdateMemberRole(wsDeps))
		huma.Register(subAPI, huma.Operation{
			OperationID: "workspaces-members-remove",
			Method:      http.MethodDelete,
			Path:        "/workspaces/{wsId}/members/{userId}",
			Summary:     "Remove a member from a workspace",
			Description: "Removes the named user from the workspace. Removing the last owner is rejected. Outstanding sessions for the removed user keep working until their access tokens expire; refresh inside this workspace will fail.",
		}, wshandlers.RemoveMember(wsDeps))
		huma.Register(subAPI, huma.Operation{
			OperationID: "workspaces-invites-create",
			Method:      http.MethodPost,
			Path:        "/workspaces/{wsId}/invites",
			Summary:     "Create an invite link",
			Description: "Issues a shareable invite link with the requested role and expiry. The link is the only artifact returned that contains the token; subsequent listings return only metadata.",
		}, wshandlers.CreateInvite(inviteDeps))
		huma.Register(subAPI, huma.Operation{
			OperationID: "workspaces-invites-list",
			Method:      http.MethodGet,
			Path:        "/workspaces/{wsId}/invites",
			Summary:     "List invite links",
			Description: "Lists outstanding invite links for the workspace (id, role, expiry, redemption count) so admins can revoke or audit. Tokens are never returned.",
		}, wshandlers.ListInvites(inviteDeps))
		huma.Register(subAPI, huma.Operation{
			OperationID: "workspaces-invites-revoke",
			Method:      http.MethodDelete,
			Path:        "/workspaces/{wsId}/invites/{inviteId}",
			Summary:     "Revoke an invite link",
			Description: "Marks an invite token as revoked so future redemption attempts fail with INVITE.REVOKED. Already-redeemed memberships are unaffected.",
		}, wshandlers.RevokeInvite(inviteDeps))
	})

	// Workspace owner-only endpoints.
	r.Group(func(sub chi.Router) {
		sub.Use(authMW)
		sub.Use(wsACL)
		sub.Use(middleware.RequireWorkspaceRole(middleware.WorkspaceRoleOwner))
		subAPI := newAuthedSubAPI(sub)
		huma.Register(subAPI, huma.Operation{
			OperationID: "workspaces-delete",
			Method:      http.MethodDelete,
			Path:        "/workspaces/{wsId}",
			Summary:     "Delete a workspace immediately",
			Description: "Destructive immediate delete by the workspace owner. Sweeps every MinIO blob owned by the workspace, then issues a CASCADE-anchored hard DELETE on the workspaces row and every dependent member, project, task, event, and attachment. Requires `confirm: true` in the request body, returns 400 WORKSPACE.DELETE.CONFIRM_REQUIRED otherwise. Idempotent: an already-deleted workspace returns 200 with deleted=false. Suspension (PATCH with enabled=false) is a separate, reversible operation and is NOT a precondition.",
		}, wshandlers.Delete(wsDeps))
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
			Description: "Returns workspace name, inviter display name, and role for an invite token so the accept page can render context before the user signs in. Public, rate-limited; never reveals member emails or token-derived secrets.",
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
		Audit:         auditRec,
	}

	// OAuth callback — unauthenticated (user arrives from provider).
	huma.Register(api, huma.Operation{
		OperationID: "oauth-integration-callback",
		Method:      http.MethodGet,
		Path:        "/oauth/callback/{provider}",
		Summary:     "Complete a personal OAuth integration flow",
		Description: "Receives the provider's authorization code, exchanges it for tokens, encrypts and persists them on the user's user_integrations row, and 302-redirects back to WebBaseURL. Public because the user arrives via redirect from the third-party.",
	}, inthandlers.Callback(integrationsDeps))

	// /me/integrations (auth-protected).
	r.Group(func(sub chi.Router) {
		sub.Use(authMW)
		subAPI := newAuthedSubAPI(sub)
		huma.Register(subAPI, huma.Operation{
			OperationID: "me-integrations-list",
			Method:      http.MethodGet,
			Path:        "/me/integrations",
			Summary:     "List the authenticated user's personal OAuth integrations",
			Description: "Lists each personal OAuth integration the caller has connected (provider, account label, expiry). Encrypted tokens are never returned. Used by the integrations panel.",
		}, inthandlers.List(integrationsDeps))
		huma.Register(subAPI, huma.Operation{
			OperationID: "me-integrations-connect",
			Method:      http.MethodPost,
			Path:        "/me/integrations/{provider}/connect",
			Summary:     "Start a personal OAuth connect flow",
			Description: "Generates a state nonce and returns the provider's authorization URL the client should redirect to. The flow finishes at /oauth/callback/{provider}. Returns INTEGRATION.OAUTH.PROVIDER_NOT_CONFIGURED when the provider is not registered.",
		}, inthandlers.Connect(integrationsDeps))
		huma.Register(subAPI, huma.Operation{
			OperationID: "me-integrations-disconnect",
			Method:      http.MethodDelete,
			Path:        "/me/integrations/{id}",
			Summary:     "Disconnect a personal OAuth integration",
			Description: "Removes the named user_integrations row and best-effort revokes the provider token. Idempotent: returns 200 even if the integration was already removed.",
		}, inthandlers.Disconnect(integrationsDeps))
	})

	// Every operation is registered by now, so the schema registries are
	// populated and the write-only members can be read off them.
	errormodel.LearnWriteOnlyFields(apis)

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
