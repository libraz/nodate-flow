package auth

import (
	"context"
	"log/slog"

	apierrors "github.com/libraz/nodate-flow/apps/auth-api/internal/errors"
	"github.com/libraz/nodate-flow/packages/go-shared/authn"
)

// OIDCGithubStart handles GET /auth/oidc/github/start. It returns the
// authorization URL the client should redirect to.
//
// The GitHub authorization endpoint is fixed (no discovery round
// trip), so the underlying [GithubExchanger.AuthCodeURL] does not
// take a context. We still accept and consume the request ctx to
// match the Google / Microsoft start handlers and to feed
// trace-aware logging via [slog.DebugContext].
func OIDCGithubStart(deps Deps) func(context.Context, *struct{}) (*OIDCStartOutput, error) {
	return func(ctx context.Context, _ *struct{}) (*OIDCStartOutput, error) {
		if deps.OIDCGithub == nil {
			return nil, httpErr(apierrors.AuthOidcGithubNotConfigured)
		}
		state, err := deps.JWT.SignOIDCStateForProvider(authn.RandomHex(16), "github")
		if err != nil {
			slog.ErrorContext(ctx, "oidc github start: failed to sign state",
				slog.String("err", err.Error()))
			return nil, httpErr(apierrors.InternalUnexpected)
		}
		url := deps.OIDCGithub.AuthCodeURL(state)
		slog.DebugContext(ctx, "oidc github start: authorization url issued")
		out := &OIDCStartOutput{}
		out.Body.AuthorizationURL = url
		out.Body.State = state
		return out, nil
	}
}
