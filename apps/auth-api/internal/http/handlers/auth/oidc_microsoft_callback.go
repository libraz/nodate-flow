package auth

import (
	"context"

	"github.com/nodate-flow/nodate-flow/apps/auth-api/internal/audit"
	internauth "github.com/nodate-flow/nodate-flow/apps/auth-api/internal/auth"
	"github.com/nodate-flow/nodate-flow/apps/auth-api/internal/db/generated"
	apierrors "github.com/nodate-flow/nodate-flow/apps/auth-api/internal/errors"
)

// OIDCMicrosoftCallback handles GET /auth/oidc/microsoft/callback. It
// exchanges the authorization code for an id_token via the Microsoft
// OIDC provider, finds or creates an identity row, and issues fresh
// app tokens.
func OIDCMicrosoftCallback(deps Deps) func(context.Context, *OIDCCallbackInput) (*OIDCCallbackOutput, error) {
	return func(ctx context.Context, in *OIDCCallbackInput) (*OIDCCallbackOutput, error) {
		// Surface provider-side rejections before any code-exchange
		// logic. Microsoft Entra ID redirects with
		// `?error=...&error_description=...` when the user denies
		// consent or the relying-party app is misconfigured; without
		// this branch the handler would fail later with a generic
		// "id_token invalid" code and obscure the real cause.
		if in.Error != "" {
			return nil, oidcProviderRejection(ctx, "microsoft", in.Error, in.ErrorDescription)
		}
		if deps.OIDCMicrosoft == nil {
			return nil, httpErr(apierrors.AuthOidcMicrosoftNotConfigured)
		}
		// Validate the state JWT for CSRF protection. The signed state
		// embeds the nonce + the provider it was minted for; the
		// provider claim defends against cross-provider state replay.
		nonce, err := deps.JWT.VerifyOIDCStateForProvider(in.State, "microsoft")
		if err != nil {
			return nil, httpErr(apierrors.AuthOidcStateMismatch)
		}
		claims, err := deps.OIDCMicrosoft.Exchange(ctx, in.Code, nonce)
		if err != nil {
			return nil, httpErr(apierrors.AuthOidcIdTokenInvalid)
		}
		if err := internauth.ValidateMicrosoftClaims(claims, deps.MicrosoftAllowedTenantIDs); err != nil {
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
		// Email trust for Microsoft is enforced by
		// ValidateMicrosoftClaims: the allowed tenant must match and
		// xms_edov must be true. Entra v2.0 commonly omits the generic
		// email_verified claim, so the callback must not apply a second
		// provider-agnostic gate here.
		// Enforce the opt-in sign-in allowlist on the verified email
		// before any provisioning or session issuance. Gates both
		// new-user creation and existing-user login. No-op (allows all)
		// when the allowlist is unconfigured.
		if !deps.isSignInEmailAllowed(email) {
			return nil, httpErr(apierrors.AuthOidcDomainNotAllowed)
		}

		// Resolve the verified sign-in to a user: log in an existing
		// identity, link onto an existing same-email account, or
		// auto-provision a fresh user (gated by RegistrationOpen). See
		// resolveOIDCUser for the full ordering.
		userID, userPub, err := deps.resolveOIDCUser(ctx, oidcProvisionParams{
			Provider:       generated.IdentitiesProvider("microsoft"),
			Subject:        claims.Sub,
			Email:          email,
			DisplayName:    claims.Name,
			Locale:         "en",
			AllowEmailLink: false,
		})
		if err != nil {
			return nil, err
		}

		deps.Audit.Record(ctx, audit.Entry{
			Action:       "auth.login_oidc",
			ActorID:      userID,
			ResourceType: "user",
			Metadata:     map[string]any{"provider": "microsoft", "email": email},
		})
		// Shared step-up gate: if the account enrolled app-level TOTP,
		// finishOIDCLogin returns a totp_required challenge instead of
		// tokens. See its doc comment for the policy.
		return deps.finishOIDCLogin(ctx, userID, userPub, in.UserAgent)
	}
}
