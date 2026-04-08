package slack

import (
	"strconv"
	"testing"
	"time"
)

func TestVerifySignatureRoundTrip(t *testing.T) {
	body := []byte(`{"event":{"type":"message"}}`)
	secret := "shh"
	now := time.Now()
	ts := strconv.FormatInt(now.Unix(), 10)
	sig := Sign(body, ts, secret)
	if !VerifySignature(body, sig, ts, secret, now) {
		t.Fatalf("expected signature to verify")
	}
}

func TestVerifySignatureTamper(t *testing.T) {
	body := []byte(`{"a":1}`)
	secret := "shh"
	now := time.Now()
	ts := strconv.FormatInt(now.Unix(), 10)
	sig := Sign(body, ts, secret)

	if VerifySignature([]byte(`{"a":2}`), sig, ts, secret, now) {
		t.Fatalf("tampered body must not verify")
	}
	if VerifySignature(body, sig, ts, "other", now) {
		t.Fatalf("wrong secret must not verify")
	}
	old := strconv.FormatInt(now.Add(-10*time.Minute).Unix(), 10)
	oldSig := Sign(body, old, secret)
	if VerifySignature(body, oldSig, old, secret, now) {
		t.Fatalf("stale timestamp must not verify")
	}
	if VerifySignature(body, "v1=abc", ts, secret, now) {
		t.Fatalf("wrong scheme must not verify")
	}
}
