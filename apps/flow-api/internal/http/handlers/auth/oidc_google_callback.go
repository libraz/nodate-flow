package auth

import (
	"context"
	"database/sql"
	"errors"

	"github.com/nodate-flow/nodate-flow/apps/flow-api/internal/db/generated"
	"github.com/nodate-flow/nodate-flow/apps/flow-api/internal/db/types"
	apierrors "github.com/nodate-flow/nodate-flow/apps/flow-api/internal/errors"
	"github.com/nodate-flow/nodate-flow/apps/flow-api/internal/http/middleware"
)

// OIDCGoogleCallback handles GET /auth/oidc/google/callback. It exchanges
// the authorization code for an id_token, finds or creates an identity
// row, and issues fresh app tokens.
func OIDCGoogleCallback(deps Deps) func(context.Context, *OIDCCallbackInput) (*OIDCCallbackOutput, error) {
	return func(ctx context.Context, in *OIDCCallbackInput) (*OIDCCallbackOutput, error) {
		if deps.OIDC == nil {
			return nil, httpErr(apierrors.AuthOidcProviderUnreachable)
		}
		// Validate the state JWT to prevent CSRF. The signed state
		// embeds the nonce, so we use the extracted nonce for the
		// id_token verification below.
		nonce, err := deps.JWT.VerifyOIDCState(in.State)
		if err != nil {
			return nil, httpErr(apierrors.AuthOidcStateMismatch)
		}
		idTok, err := deps.OIDC.Exchange(ctx, in.Code, nonce)
		if err != nil {
			return nil, httpErr(apierrors.AuthOidcIdTokenInvalid)
		}
		var claims struct {
			Email       string `json:"email"`
			Sub         string `json:"sub"`
			Name        string `json:"name"`
			Locale      string `json:"locale"`
			Verified    bool   `json:"email_verified"`
		}
		if err := idTok.Claims(&claims); err != nil {
			return nil, httpErr(apierrors.AuthOidcIdTokenInvalid)
		}

		ident, err := deps.Queries.FindIdentityByProviderSubject(ctx, generated.FindIdentityByProviderSubjectParams{
			Provider: generated.IdentitiesProvider("google"),
			Subject:  claims.Sub,
		})
		var userID uint32
		var userPub types.PublicID
		switch {
		case err == nil:
			userID = ident.UserID
			pub, qerr := deps.Queries.FindUserPublicIdById(ctx, userID)
			if qerr != nil {
				return nil, httpErr(apierrors.InternalUnexpected)
			}
			userPub = pub
		case errors.Is(err, sql.ErrNoRows):
			// Auto-provision a new user.
			userPub = types.New()
			locale := claims.Locale
			if locale == "" {
				locale = "en"
			}
			uid, err := deps.Queries.RegisterUser(ctx, generated.RegisterUserParams{
				PublicID:        userPub,
				Email:           claims.Email,
				DisplayName:     claims.Name,
				Locale:          locale,
				ThemePreference: generated.UsersThemePreference("system"),
			})
			if err != nil {
				return nil, httpErr(apierrors.InternalUnexpected)
			}
			userID = uint32(uid)
			identPub := types.New()
			if _, err := deps.Queries.CreateIdentity(ctx, generated.CreateIdentityParams{
				PublicID: identPub,
				UserID:   userID,
				Provider: generated.IdentitiesProvider("google"),
				Subject:  claims.Sub,
			}); err != nil {
				return nil, httpErr(apierrors.InternalUnexpected)
			}
		default:
			return nil, httpErr(apierrors.InternalUnexpected)
		}

		tokens, refresh, err := issueTokens(ctx, deps, userID, userPub, in.UserAgent, middleware.ClientIPFromContext(ctx))
		if err != nil {
			return nil, err
		}
		return &OIDCCallbackOutput{
			SetCookie: newRefreshCookie(refresh, deps.CookieSecure),
			Body:      tokens,
		}, nil
	}
}
