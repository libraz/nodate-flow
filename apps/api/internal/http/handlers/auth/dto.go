// Package auth contains Huma operation handlers for the authentication
// endpoints: register, login, refresh, logout, OIDC, and /me.
package auth

import (
	"database/sql"
	"net/http"

	"github.com/nodate-flow/nodate-flow/apps/api/internal/auth"
	"github.com/nodate-flow/nodate-flow/apps/api/internal/auth/sessionstore"
	"github.com/nodate-flow/nodate-flow/apps/api/internal/db/generated"
)

// Deps is the dependency bundle passed to each handler.
type Deps struct {
	DB       *sql.DB
	Queries  *generated.Queries
	Sessions sessionstore.Store
	JWT      *auth.JWTIssuer
	OIDC     *auth.OIDCClient
	// CookieSecure toggles the Secure flag on the refresh cookie. It
	// defaults to true in production; local http dev can disable it via
	// NF_COOKIE_SECURE=false.
	CookieSecure bool
}

// RegisterInput is the body for POST /auth/register.
type RegisterInput struct {
	Body struct {
		Email       string `json:"email" format:"email" maxLength:"254"`
		Password    string `json:"password" minLength:"8" maxLength:"256"`
		DisplayName string `json:"displayName" minLength:"1" maxLength:"100"`
		Locale      string `json:"locale,omitempty" maxLength:"10"`
	}
}

// AuthTokens is the tokens envelope returned by register/login/refresh.
// The refresh token is intentionally NOT part of this struct: it is
// delivered as an httpOnly Set-Cookie header instead.
type AuthTokens struct {
	AccessToken string `json:"accessToken"`
	ExpiresAt   int64  `json:"expiresAt" doc:"Access token expiry, unix seconds"`
	UserID      string `json:"userId" doc:"User public id (UUID v7)"`
}

// RegisterOutput is the response for POST /auth/register.
type RegisterOutput struct {
	SetCookie http.Cookie `header:"Set-Cookie"`
	Body      AuthTokens
}

// LoginInput is the body for POST /auth/login.
type LoginInput struct {
	Body struct {
		Email    string `json:"email" format:"email"`
		Password string `json:"password"`
	}
}

// LoginOutput is the response for POST /auth/login.
type LoginOutput struct {
	SetCookie http.Cookie `header:"Set-Cookie"`
	Body      AuthTokens
}

// RefreshInput is the request for POST /auth/refresh. The refresh token
// is read from the nf_rt httpOnly cookie; there is no request body.
type RefreshInput struct {
	RefreshCookie http.Cookie `cookie:"nf_rt"`
}

// RefreshOutput is the response for POST /auth/refresh.
type RefreshOutput struct {
	SetCookie http.Cookie `header:"Set-Cookie"`
	Body      AuthTokens
}

// LogoutInput is the request for POST /auth/logout. The refresh token is
// read from the nf_rt httpOnly cookie.
type LogoutInput struct {
	RefreshCookie http.Cookie `cookie:"nf_rt"`
}

// LogoutOutput is the response for POST /auth/logout.
type LogoutOutput struct {
	SetCookie http.Cookie `header:"Set-Cookie"`
	Body      struct {
		Ok bool `json:"ok"`
	}
}

// OIDCStartOutput is the response for GET /auth/oidc/google/start.
type OIDCStartOutput struct {
	Body struct {
		AuthorizationURL string `json:"authorizationUrl"`
		State            string `json:"state"`
		Nonce            string `json:"nonce"`
	}
}

// OIDCCallbackInput is the query for GET /auth/oidc/google/callback.
type OIDCCallbackInput struct {
	Code  string `query:"code"`
	State string `query:"state"`
}

// OIDCCallbackOutput is the response for OIDC callback.
type OIDCCallbackOutput struct {
	SetCookie http.Cookie `header:"Set-Cookie"`
	Body      AuthTokens
}

// MeBody is the public DTO for the authenticated user profile, shared
// by GET /me and PATCH /me.
type MeBody struct {
	ID              string  `json:"id"`
	Email           string  `json:"email"`
	DisplayName     string  `json:"displayName"`
	Locale          string  `json:"locale"`
	ThemePreference string  `json:"themePreference" enum:"aurora-light,aurora-dark,dotline-light,dotline-dark,system"`
	AvatarURL       *string `json:"avatarUrl,omitempty"`
}

// MeOutput is the response for GET /me.
type MeOutput struct {
	Body MeBody
}

// PatchMeInput is the body for PATCH /me. All fields are optional;
// only non-nil fields are applied.
type PatchMeInput struct {
	Body PatchMeInputBody
}

// PatchMeInputBody carries the optional fields for PATCH /me.
type PatchMeInputBody struct {
	DisplayName     *string `json:"displayName,omitempty" minLength:"1" maxLength:"100"`
	Locale          *string `json:"locale,omitempty" maxLength:"10"`
	ThemePreference *string `json:"themePreference,omitempty" enum:"aurora-light,aurora-dark,dotline-light,dotline-dark,system"`
	AvatarURL       *string `json:"avatarUrl,omitempty" maxLength:"1024"`
}

// PatchMeOutput is the response for PATCH /me. It returns the updated
// profile in the same shape as GET /me.
type PatchMeOutput struct {
	Body MeBody
}
