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

// OIDCGoogleCallback handles GET /auth/oidc/google/callback. It exchanges
// the authorization code for an id_token, finds or creates an identity
// row, and issues fresh app tokens.
func OIDCGoogleCallback(deps Deps) func(context.Context, *OIDCCallbackInput) (*OIDCCallbackOutput, error) {
	return func(ctx context.Context, in *OIDCCallbackInput) (*OIDCCallbackOutput, error) {
		// Surface provider-side rejections before any code-exchange
		// logic. Google redirects with `?error=...&error_description=...`
		// when the user denies consent or the relying-party app is
		// misconfigured; without this branch the handler would fail
		// later with a generic "id_token invalid" code and obscure the
		// real cause.
		if in.Error != "" {
			return nil, oidcProviderRejection(ctx, "google", in.Error, in.ErrorDescription)
		}
		if deps.OIDC == nil {
			return nil, httpErr(apierrors.AuthOidcProviderUnreachable)
		}
		// Validate the state JWT to prevent CSRF. The signed state
		// embeds the nonce and the provider it was minted for; the
		// provider check stops a state issued for one redirect URI
		// from being redeemed at another.
		nonce, err := deps.JWT.VerifyOIDCStateForProvider(in.State, "google")
		if err != nil {
			return nil, httpErr(apierrors.AuthOidcStateMismatch)
		}
		idTok, err := deps.OIDC.Exchange(ctx, in.Code, nonce)
		if err != nil {
			return nil, httpErr(apierrors.AuthOidcIdTokenInvalid)
		}
		var claims struct {
			Email    string `json:"email"`
			Sub      string `json:"sub"`
			Name     string `json:"name"`
			Locale   string `json:"locale"`
			Verified bool   `json:"email_verified"`
		}
		if err := idTok.Claims(&claims); err != nil {
			return nil, httpErr(apierrors.AuthOidcIdTokenInvalid)
		}
		// Reject unverified emails. Google OIDC normally returns
		// email_verified=true, but compromised or misconfigured IdPs
		// might not; this check prevents account creation with an
		// unverified email address.
		if !claims.Verified {
			return nil, httpErr(apierrors.AuthOidcEmailNotVerified)
		}
		// Enforce the opt-in sign-in allowlist on the verified email
		// before any provisioning or session issuance. Gates both
		// new-user creation and existing-user login. No-op (allows all)
		// when the allowlist is unconfigured.
		if !deps.isSignInEmailAllowed(claims.Email) {
			return nil, httpErr(apierrors.AuthOidcDomainNotAllowed)
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
			// Block auto-provisioning when self-service registration is
			// disabled at the instance level.
			if !deps.RegistrationOpen {
				return nil, httpErr(apierrors.AuthRegisterInstanceRegistrationDisabled)
			}
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
			userID = uint32(uid) //#nosec G115 -- LastInsertId for users.id (BIGINT UNSIGNED AUTO_INCREMENT) fits uint32 in any realistic deployment
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

		deps.Audit.Record(ctx, audit.Entry{
			Action:       "auth.login_oidc",
			ActorID:      userID,
			ResourceType: "user",
			Metadata:     map[string]any{"provider": "google", "email": claims.Email},
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
