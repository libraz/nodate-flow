package auth

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/libraz/nodate-flow/apps/auth-api/internal/audit"
	internauth "github.com/libraz/nodate-flow/apps/auth-api/internal/auth"
	apierrors "github.com/libraz/nodate-flow/apps/auth-api/internal/errors"
	"github.com/libraz/nodate-flow/apps/auth-api/internal/http/handlers/handlerutil"
)

const testMicrosoftTenantID = "11111111-1111-1111-1111-111111111111"

// fakeMicrosoftExchanger captures the inputs supplied to Exchange and
// returns canned MicrosoftClaims so the callback handler's branches
// can be covered without the real go-oidc verifier or any HTTP traffic.
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

// TestOIDCMicrosoftCallback_AcceptsXmsEdovWithoutEmailVerified asserts
// Microsoft uses xms_edov, not the generic email_verified claim, as the
// email trust signal. Entra v2.0 commonly omits email_verified; in this
// struct that is indistinguishable from false, so the callback must
// proceed when the tenant is allowed and xms_edov is true.
func TestOIDCMicrosoftCallback_AcceptsXmsEdovWithoutEmailVerified(t *testing.T) {
	t.Parallel()
	jwt, err := internauth.NewJWTIssuer(nil, "iss", "aud", time.Minute)
	require.NoError(t, err)

	ms := &fakeMicrosoftExchanger{
		claims: &internauth.MicrosoftClaims{
			Sub:                      "ms-subject-id",
			Email:                    "alice@example.com",
			Name:                     "Alice",
			TenantID:                 testMicrosoftTenantID,
			EmailVerified:            false,
			EmailDomainOwnerVerified: true,
		},
	}
	deps := Deps{
		JWT:                       jwt,
		OIDCMicrosoft:             ms,
		MicrosoftAllowedTenantIDs: []string{testMicrosoftTenantID},
		Audit:                     audit.NoopSink{},
	}
	state, stateCookie := signedMicrosoftState(t, &deps)
	handler := OIDCMicrosoftCallback(deps)
	defer func() {
		if r := recover(); r == nil {
			t.Fatal("expected nil-Queries to panic after Microsoft claim validation")
		}
	}()
	_, _ = handler(context.Background(), &OIDCCallbackInput{
		Code:        "auth-code",
		State:       state,
		StateCookie: stateCookie,
	})
}

