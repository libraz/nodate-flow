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
	state, err := jwt.SignOIDCState(wantNonce)
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
