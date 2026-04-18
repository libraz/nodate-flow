// Package github contains helpers for verifying inbound GitHub webhook
// requests. The signature scheme is HMAC-SHA256 over the raw request body
// with the shared secret configured on the GitHub side.
package github

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"strings"
)

// SignatureHeader is the canonical HTTP header GitHub uses to ship the
// HMAC-SHA256 of the webhook body.
const SignatureHeader = "X-Hub-Signature-256"

// EventHeader is the GitHub-supplied event kind (e.g. "pull_request").
const EventHeader = "X-GitHub-Event"

// Sign computes the canonical "sha256=<hex>" header value for body using
// the given shared secret. It is exported primarily for round-trip tests.
func Sign(body []byte, secret string) string {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(body)
	return "sha256=" + hex.EncodeToString(mac.Sum(nil))
}

// VerifySignature reports whether the value of the X-Hub-Signature-256
// header matches the HMAC-SHA256 of body computed with secret. The
// comparison is constant-time. An empty header or empty secret is rejected.
func VerifySignature(body []byte, header, secret string) bool {
	if secret == "" || header == "" {
		return false
	}
	if !strings.HasPrefix(header, "sha256=") {
		return false
	}
	expected := Sign(body, secret)
	return hmac.Equal([]byte(expected), []byte(header))
}
