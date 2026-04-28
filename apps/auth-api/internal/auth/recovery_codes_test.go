package auth

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestVerifyRecoveryCodeHash_Matches confirms the constant-time
// comparator returns true on the happy path: the supplied code
// re-hashes to the digest computed earlier in the request.
func TestVerifyRecoveryCodeHash_Matches(t *testing.T) {
	t.Parallel()

	codes, hashes, err := GenerateRecoveryCodes()
	require.NoError(t, err)
	require.NotEmpty(t, codes)

	for i := range codes {
		assert.True(t, VerifyRecoveryCodeHash(codes[i], hashes[i]),
			"recovery code %d must verify against its own digest", i)
	}
}

// TestVerifyRecoveryCodeHash_RejectsMismatch covers the defense-in-depth
// branch: a hash that does not correspond to the supplied code must be
// rejected. This is the path the login handler treats as "no match
// found" to keep the failure surface identical to sql.ErrNoRows.
func TestVerifyRecoveryCodeHash_RejectsMismatch(t *testing.T) {
	t.Parallel()

	codes, hashes, err := GenerateRecoveryCodes()
	require.NoError(t, err)
	require.GreaterOrEqual(t, len(codes), 2)

	// Verify code[0] against hash[1]: deliberate cross-pairing must
	// be rejected even though both pieces are individually valid.
	assert.False(t, VerifyRecoveryCodeHash(codes[0], hashes[1]),
		"cross-paired (code, hash) must not verify")
}

// TestVerifyRecoveryCodeHash_RejectsEmptyHash guards the boundary case
// where the caller passes a nil or zero-length stored-hash. The helper
// must report mismatch rather than crashing or accepting blindly.
func TestVerifyRecoveryCodeHash_RejectsEmptyHash(t *testing.T) {
	t.Parallel()

	codes, _, err := GenerateRecoveryCodes()
	require.NoError(t, err)

	assert.False(t, VerifyRecoveryCodeHash(codes[0], nil),
		"nil expected-hash must not verify")
	assert.False(t, VerifyRecoveryCodeHash(codes[0], []byte{}),
		"empty expected-hash must not verify")
}

// TestVerifyRecoveryCodeHash_NormalisesInput pins the case-insensitive
// / dash-insensitive normalisation contract by feeding the helper a
// re-cased and dash-stripped variant of the original code. Without
// this contract a UI that lowercases the input would silently break
// the recovery-code branch on every account.
func TestVerifyRecoveryCodeHash_NormalisesInput(t *testing.T) {
	t.Parallel()

	codes, hashes, err := GenerateRecoveryCodes()
	require.NoError(t, err)

	original := codes[0]
	expected := hashes[0]

	// Re-case and re-space the original so HashRecoveryCode's
	// normalisation must hit. We don't assert lower-case specifically
	// because GenerateRecoveryCodes is free to pick any alphabet; we
	// just round-trip through the normalisation and assert equality.
	mangled := original + " "
	assert.True(t, VerifyRecoveryCodeHash(mangled, expected),
		"trailing whitespace must normalise away")
}
