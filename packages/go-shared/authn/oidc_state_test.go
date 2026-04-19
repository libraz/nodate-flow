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
