package crypto

import (
	"bytes"
	"errors"
	"testing"
)

func newTestCipher(t *testing.T) *Cipher {
	t.Helper()
	key := bytes.Repeat([]byte{0x42}, 32)
	c, err := New(key)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return c
}

func TestRoundTrip(t *testing.T) {
	c := newTestCipher(t)
	plain := []byte("sk-ant-this-is-not-a-real-key-0123456789")
	blob, err := c.Encrypt(plain)
	if err != nil {
		t.Fatalf("Encrypt: %v", err)
	}
	if bytes.Contains(blob, plain) {
		t.Fatalf("ciphertext leaks plaintext")
	}
	got, err := c.Decrypt(blob)
	if err != nil {
		t.Fatalf("Decrypt: %v", err)
	}
	if !bytes.Equal(got, plain) {
		t.Fatalf("round-trip mismatch: got %q want %q", got, plain)
	}
}

func TestWrongKeyFails(t *testing.T) {
	c1 := newTestCipher(t)
	other, _ := New(bytes.Repeat([]byte{0x11}, 32))
	blob, _ := c1.Encrypt([]byte("payload"))
	if _, err := other.Decrypt(blob); err == nil {
		t.Fatalf("expected decrypt with wrong key to fail")
	}
}

func TestTamperedCiphertextFails(t *testing.T) {
	c := newTestCipher(t)
	blob, _ := c.Encrypt([]byte("payload"))
	blob[len(blob)-1] ^= 0xff
	if _, err := c.Decrypt(blob); err == nil {
		t.Fatalf("expected tampered ciphertext to fail")
	}
}

func TestMasterKeyLength(t *testing.T) {
	if _, err := New([]byte("short")); err == nil {
		t.Fatalf("expected length validation")
	}
}

// TestReencryptRotatesAllSecretKinds proves the key-rotation invariant for
// every kind of plaintext sealed under NF_SECRET_KEY: after Reencrypt, the
// blob opens under the NEW key and no longer opens under the old one. A
// rotation that misses any of these stores would permanently lock out MFA
// users and break OAuth integrations.
func TestReencryptRotatesAllSecretKinds(t *testing.T) {
	oldC := newTestCipher(t)
	newC, err := New(bytes.Repeat([]byte{0x99}, 32))
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	secrets := map[string][]byte{
		"aiProviderAPIKey":       []byte("sk-ant-this-is-not-a-real-key-0123456789"),
		"totpSecret":             []byte("JBSWY3DPEHPK3PXPJBSWY3DPEHPK3PXP"),
		"integrationAccessToken": []byte("gho_notARealGitHubToken0123456789abcdef"),
		"integrationRefreshToken": []byte(
			"1//notARealGoogleRefreshToken-0123456789abcdefghij"),
	}
	for name, plain := range secrets {
		t.Run(name, func(t *testing.T) {
			blob, err := oldC.Encrypt(plain)
			if err != nil {
				t.Fatalf("Encrypt: %v", err)
			}
			rotated, err := Reencrypt(oldC, newC, blob)
			if err != nil {
				t.Fatalf("Reencrypt: %v", err)
			}
			got, err := newC.Decrypt(rotated)
			if err != nil {
				t.Fatalf("Decrypt with new key: %v", err)
			}
			if !bytes.Equal(got, plain) {
				t.Fatalf("round-trip mismatch: got %q want %q", got, plain)
			}
			if _, err := oldC.Decrypt(rotated); err == nil {
				t.Fatalf("rotated blob still opens under the old key")
			}
		})
	}
}

// TestReencryptAlreadyRotated proves an interrupted rotation can be re-run:
// a blob already sealed with the new key yields ErrAlreadyRotated instead
// of a hard failure.
func TestReencryptAlreadyRotated(t *testing.T) {
	oldC := newTestCipher(t)
	newC, _ := New(bytes.Repeat([]byte{0x99}, 32))
	blob, err := newC.Encrypt([]byte("JBSWY3DPEHPK3PXP"))
	if err != nil {
		t.Fatalf("Encrypt: %v", err)
	}
	if _, err := Reencrypt(oldC, newC, blob); !errors.Is(err, ErrAlreadyRotated) {
		t.Fatalf("expected ErrAlreadyRotated, got %v", err)
	}
}

// TestReencryptUnknownKeyFails proves a blob sealed with an unrelated key
// is a hard error, not silently skipped as already-rotated.
func TestReencryptUnknownKeyFails(t *testing.T) {
	oldC := newTestCipher(t)
	newC, _ := New(bytes.Repeat([]byte{0x99}, 32))
	otherC, _ := New(bytes.Repeat([]byte{0x11}, 32))
	blob, _ := otherC.Encrypt([]byte("payload"))
	if _, err := Reencrypt(oldC, newC, blob); err == nil ||
		errors.Is(err, ErrAlreadyRotated) {
		t.Fatalf("expected hard failure for unknown key, got %v", err)
	}
}

func TestCanDecrypt(t *testing.T) {
	c := newTestCipher(t)
	other, _ := New(bytes.Repeat([]byte{0x11}, 32))
	blob, _ := c.Encrypt([]byte("payload"))
	if !c.CanDecrypt(blob) {
		t.Fatalf("expected CanDecrypt true for own blob")
	}
	if other.CanDecrypt(blob) {
		t.Fatalf("expected CanDecrypt false for foreign blob")
	}
}

func TestAPIKeyPrefixSuffix(t *testing.T) {
	sample := "sk-ant-AbCdEfGhIjKlMnOpQrStUv"
	if got := APIKeyPrefix(sample); got != "sk-ant-A" {
		t.Errorf("prefix: got %q", got)
	}
	if got := APIKeySuffix(sample); got != "StUv" {
		t.Errorf("suffix: got %q", got)
	}
	if got := APIKeyPrefix("abc"); got != "abc" {
		t.Errorf("short prefix: got %q", got)
	}
	if got := APIKeySuffix("abc"); got != "abc" {
		t.Errorf("short suffix: got %q", got)
	}
}
