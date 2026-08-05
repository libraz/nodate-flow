package auth

import (
	"context"

	apierrors "github.com/libraz/nodate-flow/apps/auth-api/internal/errors"
	"github.com/libraz/nodate-flow/packages/go-shared/authn"
)

// OIDCGoogleStart handles GET /auth/oidc/google/start. It returns the
// authorization URL the client should redirect to, plus the state and
// nonce values the client must echo back on the callback.
func OIDCGoogleStart(deps Deps) func(context.Context, *struct{}) (*OIDCStartOutput, error) {
	return func(ctx context.Context, _ *struct{}) (*OIDCStartOutput, error) {
		if deps.OIDC == nil {
			return nil, httpErr(apierrors.AuthOidcProviderUnreachable)
		}
		nonce := authn.RandomHex(16)
		// State is a signed JWT that embeds the nonce and the provider
		// it was minted for. The callback validates the JWT signature,
		// expiry, and provider binding to provide CSRF protection plus
		// cross-provider replay defence without server-side storage.
		state, err := deps.JWT.SignOIDCStateForProvider(nonce, "google")
		if err != nil {
			return nil, httpErr(apierrors.InternalUnexpected)
		}
		url, err := deps.OIDC.AuthCodeURL(ctx, state, nonce)
		if err != nil {
			return nil, httpErr(apierrors.AuthOidcProviderUnreachable)
		}
		out := &OIDCStartOutput{}
		out.Body.AuthorizationURL = url
		out.Body.State = state
		return out, nil
	}
}
