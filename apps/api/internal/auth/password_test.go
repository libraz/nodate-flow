package auth

import "testing"

func TestPasswordHashVerify(t *testing.T) {
	t.Parallel()
	hash, err := HashPassword("correct horse battery staple")
	if err != nil {
		t.Fatalf("hash: %v", err)
	}
	// Encoded form is 97 chars with the chosen parameters.
	if len(hash) > 100 {
		t.Fatalf("encoded hash unexpectedly long: len=%d", len(hash))
	}
	ok, err := VerifyPassword(hash, "correct horse battery staple")
	if err != nil || !ok {
		t.Fatalf("verify good: ok=%v err=%v", ok, err)
	}
	bad, err := VerifyPassword(hash, "wrong")
	if err != nil {
		t.Fatalf("verify bad err: %v", err)
	}
	if bad {
		t.Fatal("verify wrong password unexpectedly succeeded")
	}
}

func TestPasswordVerifyInvalidEncoding(t *testing.T) {
	t.Parallel()
	if _, err := VerifyPassword("not-a-hash", "x"); err == nil {
		t.Fatal("expected error for malformed encoded hash")
	}
}
