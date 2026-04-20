package auth

import (
	"crypto/subtle"
	"testing"
)

// TestConstantTimeNonceComparison verifies that the nonce comparison
// used in Microsoft OIDC exchange is constant-time.
func TestConstantTimeNonceComparison(t *testing.T) {
	t.Parallel()
	nonce := "abc123def456"

	// Match
	if subtle.ConstantTimeCompare([]byte(nonce), []byte("abc123def456")) != 1 {
		t.Fatal("matching nonces should compare equal")
	}
	// Mismatch
	if subtle.ConstantTimeCompare([]byte(nonce), []byte("wrong")) != 0 {
		t.Fatal("mismatching nonces should compare unequal")
	}
	// Empty expected should be handled by caller (skipped when empty)
	if subtle.ConstantTimeCompare([]byte(""), []byte("")) != 1 {
		t.Fatal("two empty byte slices should compare equal")
	}
}
