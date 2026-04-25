// Package slack contains helpers for verifying inbound Slack webhook
// requests using the v0 signing-secret scheme:
//
//	base = "v0:" + timestamp + ":" + body
//	sig  = "v0=" + hex(HMAC_SHA256(secret, base))
package slack

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"strconv"
	"strings"
	"time"
)

// Sentinel errors returned by VerifySignature so callers can map a single
// failure to a specific public error code.
var (
	// ErrSignatureMissing is returned when the signing secret, signature
	// header, or timestamp header is absent.
	ErrSignatureMissing = errors.New("slack: signature header missing")

	// ErrSignatureMalformed is returned when the signature header does not
	// start with "v0=" or the timestamp header is not a valid integer.
	ErrSignatureMalformed = errors.New("slack: signature malformed")

	// ErrTimestampExpired is returned when the request timestamp drifts
	// beyond MaxClockSkew from the local clock.
	ErrTimestampExpired = errors.New("slack: timestamp outside clock skew")

	// ErrSignatureMismatch is returned when the HMAC computed from the
	// body and signing secret does not match the provided signature.
	ErrSignatureMismatch = errors.New("slack: signature mismatch")
)

// SignatureHeader is the canonical HTTP header Slack uses to ship the
// v0 HMAC-SHA256 of the webhook body.
const SignatureHeader = "X-Slack-Signature"

// TimestampHeader is the canonical HTTP header Slack uses to ship the
// request timestamp (Unix seconds, as a string).
const TimestampHeader = "X-Slack-Request-Timestamp"

// MaxClockSkew is the maximum drift permitted between the request
// timestamp and the local clock. Slack itself recommends 5 minutes.
const MaxClockSkew = 5 * time.Minute

// Sign computes the canonical "v0=<hex>" signature value for the given
// body / timestamp / secret triple. It is exported primarily for
// round-trip tests.
func Sign(body []byte, timestamp, secret string) string {
	base := "v0:" + timestamp + ":"
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(base))
	mac.Write(body)
	return "v0=" + hex.EncodeToString(mac.Sum(nil))
}

// VerifySignature returns nil when the v0 Slack signature header matches
// the HMAC-SHA256 of (timestamp || body) under secret. The comparison is
// constant-time and the timestamp must be within [MaxClockSkew] of now.
// On failure it returns one of [ErrSignatureMissing],
// [ErrSignatureMalformed], [ErrTimestampExpired], or
// [ErrSignatureMismatch] so callers can map each cause to a specific
// public error code.
func VerifySignature(body []byte, header, timestamp, secret string, now time.Time) error {
	if secret == "" || header == "" || timestamp == "" {
		return ErrSignatureMissing
	}
	if !strings.HasPrefix(header, "v0=") {
		return ErrSignatureMalformed
	}
	ts, err := strconv.ParseInt(timestamp, 10, 64)
	if err != nil {
		return ErrSignatureMalformed
	}
	delta := now.Unix() - ts
	if delta < 0 {
		delta = -delta
	}
	if time.Duration(delta)*time.Second > MaxClockSkew {
		return ErrTimestampExpired
	}
	expected := Sign(body, timestamp, secret)
	if !hmac.Equal([]byte(expected), []byte(header)) {
		return ErrSignatureMismatch
	}
	return nil
}
