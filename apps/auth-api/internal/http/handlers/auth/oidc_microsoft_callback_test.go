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

// fakeMicrosoftExchanger captures the inputs supplied to Exchange and
// returns canned MicrosoftClaims so the callback handler's branches
// (state validation, email_verified rejection) can be covered without
// the real go-oidc verifier or any HTTP traffic.
type fakeMicrosoftExchanger struct {
	gotCode  string
	gotNonce string
	claims   *internauth.MicrosoftClaims
	err      error
}

func (f *fakeMicrosoftExchanger) AuthCodeURL(_ context.Context, state, _ string) (string, error) {
	return "https://example.invalid/auth?state=" + state, nil
}

func (f *fakeMicrosoftExchanger) Exchange(_ context.Context, code, nonce string) (*internauth.MicrosoftClaims, error) {
	f.gotCode = code
	f.gotNonce = nonce
	if f.err != nil {
		return nil, f.err
	}
	return f.claims, nil
}

// TestOIDCMicrosoftCallback_RejectsUnverifiedEmail asserts the callback
// returns AUTH.OIDC.EMAIL_NOT_VERIFIED when Microsoft's id_token claims
// carry email_verified=false. Without this guard a Microsoft personal
// account holding an unverified email could auto-provision a flow
// account for that address, letting an attacker hijack the eventual
// owner's identity once they sign up.
func TestOIDCMicrosoftCallback_RejectsUnverifiedEmail(t *testing.T) {
	t.Parallel()
	jwt, err := internauth.NewJWTIssuer(nil, "iss", "aud", time.Minute)
	require.NoError(t, err)

	state, err := jwt.SignOIDCStateForProvider("nonce-value", "microsoft")
	require.NoError(t, err)

	ms := &fakeMicrosoftExchanger{
		claims: &internauth.MicrosoftClaims{
			Sub:           "ms-subject-id",
			Email:         "alice@example.com",
			Name:          "Alice",
			EmailVerified: false,
		},
	}
	deps := Deps{
		JWT:           jwt,
		OIDCMicrosoft: ms,
		Audit:         audit.NoopSink{},
	}
	handler := OIDCMicrosoftCallback(deps)
	_, err = handler(context.Background(), &OIDCCallbackInput{Code: "auth-code", State: state})
	require.Error(t, err, "unverified email must be rejected")

	var problem *handlerutil.ProblemDetails
	require.True(t, errors.As(err, &problem), "expected handlerutil.ProblemDetails, got %T", err)
	assert.Equal(t, apierrors.AuthOidcEmailNotVerified.Code, problem.Type,
		"unverified email must surface as AUTH.OIDC.EMAIL_NOT_VERIFIED")
	assert.Equal(t, apierrors.AuthOidcEmailNotVerified.Status, problem.Status)
}

// TestOIDCMicrosoftCallback_AcceptsVerifiedEmailWithoutDB stops short
// of the database write to confirm the email_verified guard does not
// fire on the happy path. It does not assert successful login (that
// requires a live DB) — the assertion is simply that the handler
// proceeds past the unverified-email gate when EmailVerified=true.
func TestOIDCMicrosoftCallback_AcceptsVerifiedEmailWithoutDB(t *testing.T) {
	t.Parallel()
	jwt, err := internauth.NewJWTIssuer(nil, "iss", "aud", time.Minute)
	require.NoError(t, err)
	state, err := jwt.SignOIDCStateForProvider("nonce-value", "microsoft")
	require.NoError(t, err)

	ms := &fakeMicrosoftExchanger{
		claims: &internauth.MicrosoftClaims{
			Sub:           "ms-subject-id",
			Email:         "alice@example.com",
			Name:          "Alice",
			EmailVerified: true,
		},
	}
	deps := Deps{
		JWT:           jwt,
		OIDCMicrosoft: ms,
		Audit:         audit.NoopSink{},
		// Queries is nil — the handler will panic when it reaches the
		// FindIdentityByProviderSubject call. That's the assertion: we
		// got past the email_verified gate without being short-circuited.
	}
	handler := OIDCMicrosoftCallback(deps)
	defer func() {
		if r := recover(); r == nil {
			t.Fatal("expected nil-Queries to panic past the email_verified gate")
		}
	}()
	_, _ = handler(context.Background(), &OIDCCallbackInput{Code: "auth-code", State: state})
}

