package github

import "testing"

func TestVerifySignatureRoundTrip(t *testing.T) {
	body := []byte(`{"action":"opened"}`)
	secret := "shh"
	sig := Sign(body, secret)
	if !VerifySignature(body, sig, secret) {
		t.Fatalf("expected signature to verify")
	}
}

func TestVerifySignatureTamper(t *testing.T) {
	body := []byte(`{"action":"opened"}`)
	secret := "shh"
	sig := Sign(body, secret)
	if VerifySignature([]byte(`{"action":"closed"}`), sig, secret) {
		t.Fatalf("tampered body must not verify")
	}
	if VerifySignature(body, sig, "other") {
		t.Fatalf("wrong secret must not verify")
	}
	if VerifySignature(body, "", secret) {
		t.Fatalf("empty header must not verify")
	}
	if VerifySignature(body, "sha1=abc", secret) {
		t.Fatalf("wrong scheme must not verify")
	}
}
