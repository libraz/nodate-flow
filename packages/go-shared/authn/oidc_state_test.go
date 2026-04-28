package authn

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSignAndVerifyOIDCState(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		nonce string
	}{
		{"simple nonce", "abc123"},
		{"uuid nonce", "019012ab-cdef-7000-8000-000000000001"},
		{"long nonce", "a-very-long-nonce-value-that-might-come-from-crypto-random-bytes-abcdef1234567890"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			j := newTestIssuer(t)

			token, err := j.SignOIDCState(tc.nonce)
			require.NoError(t, err)
			assert.NotEmpty(t, token)

			got, err := j.VerifyOIDCState(token)
			require.NoError(t, err)
			assert.Equal(t, tc.nonce, got)
		})
	}
}

func TestVerifyOIDCState_WrongKey(t *testing.T) {
	t.Parallel()

	j1 := newTestIssuer(t)
	j2 := newTestIssuer(t)

	token, err := j1.SignOIDCState("test-nonce")
	require.NoError(t, err)

	_, err = j2.VerifyOIDCState(token)
	assert.Error(t, err, "verification with wrong key should fail")
}

func TestVerifyOIDCState_InvalidToken(t *testing.T) {
	t.Parallel()

	j := newTestIssuer(t)
	_, err := j.VerifyOIDCState("garbage-token")
	assert.Error(t, err)
}

func TestVerifyOIDCState_WrongAudience(t *testing.T) {
	t.Parallel()

	// An OIDC state token must not pass TOTP challenge verification.
	j := newTestIssuer(t)
	token, err := j.SignOIDCState("test-nonce")
	require.NoError(t, err)

	_, err = j.VerifyTotpChallenge(token)
	assert.Error(t, err, "OIDC state token must not pass TOTP challenge verification")
}

// TestVerifyOIDCStateForProvider_ProviderMismatch is the regression test
// for the cross-provider replay defence: a state JWT signed for "google"
// must not pass verification when checked against "github". Without
// this binding an attacker who phishes a victim through one provider
// could redeem the resulting state at another provider's callback.
func TestVerifyOIDCStateForProvider_ProviderMismatch(t *testing.T) {
	t.Parallel()
	j := newTestIssuer(t)

	token, err := j.SignOIDCStateForProvider("nonce-x", "google")
	require.NoError(t, err)

	_, err = j.VerifyOIDCStateForProvider(token, "github")
	assert.Error(t, err, "state signed for one provider must not verify against another")
}

// TestVerifyOIDCStateForProvider_AcceptsMatchingProvider is the
// happy-path counterpart: a state for the matching provider is accepted
// and the nonce is returned intact.
func TestVerifyOIDCStateForProvider_AcceptsMatchingProvider(t *testing.T) {
	t.Parallel()
	j := newTestIssuer(t)

	const nonce = "nonce-y"
	token, err := j.SignOIDCStateForProvider(nonce, "github")
	require.NoError(t, err)

	got, err := j.VerifyOIDCStateForProvider(token, "github")
	require.NoError(t, err)
	assert.Equal(t, nonce, got)
}

// TestVerifyOIDCStateForProvider_RejectsLegacyUnboundState ensures the
// laxer legacy [SignOIDCState] tokens (which omit the provider claim)
// are rejected by the provider-aware verifier. Otherwise an attacker
// could craft a "compatibility" token that bypasses the binding.
func TestVerifyOIDCStateForProvider_RejectsLegacyUnboundState(t *testing.T) {
	t.Parallel()
	j := newTestIssuer(t)

	token, err := j.SignOIDCState("nonce-z")
	require.NoError(t, err)

	_, err = j.VerifyOIDCStateForProvider(token, "google")
	assert.Error(t, err, "legacy state without provider claim must not satisfy provider-bound verification")
}
