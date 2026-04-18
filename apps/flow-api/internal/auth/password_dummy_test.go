package auth

import "testing"

func TestDummyHashIsValid(t *testing.T) {
	t.Parallel()
	h := DummyHash()
	if h == "" {
		t.Fatal("dummy hash is empty")
	}
	// The dummy hash must be verifiable (well-formed argon2id).
	ok, err := VerifyPassword(h, "nodate-shared-dummy-password-timing-equaliser")
	if err != nil {
		t.Fatalf("verify dummy hash: %v", err)
	}
	if !ok {
		t.Fatal("dummy hash did not verify against its own password")
	}
}

func TestDummyHashAlwaysFailsForArbitraryInput(t *testing.T) {
	t.Parallel()
	h := DummyHash()
	ok, err := VerifyPassword(h, "attacker-guess")
	if err != nil {
		t.Fatalf("verify: %v", err)
	}
	if ok {
		t.Fatal("dummy hash should never verify for an arbitrary password")
	}
}
