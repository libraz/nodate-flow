package auth

import (
	"context"
	"database/sql"
	"errors"

	"github.com/nodate-flow/nodate-flow/apps/auth-api/internal/audit"
	"github.com/nodate-flow/nodate-flow/apps/auth-api/internal/db/generated"
	"github.com/nodate-flow/nodate-flow/apps/auth-api/internal/db/types"
	apierrors "github.com/nodate-flow/nodate-flow/apps/auth-api/internal/errors"
	"github.com/nodate-flow/nodate-flow/packages/go-shared/authn"
)

// OIDCMicrosoftCallback handles GET /auth/oidc/microsoft/callback. It
// exchanges the authorization code for an id_token via the Microsoft
// OIDC provider, finds or creates an identity row, and issues fresh
// app tokens.
func OIDCMicrosoftCallback(deps Deps) func(context.Context, *OIDCCallbackInput) (*OIDCCallbackOutput, error) {
	return func(ctx context.Context, in *OIDCCallbackInput) (*OIDCCallbackOutput, error) {
		if deps.OIDCMicrosoft == nil {
			return nil, httpErr(apierrors.AuthOidcMicrosoftNotConfigured)
		}
		nonce, err := deps.JWT.VerifyOIDCState(in.State)
		if err != nil {
			return nil, httpErr(apierrors.AuthOidcStateMismatch)
		}
		idTok, err := deps.OIDCMicrosoft.Exchange(ctx, in.Code, nonce)
		if err != nil {
			return nil, httpErr(apierrors.AuthOidcIdTokenInvalid)
		}
		var claims struct {
			Email    string `json:"email"`
			Sub      string `json:"sub"`
			Name     string `json:"name"`
			Verified bool   `json:"email_verified"`
		}
		if err := idTok.Claims(&claims); err != nil {
			return nil, httpErr(apierrors.AuthOidcIdTokenInvalid)
		}
		// Microsoft sometimes returns preferred_username instead of email.
		if claims.Email == "" {
			var alt struct {
				PreferredUsername string `json:"preferred_username"`
			}
			_ = idTok.Claims(&alt)
			claims.Email = alt.PreferredUsername
		}
		if claims.Email == "" {
			return nil, httpErr(apierrors.AuthOidcIdTokenInvalid)
		}

		ident, err := deps.Queries.FindIdentityByProviderSubject(ctx, generated.FindIdentityByProviderSubjectParams{
			Provider: generated.IdentitiesProvider("microsoft"),
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
			if !deps.RegistrationOpen {
				return nil, httpErr(apierrors.AuthRegisterInstanceRegistrationDisabled)
			}
			userPub = types.New()
			uid, err := deps.Queries.RegisterUser(ctx, generated.RegisterUserParams{
				PublicID:        userPub,
				Email:           claims.Email,
				DisplayName:     claims.Name,
				Locale:          "en",
				ThemePreference: generated.UsersThemePreference("system"),
			})
			if err != nil {
				return nil, httpErr(apierrors.InternalUnexpected)
			}
			userID = uint32(uid) //#nosec G115 -- LastInsertId for users.id (BIGINT UNSIGNED AUTO_INCREMENT) fits uint32 in any realistic deployment
			identPub := types.New()
			if _, err := deps.Queries.CreateIdentity(ctx, generated.CreateIdentityParams{
				PublicID: identPub,
				UserID:   userID,
				Provider: generated.IdentitiesProvider("microsoft"),
				Subject:  claims.Sub,
			}); err != nil {
				return nil, httpErr(apierrors.InternalUnexpected)
			}
		default:
			return nil, httpErr(apierrors.InternalUnexpected)
		}

		deps.Audit.Record(ctx, audit.Entry{
			Action:       "auth.login_oidc",
			ActorID:      userID,
			ResourceType: "user",
			Metadata:     map[string]any{"provider": "microsoft", "email": claims.Email},
		})
		tokens, refresh, err := issueTokens(ctx, deps, userID, userPub, in.UserAgent, authn.ClientIPFromContext(ctx))
		if err != nil {
			return nil, err
		}
		return &OIDCCallbackOutput{
			SetCookie: newRefreshCookie(refresh, deps.CookieSecure),
			Body:      tokens,
		}, nil
	}
}