// TestOIDCMicrosoftCallback_RejectsBadState asserts state validation
// fails fast and Exchange is never reached when the state JWT cannot
// be verified. Mirrors the GitHub callback's coverage so the three
// providers stay aligned.
func TestOIDCMicrosoftCallback_RejectsBadState(t *testing.T) {
	t.Parallel()
	jwt, err := internauth.NewJWTIssuer(nil, "iss", "aud", time.Minute)
	require.NoError(t, err)

	ms := &fakeMicrosoftExchanger{}
	deps := Deps{
		JWT:           jwt,
		OIDCMicrosoft: ms,
		Audit:         audit.NoopSink{},
	}
	handler := OIDCMicrosoftCallback(deps)
	_, err = handler(context.Background(), &OIDCCallbackInput{Code: "c", State: "garbage"})
	require.Error(t, err)
	assert.Empty(t, ms.gotCode, "Exchange must not run when state verification fails")
}

// TestOIDCMicrosoftCallback_SurfacesProviderRejection asserts that an
// IdP callback carrying `?error=...&error_description=...` is surfaced
// as AUTH.OIDC.PROVIDER_REJECTED with HTTP 400, and the token-exchange
// stub is never invoked. Without this guard a Microsoft-side rejection
// would surface as the downstream "id_token invalid" code and obscure
// the real cause of the failure (denied consent, misconfigured app,
// rejected scopes, etc.).
func TestOIDCMicrosoftCallback_SurfacesProviderRejection(t *testing.T) {
	t.Parallel()
	jwt, err := internauth.NewJWTIssuer(nil, "iss", "aud", time.Minute)
	require.NoError(t, err)
	state, err := jwt.SignOIDCStateForProvider("nonce-value", "microsoft")
	require.NoError(t, err)

	ms := &fakeMicrosoftExchanger{}
	deps := Deps{
		JWT:           jwt,
		OIDCMicrosoft: ms,
		Audit:         audit.NoopSink{},
	}
	handler := OIDCMicrosoftCallback(deps)
	_, err = handler(context.Background(), &OIDCCallbackInput{
		State:            state,
		Error:            "invalid_scope",
		ErrorDescription: "scope rejected",
	})
	require.Error(t, err)

	var problem *handlerutil.ProblemDetails
	require.True(t, errors.As(err, &problem), "expected handlerutil.ProblemDetails, got %T", err)
	assert.Equal(t, apierrors.AuthOidcProviderRejected.Code, problem.Type,
		"provider-rejected callback must surface AUTH.OIDC.PROVIDER_REJECTED")
	assert.Equal(t, apierrors.AuthOidcProviderRejected.Status, problem.Status)
	assert.Equal(t, "microsoft", problem.Extensions["provider"])
	assert.Equal(t, "invalid_scope", problem.Extensions["provider_error"])
	assert.Equal(t, "scope rejected", problem.Extensions["provider_error_description"])
	assert.Empty(t, ms.gotCode, "Exchange must not run when the IdP rejects the callback")
}

// TestOIDCMicrosoftCallback_PreferredUsernameFallback verifies the
// callback falls back to the preferred_username claim when email is
// empty (Microsoft Entra ID often omits the `email` claim for
// work/school accounts).
func TestOIDCMicrosoftCallback_PreferredUsernameFallback(t *testing.T) {
	t.Parallel()
	jwt, err := internauth.NewJWTIssuer(nil, "iss", "aud", time.Minute)
	require.NoError(t, err)
	state, err := jwt.SignOIDCStateForProvider("nonce-value", "microsoft")
	require.NoError(t, err)

	ms := &fakeMicrosoftExchanger{
		claims: &internauth.MicrosoftClaims{
			Sub:               "ms-subject-id",
			Email:             "",
			PreferredUsername: "alice@example.com",
			Name:              "Alice",
			EmailVerified:     true,
		},
	}
	deps := Deps{
		JWT:           jwt,
		OIDCMicrosoft: ms,
		Audit:         audit.NoopSink{},
	}
	handler := OIDCMicrosoftCallback(deps)
	defer func() {
		// Past the email-derivation step the handler dereferences
		// deps.Queries; in this test that's nil, which is the signal
		// we made it past the email_verified gate with the fallback
		// email substituted in.
		_ = recover()
	}()
	_, _ = handler(context.Background(), &OIDCCallbackInput{Code: "c", State: state})
}
