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
	"strconv"
	"strings"
	"time"
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

// VerifySignature reports whether the v0 Slack signature header matches
// the HMAC-SHA256 of (timestamp || body) under secret. The comparison is
// constant-time and the timestamp must be within [MaxClockSkew] of now.
func VerifySignature(body []byte, header, timestamp, secret string, now time.Time) bool {
	if secret == "" || header == "" || timestamp == "" {
		return false
	}
	if !strings.HasPrefix(header, "v0=") {
		return false
	}
	ts, err := strconv.ParseInt(timestamp, 10, 64)
	if err != nil {
		return false
	}
	delta := now.Unix() - ts
	if delta < 0 {
		delta = -delta
	}
	if time.Duration(delta)*time.Second > MaxClockSkew {
		return false
	}
	expected := Sign(body, timestamp, secret)
	return hmac.Equal([]byte(expected), []byte(header))
}
