package auth

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/libraz/nodate-flow/apps/auth-api/internal/audit"
	internauth "github.com/libraz/nodate-flow/apps/auth-api/internal/auth"
	apierrors "github.com/libraz/nodate-flow/apps/auth-api/internal/errors"
	"github.com/libraz/nodate-flow/apps/auth-api/internal/http/handlers/handlerutil"
	"github.com/libraz/nodate-flow/packages/go-shared/authn"
)

// fakeGoogleExchanger captures the inputs supplied to Exchange and
// returns canned GoogleClaims so the callback's branches can be covered
// without the real discovery document or any HTTP traffic.
type fakeGoogleExchanger struct {
	gotCode  string
	gotNonce string
	claims   *internauth.GoogleClaims
	err      error
}

func (f *fakeGoogleExchanger) AuthCodeURL(_ context.Context, state, _ string) (string, error) {
	return "https://example.invalid/auth?state=" + state, nil
}

func (f *fakeGoogleExchanger) Exchange(_ context.Context, code, nonce string) (*internauth.GoogleClaims, error) {
	f.gotCode = code
	f.gotNonce = nonce
	if f.err != nil {
		return nil, f.err
	}
	return f.claims, nil
}

func signedGoogleState(t *testing.T, deps *Deps) (string, http.Cookie) {
	t.Helper()
	return boundOIDCState(t, deps, "google", "nonce-value")
}

// TestOIDCGoogleCallback_PassesNonceFromState verifies the nonce in the
// signed state reaches the Exchange call.
func TestOIDCGoogleCallback_PassesNonceFromState(t *testing.T) {
	t.Parallel()
	jwt, err := internauth.NewJWTIssuer(nil, "iss", "aud", time.Minute)
	require.NoError(t, err)

	const wantNonce = "01961234-5678-7000-8000-deadbeefcafe"
	g := &fakeGoogleExchanger{err: errors.New("stop after capturing nonce")}
	deps := Deps{JWT: jwt, OIDC: g, Audit: audit.NoopSink{}}
	state, stateCookie := boundOIDCState(t, &deps, "google", wantNonce)
	_, err = OIDCGoogleCallback(deps)(context.Background(), &OIDCCallbackInput{
		Code: "auth-code", State: state, StateCookie: stateCookie,
	})

	problem := problemFor(t, err)
	assert.Equal(t, apierrors.AuthOidcIdTokenInvalid.Code, problem.Type)
	assert.Equal(t, "auth-code", g.gotCode)
	assert.Equal(t, wantNonce, g.gotNonce)
}

// TestOIDCGoogleCallback_NonceMismatchHasItsOwnCode asserts a replayed
// id_token is answered with AUTH.OIDC.NONCE_MISMATCH, not the generic
// verification failure.
func TestOIDCGoogleCallback_NonceMismatchHasItsOwnCode(t *testing.T) {
	t.Parallel()
	jwt, err := internauth.NewJWTIssuer(nil, "iss", "aud", time.Minute)
	require.NoError(t, err)
	g := &fakeGoogleExchanger{err: fmt.Errorf("google: %w", authn.ErrNonceMismatch)}
	deps := Deps{JWT: jwt, OIDC: g, Audit: audit.NoopSink{}}
	state, stateCookie := signedGoogleState(t, &deps)
	_, err = OIDCGoogleCallback(deps)(context.Background(), &OIDCCallbackInput{
		Code: "auth-code", State: state, StateCookie: stateCookie,
	})

	problem := problemFor(t, err)
	assert.Equal(t, apierrors.AuthOidcNonceMismatch.Code, problem.Type)
}

