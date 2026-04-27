package auth

import (
	"context"

	apierrors "github.com/nodate-flow/nodate-flow/apps/auth-api/internal/errors"
	"github.com/nodate-flow/nodate-flow/packages/go-shared/authn"
)

// OIDCGithubStart handles GET /auth/oidc/github/start. It returns the
// authorization URL the client should redirect to.
func OIDCGithubStart(deps Deps) func(context.Context, *struct{}) (*OIDCStartOutput, error) {
	return func(_ context.Context, _ *struct{}) (*OIDCStartOutput, error) {
		if deps.OIDCGithub == nil {
			return nil, httpErr(apierrors.AuthOidcGithubNotConfigured)
		}
		state, err := deps.JWT.SignOIDCState(authn.RandomHex(16))
		if err != nil {
			return nil, httpErr(apierrors.InternalUnexpected)
		}
		url := deps.OIDCGithub.AuthCodeURL(state)
		out := &OIDCStartOutput{}
		out.Body.AuthorizationURL = url
		out.Body.State = state
		return out, nil
	}
}