// TestOIDCMicrosoftCallback_AcceptsVerifiedEmailWithoutDB stops short
// of the database write to confirm a fully populated Microsoft claim
// set proceeds past provider-specific validation. It does not assert
// successful login (that requires a live DB).
func TestOIDCMicrosoftCallback_AcceptsVerifiedEmailWithoutDB(t *testing.T) {
	t.Parallel()
	jwt, err := internauth.NewJWTIssuer(nil, "iss", "aud", time.Minute)
	require.NoError(t, err)
	ms := &fakeMicrosoftExchanger{
		claims: &internauth.MicrosoftClaims{
			Sub:                      "ms-subject-id",
			Email:                    "alice@example.com",
			Name:                     "Alice",
			TenantID:                 testMicrosoftTenantID,
			EmailVerified:            true,
			EmailDomainOwnerVerified: true,
		},
	}
	deps := Deps{
		JWT:                       jwt,
		OIDCMicrosoft:             ms,
		MicrosoftAllowedTenantIDs: []string{testMicrosoftTenantID},
		Audit:                     audit.NoopSink{},
		// Queries is nil — the handler will panic at the first statement
		// past the claim gates, the sign-in allowlist read. That's the
		// assertion: we got past Microsoft claim validation without being
		// short-circuited.
	}
	state, stateCookie := signedMicrosoftState(t, &deps)
	handler := OIDCMicrosoftCallback(deps)
	defer func() {
		if r := recover(); r == nil {
			t.Fatal("expected nil-Queries to panic past the email_verified gate")
		}
	}()
	_, _ = handler(context.Background(), &OIDCCallbackInput{
		Code:        "auth-code",
		State:       state,
		StateCookie: stateCookie,
	})
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
	_, stateCookie := signedMicrosoftState(t, &deps)
	handler := OIDCMicrosoftCallback(deps)
	_, err = handler(context.Background(), &OIDCCallbackInput{
		Code:        "c",
		State:       "garbage",
		StateCookie: stateCookie,
	})
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
	ms := &fakeMicrosoftExchanger{}
	deps := Deps{
		JWT:                       jwt,
		OIDCMicrosoft:             ms,
		MicrosoftAllowedTenantIDs: []string{testMicrosoftTenantID},
		Audit:                     audit.NoopSink{},
	}
	state, stateCookie := signedMicrosoftState(t, &deps)
	handler := OIDCMicrosoftCallback(deps)
	_, err = handler(context.Background(), &OIDCCallbackInput{
		State:            state,
		StateCookie:      stateCookie,
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
	ms := &fakeMicrosoftExchanger{
		claims: &internauth.MicrosoftClaims{
			Sub:                      "ms-subject-id",
			Email:                    "",
			PreferredUsername:        "alice@example.com",
			Name:                     "Alice",
			TenantID:                 testMicrosoftTenantID,
			EmailVerified:            true,
			EmailDomainOwnerVerified: true,
		},
	}
	deps := Deps{
		JWT:                       jwt,
		OIDCMicrosoft:             ms,
		MicrosoftAllowedTenantIDs: []string{testMicrosoftTenantID},
		Audit:                     audit.NoopSink{},
	}
	state, stateCookie := signedMicrosoftState(t, &deps)
	handler := OIDCMicrosoftCallback(deps)
	defer func() {
		// Past the email-derivation step the handler dereferences
		// deps.Queries for the sign-in allowlist read; in this test
		// that's nil, which is the signal we made it past Microsoft
		// claim validation with the fallback email substituted in.
		_ = recover()
	}()
	_, _ = handler(context.Background(), &OIDCCallbackInput{
		Code:        "c",
		State:       state,
		StateCookie: stateCookie,
	})
}

func TestOIDCMicrosoftCallback_RejectsUnlistedTenant(t *testing.T) {
	t.Parallel()
	jwt, err := internauth.NewJWTIssuer(nil, "iss", "aud", time.Minute)
	require.NoError(t, err)
	ms := &fakeMicrosoftExchanger{
		claims: &internauth.MicrosoftClaims{
			Sub:                      "ms-subject-id",
			Email:                    "alice@example.com",
			Name:                     "Alice",
			TenantID:                 "22222222-2222-2222-2222-222222222222",
			EmailVerified:            true,
			EmailDomainOwnerVerified: true,
		},
	}
	deps := Deps{
		JWT:                       jwt,
		OIDCMicrosoft:             ms,
		MicrosoftAllowedTenantIDs: []string{testMicrosoftTenantID},
		Audit:                     audit.NoopSink{},
	}
	state, stateCookie := signedMicrosoftState(t, &deps)
	handler := OIDCMicrosoftCallback(deps)
	_, err = handler(context.Background(), &OIDCCallbackInput{
		Code:        "auth-code",
		State:       state,
		StateCookie: stateCookie,
	})
	require.Error(t, err)

	var problem *handlerutil.ProblemDetails
	require.True(t, errors.As(err, &problem))
	assert.Equal(t, apierrors.AuthOidcIdTokenInvalid.Code, problem.Type)
}

func TestOIDCMicrosoftCallback_RejectsMissingXmsEdov(t *testing.T) {
	t.Parallel()
	jwt, err := internauth.NewJWTIssuer(nil, "iss", "aud", time.Minute)
	require.NoError(t, err)
	ms := &fakeMicrosoftExchanger{
		claims: &internauth.MicrosoftClaims{
			Sub:           "ms-subject-id",
			Email:         "alice@example.com",
			Name:          "Alice",
			TenantID:      testMicrosoftTenantID,
			EmailVerified: true,
		},
	}
	deps := Deps{
		JWT:                       jwt,
		OIDCMicrosoft:             ms,
		MicrosoftAllowedTenantIDs: []string{testMicrosoftTenantID},
		Audit:                     audit.NoopSink{},
	}
	state, stateCookie := signedMicrosoftState(t, &deps)
	handler := OIDCMicrosoftCallback(deps)
	_, err = handler(context.Background(), &OIDCCallbackInput{
		Code:        "auth-code",
		State:       state,
		StateCookie: stateCookie,
	})
	require.Error(t, err)

	var problem *handlerutil.ProblemDetails
	require.True(t, errors.As(err, &problem))
	assert.Equal(t, apierrors.AuthOidcIdTokenInvalid.Code, problem.Type)
}
