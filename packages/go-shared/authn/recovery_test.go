package authn

import (
	"bytes"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestGenerateRecoveryCodes_Count(t *testing.T) {
	t.Parallel()

	codes, hashes, err := GenerateRecoveryCodes()
	require.NoError(t, err)
	assert.Len(t, codes, 10, "should generate 10 recovery codes")
	assert.Len(t, hashes, 10, "should generate 10 hashes")
}

func TestGenerateRecoveryCodes_Format(t *testing.T) {
	t.Parallel()

	codes, _, err := GenerateRecoveryCodes()
	require.NoError(t, err)

	for i, code := range codes {
		// Each code is XXXX-XXXX-XXXX = 14 chars total.
		assert.Len(t, code, 14, "code %d should be 14 chars (got %q)", i, code)
		assert.Equal(t, byte('-'), code[4], "code %d missing first dash", i)
		assert.Equal(t, byte('-'), code[9], "code %d missing second dash", i)
	}
}

func TestGenerateRecoveryCodes_Unique(t *testing.T) {
	t.Parallel()

	codes, _, err := GenerateRecoveryCodes()
	require.NoError(t, err)

	seen := make(map[string]bool)
	for _, code := range codes {
		assert.False(t, seen[code], "duplicate recovery code: %s", code)
		seen[code] = true
	}
}

func TestGenerateRecoveryCodes_HashesMatchCodes(t *testing.T) {
	t.Parallel()

	codes, hashes, err := GenerateRecoveryCodes()
	require.NoError(t, err)

	for i, code := range codes {
		recomputed := HashRecoveryCode(code)
		assert.True(t, bytes.Equal(hashes[i], recomputed),
			"hash at index %d does not match recomputed hash for code %q", i, code)
	}
}

func TestHashRecoveryCode_Deterministic(t *testing.T) {
	t.Parallel()

	code := "ABCD-EFGH-JKMN"
	h1 := HashRecoveryCode(code)
	h2 := HashRecoveryCode(code)
	assert.True(t, bytes.Equal(h1, h2), "HashRecoveryCode should be deterministic")
	assert.Len(t, h1, 32, "SHA-256 hash should be 32 bytes")
}

func TestHashRecoveryCode_CaseInsensitive(t *testing.T) {
	t.Parallel()

	upper := HashRecoveryCode("ABCD-EFGH-JKMN")
	lower := HashRecoveryCode("abcd-efgh-jkmn")
	assert.True(t, bytes.Equal(upper, lower), "hash should be case-insensitive")
}

func TestHashRecoveryCode_IgnoresDashesAndSpaces(t *testing.T) {
	t.Parallel()

	withDashes := HashRecoveryCode("ABCD-EFGH-JKMN")
	withSpaces := HashRecoveryCode("ABCD EFGH JKMN")
	noPunct := HashRecoveryCode("ABCDEFGHJKMN")
	assert.True(t, bytes.Equal(withDashes, noPunct), "dashes should be stripped before hashing")
	assert.True(t, bytes.Equal(withSpaces, noPunct), "spaces should be stripped before hashing")
}
