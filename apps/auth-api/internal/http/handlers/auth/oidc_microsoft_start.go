package auth

import (
	"context"

	apierrors "github.com/nodate-flow/nodate-flow/apps/auth-api/internal/errors"
	"github.com/nodate-flow/nodate-flow/packages/go-shared/authn"
)

// OIDCMicrosoftStart handles GET /auth/oidc/microsoft/start. It returns
// the authorization URL for the Microsoft OIDC login flow.
func OIDCMicrosoftStart(deps Deps) func(context.Context, *struct{}) (*OIDCStartOutput, error) {
	return func(ctx context.Context, _ *struct{}) (*OIDCStartOutput, error) {
		if deps.OIDCMicrosoft == nil {
			return nil, httpErr(apierrors.AuthOidcMicrosoftNotConfigured)
		}
		nonce := authn.RandomHex(16)
		state, err := deps.JWT.SignOIDCState(nonce)
		if err != nil {
			return nil, httpErr(apierrors.InternalUnexpected)
		}
		url, err := deps.OIDCMicrosoft.AuthCodeURL(ctx, state, nonce)
		if err != nil {
			return nil, httpErr(apierrors.AuthOidcProviderUnreachable)
		}
		out := &OIDCStartOutput{}
		out.Body.AuthorizationURL = url
		out.Body.State = state
		return out, nil
	}
}
