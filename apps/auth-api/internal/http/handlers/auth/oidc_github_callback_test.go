package auth

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/nodate-flow/nodate-flow/apps/auth-api/internal/audit"
	internauth "github.com/nodate-flow/nodate-flow/apps/auth-api/internal/auth"
	apierrors "github.com/nodate-flow/nodate-flow/apps/auth-api/internal/errors"
	"github.com/nodate-flow/nodate-flow/apps/auth-api/internal/http/handlers/handlerutil"
)

// fakeGithubExchanger captures the nonce passed to Exchange so the
// nonce-roundtrip test can assert the callback wires the verified
// nonce through to the OAuth client.
type fakeGithubExchanger struct {
	gotCode  string
	gotNonce string
	claims   *internauth.GithubClaims
	err      error
}

func (f *fakeGithubExchanger) AuthCodeURL(state string) string {
	return "https://example.invalid/auth?state=" + state
}

func (f *fakeGithubExchanger) Exchange(_ context.Context, code, nonce string) (*internauth.GithubClaims, error) {
	f.gotCode = code
	f.gotNonce = nonce
	if f.err != nil {
		return nil, f.err
	}
	return f.claims, nil
}

// TestOIDCGithubCallback_PassesNonceFromState verifies that the
// nonce embedded in the signed OIDC state JWT is forwarded into the
// GitHub OAuth Exchange call. Discarding the nonce — the bug the
// audit caught — is reflected as an empty string at the boundary.
func TestOIDCGithubCallback_PassesNonceFromState(t *testing.T) {
	t.Parallel()
	jwt, err := internauth.NewJWTIssuer(nil, "test-issuer", "test-audience", time.Minute)
	require.NoError(t, err)

	const wantNonce = "01961234-5678-7000-8000-deadbeefcafe"
	state, err := jwt.SignOIDCStateForProvider(wantNonce, "github")
	require.NoError(t, err)

	gh := &fakeGithubExchanger{
		err: errors.New("stop after capturing nonce"),
	}
	deps := Deps{
		JWT:        jwt,
		OIDCGithub: gh,
		Audit:      audit.NoopSink{},
	}
	handler := OIDCGithubCallback(deps)
	_, err = handler(context.Background(), &OIDCCallbackInput{Code: "auth-code", State: state})
	require.Error(t, err, "Exchange returned an error so handler must propagate")

	assert.Equal(t, "auth-code", gh.gotCode, "code must be forwarded verbatim")
	assert.Equal(t, wantNonce, gh.gotNonce,
		"nonce decoded from the signed state must be forwarded to Exchange (was discarded before the audit fix)")
}

// TestOIDCGithubCallback_RejectsUnverifiedEmail asserts the callback
// returns AUTH.OIDC.EMAIL_NOT_VERIFIED when the OAuth client signals
// no primary verified email is available. This keeps GitHub aligned
// with Google and Microsoft, which reject unverified emails using the
// same error code.
func TestOIDCGithubCallback_RejectsUnverifiedEmail(t *testing.T) {
	t.Parallel()
	jwt, err := internauth.NewJWTIssuer(nil, "iss", "aud", time.Minute)
	require.NoError(t, err)
	state, err := jwt.SignOIDCStateForProvider("nonce-value", "github")
	require.NoError(t, err)

	gh := &fakeGithubExchanger{err: internauth.ErrGithubEmailNotVerified}
	deps := Deps{
		JWT:        jwt,
		OIDCGithub: gh,
		Audit:      audit.NoopSink{},
	}
	handler := OIDCGithubCallback(deps)
	_, err = handler(context.Background(), &OIDCCallbackInput{Code: "auth-code", State: state})
	require.Error(t, err)

	var problem *handlerutil.ProblemDetails
	require.True(t, errors.As(err, &problem), "expected handlerutil.ProblemDetails, got %T", err)
	assert.Equal(t, apierrors.AuthOidcEmailNotVerified.Code, problem.Type,
		"unverified primary email must surface as AUTH.OIDC.EMAIL_NOT_VERIFIED")
	assert.Equal(t, apierrors.AuthOidcEmailNotVerified.Status, problem.Status)
}

// TestOIDCGithubCallback_RejectsBadState asserts state validation
// fails fast and Exchange is never reached when the state JWT cannot
// be verified.
func TestOIDCGithubCallback_RejectsBadState(t *testing.T) {
	t.Parallel()
	jwt, err := internauth.NewJWTIssuer(nil, "iss", "aud", time.Minute)
	require.NoError(t, err)

	gh := &fakeGithubExchanger{}
	deps := Deps{
		JWT:        jwt,
		OIDCGithub: gh,
		Audit:      audit.NoopSink{},
	}
	handler := OIDCGithubCallback(deps)
	_, err = handler(context.Background(), &OIDCCallbackInput{Code: "c", State: "garbage"})
	require.Error(t, err)
	assert.Empty(t, gh.gotCode, "Exchange must not run when state verification fails")
}

// TestOIDCGithubCallback_SurfacesProviderRejection asserts that an IdP
// callback carrying `?error=...&error_description=...` is surfaced as
// AUTH.OIDC.PROVIDER_REJECTED with HTTP 400, and the token-exchange
// stub is never invoked. This guards against the audit-found behaviour
// where a user denying consent (or a misconfigured app) would surface
// as a cryptic AUTH.OIDC.ID_TOKEN_INVALID instead of a precise
// provider-rejection code.
func TestOIDCGithubCallback_SurfacesProviderRejection(t *testing.T) {
	t.Parallel()
	jwt, err := internauth.NewJWTIssuer(nil, "iss", "aud", time.Minute)
	require.NoError(t, err)
	state, err := jwt.SignOIDCStateForProvider("nonce-value", "github")
	require.NoError(t, err)

	gh := &fakeGithubExchanger{}
	deps := Deps{
		JWT:        jwt,
		OIDCGithub: gh,
		Audit:      audit.NoopSink{},
	}
	handler := OIDCGithubCallback(deps)
	_, err = handler(context.Background(), &OIDCCallbackInput{
		State:            state,
		Error:            "access_denied",
		ErrorDescription: "user denied",
	})
	require.Error(t, err)

	var problem *handlerutil.ProblemDetails
	require.True(t, errors.As(err, &problem), "expected handlerutil.ProblemDetails, got %T", err)
	assert.Equal(t, apierrors.AuthOidcProviderRejected.Code, problem.Type,
		"provider-rejected callback must surface AUTH.OIDC.PROVIDER_REJECTED")
	assert.Equal(t, apierrors.AuthOidcProviderRejected.Status, problem.Status)
	assert.Equal(t, "github", problem.Extensions["provider"])
	assert.Equal(t, "access_denied", problem.Extensions["provider_error"])
	assert.Equal(t, "user denied", problem.Extensions["provider_error_description"])
	assert.Empty(t, gh.gotCode, "Exchange must not run when the IdP rejects the callback")
}
