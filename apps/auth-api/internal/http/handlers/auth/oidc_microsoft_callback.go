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
		claims, err := deps.OIDCMicrosoft.Exchange(ctx, in.Code, nonce)
		if err != nil {
			return nil, httpErr(apierrors.AuthOidcIdTokenInvalid)
		}
		// Microsoft sometimes returns preferred_username instead of email.
		email := claims.Email
		if email == "" {
			email = claims.PreferredUsername
		}
		if email == "" {
			return nil, httpErr(apierrors.AuthOidcIdTokenInvalid)
		}
		// Reject unverified emails. Microsoft Entra ID may omit
		// email_verified or set it to false for personal Microsoft
		// accounts whose email has not been confirmed; auto-provisioning
		// an account with an unverified email would let an attacker
		// claim a victim's email by creating an unverified Microsoft
		// account first.
		if !claims.EmailVerified {
			return nil, httpErr(apierrors.AuthOidcEmailNotVerified)
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
				Email:           email,
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
			Metadata:     map[string]any{"provider": "microsoft", "email": email},
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
