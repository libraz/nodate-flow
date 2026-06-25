package auth

import (
	"context"
	"database/sql"
	"errors"

	"github.com/nodate-flow/nodate-flow/apps/auth-api/internal/audit"
	internauth "github.com/nodate-flow/nodate-flow/apps/auth-api/internal/auth"
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
		// Surface provider-side rejections before any code-exchange
		// logic. GitHub redirects with `?error=...&error_description=...`
		// when the user denies consent or the relying-party app is
		// misconfigured; without this branch the handler would fail
		// later with a generic "id_token invalid" / token-exchange
		// error and obscure the real cause.
		if in.Error != "" {
			return nil, oidcProviderRejection(ctx, "github", in.Error, in.ErrorDescription)
		}
		if deps.OIDCGithub == nil {
			return nil, httpErr(apierrors.AuthOidcGithubNotConfigured)
		}
		// Validate the state JWT for CSRF protection. The signed state
		// embeds the nonce + the provider it was minted for; the
		// provider claim defends against cross-provider state replay
		// (a state issued for /oidc/google/callback can't be redeemed
		// here). GitHub itself does not bind a nonce yet, but
		// threading it through removes drift between handlers and
		// prepares for a future when it does.
		nonce, err := deps.JWT.VerifyOIDCStateForProvider(in.State, "github")
		if err != nil {
			return nil, httpErr(apierrors.AuthOidcStateMismatch)
		}
		claims, err := deps.OIDCGithub.Exchange(ctx, in.Code, nonce)
		if err != nil {
			// GitHub does not return email_verified in an id_token (it
			// has no id_token at all), so the verification check is
			// performed inside the OAuth client when it resolves the
			// primary email. Surface that as AUTH.OIDC.EMAIL_NOT_VERIFIED
			// so the three providers reject unverified accounts with
			// the same code.
			if errors.Is(err, internauth.ErrGithubEmailNotVerified) {
				return nil, httpErr(apierrors.AuthOidcEmailNotVerified)
			}
			return nil, httpErr(apierrors.AuthOidcIdTokenInvalid)
		}
		if claims.Email == "" {
			return nil, httpErr(apierrors.AuthOidcIdTokenInvalid)
		}
		// Enforce the opt-in sign-in allowlist on the verified email
		// (GitHub's exchanger already rejected unverified primary
		// emails above) before any provisioning or session issuance.
		// Gates both new-user creation and existing-user login. No-op
		// (allows all) when the allowlist is unconfigured.
		if !deps.isSignInEmailAllowed(claims.Email) {
			return nil, httpErr(apierrors.AuthOidcDomainNotAllowed)
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
		tokens, refresh, err := IssueTokens(ctx, deps, userID, userPub, in.UserAgent, authn.ClientIPFromContext(ctx))
		if err != nil {
			return nil, err
		}
		return &OIDCCallbackOutput{
			SetCookie: newRefreshCookie(refresh, deps.CookieSecure),
			Body:      tokens,
		}, nil
	}
}
