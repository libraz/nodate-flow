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
		// The state is a signed JWT embedding the nonce, the provider it
		// was minted for, and a hash of the verifier that startOIDCState
		// puts in this browser's cookie. The callback requires all three
		// to line up and refuses a state it has already redeemed.
		out := &OIDCStartOutput{}
		state, err := deps.startOIDCState(ctx, out, nonce, "google")
		if err != nil {
			return nil, err
		}
		url, err := deps.OIDC.AuthCodeURL(ctx, state, nonce)
		if err != nil {
			return nil, httpErr(apierrors.AuthOidcProviderUnreachable)
		}
		out.Body.AuthorizationURL = url
		return out, nil
	}
}
