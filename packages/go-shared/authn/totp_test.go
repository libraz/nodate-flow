package authn

import (
	"encoding/base32"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestGenerateTotpSecret(t *testing.T) {
	t.Parallel()

	secret, err := GenerateTotpSecret()
	require.NoError(t, err)
	assert.Len(t, secret, 20, "TOTP secret must be 20 bytes")

	// Verify the secret is valid base32-encodable.
	encoded := base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString(secret)
	assert.NotEmpty(t, encoded)

	// Two calls should produce different secrets.
	secret2, err := GenerateTotpSecret()
	require.NoError(t, err)
	assert.NotEqual(t, secret, secret2, "consecutive secrets must differ")
}

func TestTotpOtpauthURL(t *testing.T) {
	t.Parallel()

	secret := []byte("12345678901234567890") // 20 bytes
	url := TotpOtpauthURL("nodate-flow", "user@example.com", secret)
	assert.Contains(t, url, "otpauth://totp/")
	assert.Contains(t, url, "nodate-flow")
	assert.Contains(t, url, "user@example.com")
	assert.Contains(t, url, "algorithm=SHA1")
	assert.Contains(t, url, "digits=6")
	assert.Contains(t, url, "period=30")
}

func TestVerifyTotp_ValidCode(t *testing.T) {
	t.Parallel()

	secret, err := GenerateTotpSecret()
	require.NoError(t, err)

	now := time.Now()
	// Generate a valid code using the internal totpAt function.
	code := totpAt(secret, now)
	assert.Len(t, code, 6)

	ok := VerifyTotp(secret, code, now)
	assert.True(t, ok, "valid TOTP code should pass verification")
}

func TestVerifyTotp_InvalidCode(t *testing.T) {
	t.Parallel()

	secret, err := GenerateTotpSecret()
	require.NoError(t, err)

	tests := []struct {
		name string
		code string
	}{
		{"wrong code", "000000"},
		{"too short", "12345"},
		{"too long", "1234567"},
		{"empty", ""},
		{"letters", "abcdef"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			// Use a fixed time far in the past to avoid accidental match with "000000".
			ok := VerifyTotp(secret, tc.code, time.Date(2000, 1, 1, 0, 0, 0, 0, time.UTC))
			assert.False(t, ok, "invalid TOTP code %q should fail verification", tc.code)
		})
	}
}

func TestVerifyTotp_SkewWindow(t *testing.T) {
	t.Parallel()

	secret, err := GenerateTotpSecret()
	require.NoError(t, err)

	now := time.Now()
	// Code from one period ago should still be valid (skew=1).
	codePrev := totpAt(secret, now.Add(-totpPeriod*time.Second))
	assert.True(t, VerifyTotp(secret, codePrev, now), "code from previous window should be accepted")

	// Code from one period ahead should still be valid (skew=1).
	codeNext := totpAt(secret, now.Add(totpPeriod*time.Second))
	assert.True(t, VerifyTotp(secret, codeNext, now), "code from next window should be accepted")

	// Code from two periods ago should be rejected (skew=1 means only +/-1).
	codeFar := totpAt(secret, now.Add(-2*totpPeriod*time.Second))
	assert.False(t, VerifyTotp(secret, codeFar, now), "code from two periods ago should be rejected")
}
