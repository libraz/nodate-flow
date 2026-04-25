package slack

import (
	"errors"
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
	if err := VerifySignature(body, sig, ts, secret, now); err != nil {
		t.Fatalf("expected signature to verify, got %v", err)
	}
}

func TestVerifySignatureTamper(t *testing.T) {
	body := []byte(`{"a":1}`)
	secret := "shh"
	now := time.Now()
	ts := strconv.FormatInt(now.Unix(), 10)
	sig := Sign(body, ts, secret)

	if err := VerifySignature([]byte(`{"a":2}`), sig, ts, secret, now); !errors.Is(err, ErrSignatureMismatch) {
		t.Fatalf("tampered body must yield ErrSignatureMismatch, got %v", err)
	}
	if err := VerifySignature(body, sig, ts, "other", now); !errors.Is(err, ErrSignatureMismatch) {
		t.Fatalf("wrong secret must yield ErrSignatureMismatch, got %v", err)
	}
	old := strconv.FormatInt(now.Add(-10*time.Minute).Unix(), 10)
	oldSig := Sign(body, old, secret)
	if err := VerifySignature(body, oldSig, old, secret, now); !errors.Is(err, ErrTimestampExpired) {
		t.Fatalf("stale timestamp must yield ErrTimestampExpired, got %v", err)
	}
	if err := VerifySignature(body, "v1=abc", ts, secret, now); !errors.Is(err, ErrSignatureMalformed) {
		t.Fatalf("wrong scheme must yield ErrSignatureMalformed, got %v", err)
	}
}

func TestVerifySignatureMissing(t *testing.T) {
	body := []byte(`{}`)
	now := time.Now()
	ts := strconv.FormatInt(now.Unix(), 10)
	if err := VerifySignature(body, "", ts, "shh", now); !errors.Is(err, ErrSignatureMissing) {
		t.Fatalf("missing header must yield ErrSignatureMissing, got %v", err)
	}
	if err := VerifySignature(body, "v0=abc", "", "shh", now); !errors.Is(err, ErrSignatureMissing) {
		t.Fatalf("missing timestamp must yield ErrSignatureMissing, got %v", err)
	}
	if err := VerifySignature(body, "v0=abc", ts, "", now); !errors.Is(err, ErrSignatureMissing) {
		t.Fatalf("missing secret must yield ErrSignatureMissing, got %v", err)
	}
}

func TestVerifySignatureMalformedTimestamp(t *testing.T) {
	body := []byte(`{}`)
	now := time.Now()
	if err := VerifySignature(body, "v0=abc", "not-a-number", "shh", now); !errors.Is(err, ErrSignatureMalformed) {
		t.Fatalf("non-numeric timestamp must yield ErrSignatureMalformed, got %v", err)
	}
}
