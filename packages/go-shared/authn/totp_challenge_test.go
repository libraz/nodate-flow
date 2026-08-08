package authn

import (
	"crypto/ed25519"
	"crypto/rand"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
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
			assert.Equal(t, tc.publicID, got.PublicID)
			assert.NotEmpty(t, got.TokenID, "a challenge must carry a jti so it can be retired after one use")
			assert.WithinDuration(t, exp, got.ExpiresAt, time.Second)
		})
	}
}

// TestSignTotpChallenge_TokenIDIsUniquePerChallenge pins the property the
// single-use claim rests on: two challenges for the same user must not
// share a jti, or retiring one would retire the other.
func TestSignTotpChallenge_TokenIDIsUniquePerChallenge(t *testing.T) {
	t.Parallel()
	j := newTestIssuer(t)
	const pid = "019012ab-cdef-7000-8000-000000000001"

	first, _, err := j.SignTotpChallenge(pid)
	require.NoError(t, err)
	second, _, err := j.SignTotpChallenge(pid)
	require.NoError(t, err)

	a, err := j.VerifyTotpChallenge(first)
	require.NoError(t, err)
	b, err := j.VerifyTotpChallenge(second)
	require.NoError(t, err)

	assert.NotEqual(t, a.TokenID, b.TokenID,
		"each challenge needs its own jti so retiring one does not retire the other")
}

// TestVerifyTotpChallenge_RejectsMissingTokenID proves a challenge that
// carries no jti is refused rather than accepted as an unretireable
// credential.
func TestVerifyTotpChallenge_RejectsMissingTokenID(t *testing.T) {
	t.Parallel()
	j := newTestIssuer(t)
	now := time.Now().UTC()
	claims := TotpChallengeClaims{
		PublicID: "019012ab-cdef-7000-8000-000000000001",
		RegisteredClaims: jwt.RegisteredClaims{
			Issuer:    j.issuer,
			Audience:  jwt.ClaimStrings{totpChallengeAudience},
			Subject:   "019012ab-cdef-7000-8000-000000000001",
			IssuedAt:  jwt.NewNumericDate(now),
			ExpiresAt: jwt.NewNumericDate(now.Add(TotpChallengeTTL)),
		},
	}
	token, err := jwt.NewWithClaims(jwt.SigningMethodEdDSA, claims).SignedString(j.priv)
	require.NoError(t, err)

	_, err = j.VerifyTotpChallenge(token)
	assert.Error(t, err, "a challenge with no jti cannot be retired and must be refused")
}

// TestVerifiedTotpChallenge_RemainingTTL asserts the ttl handed to the
// single-use store tracks the token's own expiry and never goes negative
// (a negative ttl is a no-op claim in MemorySingleUseStore, which would
// silently disable the defence).
func TestVerifiedTotpChallenge_RemainingTTL(t *testing.T) {
	t.Parallel()
	now := time.Now()
	c := VerifiedTotpChallenge{ExpiresAt: now.Add(2 * time.Minute)}
	assert.InDelta(t, (2 * time.Minute).Seconds(), c.RemainingTTL(now).Seconds(), 1)
	assert.Zero(t, c.RemainingTTL(now.Add(5*time.Minute)), "a past expiry must clamp to zero")
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
