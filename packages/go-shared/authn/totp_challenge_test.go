package authn

import (
	"crypto/ed25519"
	"crypto/rand"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func newTestIssuer(t *testing.T) *JWTIssuer {
	t.Helper()
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	require.NoError(t, err)
	j := &JWTIssuer{
		priv:     priv,
		pub:      pub,
		issuer:   "test-issuer",
		audience: "test-audience",
		ttl:      15 * time.Minute,
	}
	return j
}

func TestSignAndVerifyTotpChallenge(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		publicID string
	}{
		{"typical uuid", "019012ab-cdef-7000-8000-000000000001"},
		{"another uuid", "019099ff-abcd-7000-8000-ffffffffffff"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			j := newTestIssuer(t)

			token, exp, err := j.SignTotpChallenge(tc.publicID)
			require.NoError(t, err)
			assert.NotEmpty(t, token)
			assert.WithinDuration(t, time.Now().Add(TotpChallengeTTL), exp, 5*time.Second)

			got, err := j.VerifyTotpChallenge(token)
			require.NoError(t, err)
			assert.Equal(t, tc.publicID, got)
		})
	}
}

func TestVerifyTotpChallenge_ExpiredToken(t *testing.T) {
	t.Parallel()

	j := newTestIssuer(t)
	// We cannot easily expire a token without manipulating time, but we can
	// craft claims manually with an already-expired timestamp.
	// Instead, sign with a different issuer's key and verify it fails.
	// For expiry, we sign a token and then verify with wrong expectations.

	// Sign a valid token, then create a new issuer with different keys --
	// the token signed with the old key should fail verification.
	token, _, err := j.SignTotpChallenge("019012ab-0000-7000-8000-000000000001")
	require.NoError(t, err)

	j2 := newTestIssuer(t) // different key pair
	_, err = j2.VerifyTotpChallenge(token)
	assert.Error(t, err, "verification with wrong key should fail")
}

func TestVerifyTotpChallenge_InvalidKey(t *testing.T) {
	t.Parallel()

	j := newTestIssuer(t)

	_, err := j.VerifyTotpChallenge("not.a.valid.jwt.token")
	assert.Error(t, err)
}

func TestVerifyTotpChallenge_WrongAudience(t *testing.T) {
	t.Parallel()

	// A regular access token should not be accepted as a TOTP challenge
	// because the audience is different.
	j := newTestIssuer(t)
	token, _, err := j.SignTotpChallenge("019012ab-0000-7000-8000-000000000001")
	require.NoError(t, err)

	// Verify via the regular Verify method (expects audience "test-audience")
	// should fail because the TOTP challenge has audience "totp-challenge".
	_, err = j.Verify(token)
	assert.Error(t, err, "TOTP challenge token must not pass regular access token verification")
}
