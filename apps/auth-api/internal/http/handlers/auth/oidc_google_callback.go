package auth

import (
	"context"

	"github.com/libraz/nodate-flow/apps/auth-api/internal/audit"
	"github.com/libraz/nodate-flow/apps/auth-api/internal/db/generated"
	apierrors "github.com/libraz/nodate-flow/apps/auth-api/internal/errors"
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
		// Verify and consume the state before touching the code. See
		// consumeOIDCState for what the gate covers.
		nonce, err := deps.consumeOIDCState(ctx, in, "google")
		if err != nil {
			return nil, err
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

		// Resolve the verified sign-in to a user: log in an existing
		// identity, link onto an existing same-email account, or
		// auto-provision a fresh user (gated by RegistrationOpen). See
		// resolveOIDCUser for the full ordering.
		userID, userPub, err := deps.resolveOIDCUser(ctx, oidcProvisionParams{
			Provider:       generated.IdentitiesProvider("google"),
			Subject:        claims.Sub,
			Email:          claims.Email,
			DisplayName:    claims.Name,
			Locale:         claims.Locale,
			AllowEmailLink: true,
		})
		if err != nil {
			return nil, err
		}

		deps.Audit.Record(ctx, audit.Entry{
			Action:       "auth.login_oidc",
			ActorID:      userID,
			ResourceType: "user",
			Metadata:     map[string]any{"provider": "google", "email": claims.Email},
		})
		// Shared step-up gate: if the account enrolled app-level TOTP,
		// finishOIDCLogin returns a totp_required challenge instead of
		// tokens. See its doc comment for the policy.
		return deps.finishOIDCLogin(ctx, userID, userPub, in.UserAgent)
	}
}
