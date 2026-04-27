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

// OIDCGithubCallback handles GET /auth/oidc/github/callback. It
// exchanges the authorization code for an access token, fetches the
// GitHub user profile, finds or creates an identity row, and issues
// fresh app tokens.
func OIDCGithubCallback(deps Deps) func(context.Context, *OIDCCallbackInput) (*OIDCCallbackOutput, error) {
	return func(ctx context.Context, in *OIDCCallbackInput) (*OIDCCallbackOutput, error) {
		if deps.OIDCGithub == nil {
			return nil, httpErr(apierrors.AuthOidcGithubNotConfigured)
		}
		// Validate the state JWT for CSRF protection. The signed state
		// embeds the nonce so we can pass it uniformly to Exchange,
		// matching the Google/Microsoft callbacks; GitHub itself does
		// not bind a nonce yet, but threading it through removes the
		// drift between handlers and prepares for a future when it
		// does.
		nonce, err := deps.JWT.VerifyOIDCState(in.State)
		if err != nil {
			return nil, httpErr(apierrors.AuthOidcStateMismatch)
		}
		claims, err := deps.OIDCGithub.Exchange(ctx, in.Code, nonce)
		if err != nil {
			return nil, httpErr(apierrors.AuthOidcIdTokenInvalid)
		}
		if claims.Email == "" {
			return nil, httpErr(apierrors.AuthOidcIdTokenInvalid)
		}

		ident, err := deps.Queries.FindIdentityByProviderSubject(ctx, generated.FindIdentityByProviderSubjectParams{
			Provider: generated.IdentitiesProvider("github"),
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
			name := claims.Name
			if name == "" {
				name = claims.Login
			}
			uid, err := deps.Queries.RegisterUser(ctx, generated.RegisterUserParams{
				PublicID:        userPub,
				Email:           claims.Email,
				DisplayName:     name,
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
				Provider: generated.IdentitiesProvider("github"),
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
			Metadata:     map[string]any{"provider": "github", "email": claims.Email},
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
