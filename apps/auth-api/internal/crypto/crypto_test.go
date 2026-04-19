package crypto

import (
	"bytes"
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
