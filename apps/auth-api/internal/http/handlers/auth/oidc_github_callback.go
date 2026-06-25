package auth

import (
	"context"
	"errors"

	"github.com/nodate-flow/nodate-flow/apps/auth-api/internal/audit"
	internauth "github.com/nodate-flow/nodate-flow/apps/auth-api/internal/auth"
	"github.com/nodate-flow/nodate-flow/apps/auth-api/internal/db/generated"
	apierrors "github.com/nodate-flow/nodate-flow/apps/auth-api/internal/errors"
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

		name := claims.Name
		if name == "" {
			name = claims.Login
		}
		// Resolve the verified sign-in to a user: log in an existing
		// identity, link onto an existing same-email account, or
		// auto-provision a fresh user (gated by RegistrationOpen). See
		// resolveOIDCUser for the full ordering.
		userID, userPub, err := deps.resolveOIDCUser(ctx, oidcProvisionParams{
			Provider:    generated.IdentitiesProvider("github"),
			Subject:     claims.Sub,
			Email:       claims.Email,
			DisplayName: name,
			Locale:      "en",
		})
		if err != nil {
			return nil, err
		}

		deps.Audit.Record(ctx, audit.Entry{
			Action:       "auth.login_oidc",
			ActorID:      userID,
			ResourceType: "user",
			Metadata:     map[string]any{"provider": "github", "email": claims.Email},
		})
		// Shared step-up gate: if the account enrolled app-level TOTP,
		// finishOIDCLogin returns a totp_required challenge instead of
		// tokens. See its doc comment for the policy.
		return deps.finishOIDCLogin(ctx, userID, userPub, in.UserAgent)
	}
}
