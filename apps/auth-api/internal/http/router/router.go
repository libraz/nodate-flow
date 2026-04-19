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
	"github.com/nodate-flow/nodate-flow/apps/auth-api/internal/auth/sessionstore"
	"github.com/nodate-flow/nodate-flow/apps/auth-api/internal/crypto"
	"github.com/nodate-flow/nodate-flow/apps/auth-api/internal/db/generated"
	authhandlers "github.com/nodate-flow/nodate-flow/apps/auth-api/internal/http/handlers/auth"
	"github.com/nodate-flow/nodate-flow/apps/auth-api/internal/http/middleware"
	"github.com/nodate-flow/nodate-flow/packages/go-shared/authn"
)

// Deps is the dependency bundle Build needs.
type Deps struct {
	DB               *sql.DB
	Queries          *generated.Queries
	JWT              *auth.JWTIssuer
	Sessions         sessionstore.Store
	Cipher           *crypto.Cipher
	CookieSecure     bool
	RegistrationOpen bool
	DisableRateLimit bool
}

// Build mounts every auth-api route onto a fresh chi router and returns
// it as an http.Handler.
func Build(deps Deps) http.Handler {
	r := chi.NewRouter()
	r.Use(middleware.ClientIP())
	r.Use(middleware.SecurityHeaders())

	newConfig := func() huma.Config {
		return huma.DefaultConfig("nodate-auth", "0.0.0")
	}
	api := humachi.New(r, newConfig())
	newSubAPI := func(sub chi.Router) huma.API {
		return humachi.New(sub, newConfig())
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
		sessionStore = sessionstore.NewMySQLStore(deps.DB, deps.Queries)
	}
	auditRec := audit.New(deps.Queries)
	authDeps := authhandlers.Deps{
		DB:               deps.DB,
		Queries:          deps.Queries,
		Sessions:         sessionStore,
		JWT:              deps.JWT,
		Cipher:           deps.Cipher,
		CookieSecure:     deps.CookieSecure,
		RegistrationOpen: deps.RegistrationOpen,
		Audit:            auditRec,
	}

	// Public auth endpoints (login / register) behind per-IP rate limiter.
	r.Group(func(sub chi.Router) {
		if !deps.DisableRateLimit {
			authRateLimiter := middleware.NewIPRateLimiter(middleware.RateLimitConfig{
				MaxRequests: 5,
				Window:      15 * time.Minute,
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
	})

	// Cookie-auth endpoints (refresh / logout / totp).
	r.Group(func(sub chi.Router) {
		if !deps.DisableRateLimit {
			cookieRateLimiter := middleware.NewIPRateLimiter(middleware.RateLimitConfig{
				MaxRequests: 30,
				Window:      15 * time.Minute,
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

	return r
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
