package webhook

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"strings"
	"testing"
)

// verify is the receiver's half of the signing contract, written the way
// a subscriber would write it from the documented scheme. The tests below
// use it so they assert the contract rather than re-running the
// implementation's own arithmetic.
func verify(secret, timestamp string, body []byte, header string) bool {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte("v0:" + timestamp + ":"))
	mac.Write(body)
	return hmac.Equal([]byte("v0="+hex.EncodeToString(mac.Sum(nil))), []byte(header))
}

// TestSignatureCoversTheTimestamp is the replay guard. Signing the body
// alone let a captured delivery be replayed at a receiver forever: the
// signature stayed valid however old the request was, so a receiver had
// nothing to reject it on.
//
// The mutation that matters is dropping the timestamp back out of the
// signed base string. That leaves the header present but decorative, and
// the second assertion is what catches it: a replay that swaps in a
// fresh timestamp must fail verification.
func TestSignatureCoversTheTimestamp(t *testing.T) {
	t.Parallel()

	const secret = "shared-secret"
	body := []byte(`{"eventType":"task.created"}`)

	signed := sign(secret, "1700000000", body)
	if !verify(secret, "1700000000", body, signed) {
		t.Fatalf("a delivery must verify against the timestamp it was signed with")
	}

	// The replay: same body, same signature, a timestamp the receiver's
	// clock would accept.
	if verify(secret, "1900000000", body, signed) {
		t.Fatal("a captured delivery replayed under a fresh timestamp must not verify; " +
			"the signature does not cover the timestamp")
	}
}

// TestSignatureCoversTheBody keeps the older half of the contract from
// being lost while adding the timestamp to it.
func TestSignatureCoversTheBody(t *testing.T) {
	t.Parallel()

	const secret = "shared-secret"
	const ts = "1700000000"
	signed := sign(secret, ts, []byte(`{"eventType":"task.created"}`))

	if verify(secret, ts, []byte(`{"eventType":"task.deleted"}`), signed) {
		t.Fatal("a signature must not verify against a different body")
	}
	if verify("other-secret", ts, []byte(`{"eventType":"task.created"}`), signed) {
		t.Fatal("a signature must not verify under a different secret")
	}
}

// TestSignatureUsesTheV0Scheme pins the wire format. The inbound
// verifier in this service already requires "v0=<hex>" over
// "v0:<timestamp>:<body>", and a subscriber implementing our outbound
// side should not have to learn a second scheme.
func TestSignatureUsesTheV0Scheme(t *testing.T) {
	t.Parallel()

	signed := sign("shared-secret", "1700000000", []byte(`{}`))
	if !strings.HasPrefix(signed, "v0=") {
		t.Fatalf("signature header value = %q, want a v0= prefix", signed)
	}
	if got := len(strings.TrimPrefix(signed, "v0=")); got != sha256.Size*2 {
		t.Fatalf("signature hex length = %d, want %d (HMAC-SHA256)", got, sha256.Size*2)
	}
}
