package auth

import (
	"context"
	"crypto/rand"
	"encoding/hex"

	apierrors "github.com/nodate-flow/nodate-flow/apps/flow-api/internal/errors"
)

// OIDCGoogleStart handles GET /auth/oidc/google/start. It returns the
// authorization URL the client should redirect to, plus the state and
// nonce values the client must echo back on the callback.
func OIDCGoogleStart(deps Deps) func(context.Context, *struct{}) (*OIDCStartOutput, error) {
	return func(ctx context.Context, _ *struct{}) (*OIDCStartOutput, error) {
		if deps.OIDC == nil {
			return nil, httpErr(apierrors.AuthOidcProviderUnreachable)
		}
		nonce := randomHex(16)
		// State is a signed JWT that embeds the nonce. The callback
		// validates the JWT signature + expiry to provide CSRF
		// protection without server-side storage.
		state, err := deps.JWT.SignOIDCState(nonce)
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
		out.Body.Nonce = nonce
		return out, nil
	}
}

func randomHex(n int) string {
	b := make([]byte, n)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}
