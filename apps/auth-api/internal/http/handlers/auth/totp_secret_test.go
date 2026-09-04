package auth

import (
	"bytes"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/libraz/nodate-flow/packages/go-shared/crypto"
)

// totpSecretCipher builds a Cipher over a fixed key for these tests.
func totpSecretCipher(t *testing.T) *crypto.Cipher {
	t.Helper()
	c, err := crypto.New(bytes.Repeat([]byte{0x24}, 32))
	require.NoError(t, err)
	return c
}

// TestWithTotpSecret_WipesThePlaintextAfterTheCallback is the property
// the callback shape exists for: once the code has been verified, the
// secret is no longer in the buffer it was decrypted into. A helper that
// returned the secret instead could not hold this — nothing would know
// when the caller was finished with it.
func TestWithTotpSecret_WipesThePlaintextAfterTheCallback(t *testing.T) {
	t.Parallel()
	c := totpSecretCipher(t)
	blob, err := c.Encrypt([]byte("JBSWY3DPEHPK3PXP"))
	require.NoError(t, err)

	// Held deliberately, which is what the doc tells callers not to do.
	// It is the only way to look at the buffer after the wipe.
	var held []byte
	require.NoError(t, withTotpSecret(c, string(blob), func(secret []byte) error {
		require.Equal(t, "JBSWY3DPEHPK3PXP", string(secret),
			"the callback must see the secret while it runs")
		held = secret
		return nil
	}))

	require.NotEmpty(t, held, "the callback must have been handed a secret to hold")
	assert.Equal(t, bytes.Repeat([]byte{0}, len(held)), held,
		"the TOTP secret is still readable after the callback returned")
}

// TestWithTotpSecret_WipesThePlaintextWhenTheCodeDoesNotMatch holds the
// same property on the path both call sites take most often in anger: a
// submitted code that does not verify, which the callback reports as an
// error. A wipe that only ran on success would leave the secret live for
// exactly the requests an attacker generates.
func TestWithTotpSecret_WipesThePlaintextWhenTheCodeDoesNotMatch(t *testing.T) {
	t.Parallel()
	c := totpSecretCipher(t)
	blob, err := c.Encrypt([]byte("JBSWY3DPEHPK3PXP"))
	require.NoError(t, err)

	mismatch := errors.New("code did not verify")
	var held []byte
	err = withTotpSecret(c, string(blob), func(secret []byte) error {
		held = secret
		return mismatch
	})
	require.ErrorIs(t, err, mismatch,
		"the callback's error must reach the caller unchanged")
	require.NotEmpty(t, held)
	assert.Equal(t, bytes.Repeat([]byte{0}, len(held)), held,
		"the TOTP secret survived a callback that reported a mismatch")
}

// TestWithTotpSecret_UnreadableSecretIsDistinguishable pins the split the
// call sites depend on: a secret that will not decrypt is a broken cipher
// (500 plus a high-severity audit entry), not a wrong code, and the
// callback must not run at all.
func TestWithTotpSecret_UnreadableSecretIsDistinguishable(t *testing.T) {
	t.Parallel()
	c := totpSecretCipher(t)
	other, err := crypto.New(bytes.Repeat([]byte{0x99}, 32))
	require.NoError(t, err)
	blob, err := other.Encrypt([]byte("JBSWY3DPEHPK3PXP"))
	require.NoError(t, err)

	err = withTotpSecret(c, string(blob), func([]byte) error {
		t.Fatal("the callback must not run when the secret cannot be decrypted")
		return nil
	})
	require.ErrorIs(t, err, errTotpSecretUnreadable)
}
