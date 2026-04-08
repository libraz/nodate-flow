// Package auth contains Huma operation handlers for the authentication
// endpoints: register, login, refresh, logout, OIDC, and /me.
package auth

import (
	"database/sql"

	"github.com/nodate-flow/nodate-flow/apps/api/internal/auth"
	"github.com/nodate-flow/nodate-flow/apps/api/internal/db/generated"
)

// Deps is the dependency bundle passed to each handler.
type Deps struct {
	DB      *sql.DB
	Queries *generated.Queries
	JWT     *auth.JWTIssuer
	OIDC    *auth.OIDCClient
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
type AuthTokens struct {
	AccessToken  string `json:"accessToken"`
	RefreshToken string `json:"refreshToken"`
	ExpiresAt    int64  `json:"expiresAt" doc:"Access token expiry, unix seconds"`
	UserID       string `json:"userId" doc:"User public id (UUID v7)"`
}

// RegisterOutput is the response for POST /auth/register.
type RegisterOutput struct {
	Body AuthTokens
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
	Body AuthTokens
}

// RefreshInput is the body for POST /auth/refresh.
type RefreshInput struct {
	Body struct {
		RefreshToken string `json:"refreshToken"`
	}
}

// RefreshOutput is the response for POST /auth/refresh.
type RefreshOutput struct {
	Body AuthTokens
}

// LogoutInput is the body for POST /auth/logout.
type LogoutInput struct {
	Body struct {
		RefreshToken string `json:"refreshToken,omitempty"`
	}
}

// LogoutOutput is the response for POST /auth/logout.
type LogoutOutput struct {
	Body struct {
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
	Body AuthTokens
}

// MeOutput is the response for GET /me.
type MeOutput struct {
	Body struct {
		ID          string `json:"id"`
		Email       string `json:"email"`
		DisplayName string `json:"displayName"`
		Locale      string `json:"locale"`
	}
}