// TestOIDCGoogleCallback_RejectsUnverifiedEmail asserts an id_token whose
// email_verified claim is false is refused.
func TestOIDCGoogleCallback_RejectsUnverifiedEmail(t *testing.T) {
	t.Parallel()
	jwt, err := internauth.NewJWTIssuer(nil, "iss", "aud", time.Minute)
	require.NoError(t, err)
	g := &fakeGoogleExchanger{claims: &internauth.GoogleClaims{
		Sub: "g-sub", Email: "alice@example.com", EmailVerified: false,
	}}
	deps := Deps{JWT: jwt, OIDC: g, Audit: audit.NoopSink{}}
	state, stateCookie := signedGoogleState(t, &deps)
	_, err = OIDCGoogleCallback(deps)(context.Background(), &OIDCCallbackInput{
		Code: "auth-code", State: state, StateCookie: stateCookie,
	})

	problem := problemFor(t, err)
	assert.Equal(t, apierrors.AuthOidcEmailNotVerified.Code, problem.Type)
}

// TestOIDCGoogleCallback_RejectsBadState asserts Exchange is never
// reached when the state cannot be verified.
func TestOIDCGoogleCallback_RejectsBadState(t *testing.T) {
	t.Parallel()
	jwt, err := internauth.NewJWTIssuer(nil, "iss", "aud", time.Minute)
	require.NoError(t, err)
	g := &fakeGoogleExchanger{}
	deps := Deps{JWT: jwt, OIDC: g, Audit: audit.NoopSink{}}
	_, stateCookie := signedGoogleState(t, &deps)
	_, err = OIDCGoogleCallback(deps)(context.Background(), &OIDCCallbackInput{
		Code: "c", State: "garbage", StateCookie: stateCookie,
	})
	require.Error(t, err)
	assert.Empty(t, g.gotCode)
}

// TestOIDCGoogleCallback_SurfacesProviderRejection asserts that an IdP
// callback carrying `?error=...&error_description=...` is surfaced as
// AUTH.OIDC.PROVIDER_REJECTED with HTTP 400 BEFORE the handler even
// touches the OIDC client. Without this guard a Google-side rejection
// (denied consent, rejected scopes, misconfigured app) would surface
// as a generic AUTH.OIDC.ID_TOKEN_INVALID and obscure the real cause.
//
// The test deliberately leaves deps.OIDC nil to prove the
// provider-rejection branch fires before the configured-provider
// check; a regression that re-orders the two would surface here as
// either a panic or AUTH.OIDC.PROVIDER_UNREACHABLE.
func TestOIDCGoogleCallback_SurfacesProviderRejection(t *testing.T) {
	t.Parallel()

	deps := Deps{
		Audit: audit.NoopSink{},
	}
	handler := OIDCGoogleCallback(deps)
	_, err := handler(context.Background(), &OIDCCallbackInput{
		Error:            "access_denied",
		ErrorDescription: "user denied",
	})
	require.Error(t, err)

	var problem *handlerutil.ProblemDetails
	require.True(t, errors.As(err, &problem), "expected handlerutil.ProblemDetails, got %T", err)
	assert.Equal(t, apierrors.AuthOidcProviderRejected.Code, problem.Type,
		"provider-rejected callback must surface AUTH.OIDC.PROVIDER_REJECTED")
	assert.Equal(t, apierrors.AuthOidcProviderRejected.Status, problem.Status)
	assert.Equal(t, "google", problem.Extensions["provider"])
	assert.Equal(t, "access_denied", problem.Extensions["provider_error"])
	assert.Equal(t, "user denied", problem.Extensions["provider_error_description"])
}

// TestOIDCGoogleCallback_OmitsEmptyErrorDescription asserts the
// handler does not insert a `provider_error_description` extension
// member when the IdP omits the optional human-readable description.
// This keeps the wire envelope minimal on the common path while
// still emitting the slug that drives downstream diagnostics.
func TestOIDCGoogleCallback_OmitsEmptyErrorDescription(t *testing.T) {
	t.Parallel()

	deps := Deps{
		Audit: audit.NoopSink{},
	}
	handler := OIDCGoogleCallback(deps)
	_, err := handler(context.Background(), &OIDCCallbackInput{
		Error: "access_denied",
	})
	require.Error(t, err)

	var problem *handlerutil.ProblemDetails
	require.True(t, errors.As(err, &problem))
	_, hasDesc := problem.Extensions["provider_error_description"]
	assert.False(t, hasDesc,
		"empty error_description must be omitted from extensions")
	assert.Equal(t, "access_denied", problem.Extensions["provider_error"])
}
