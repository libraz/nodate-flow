// Package google contains helpers for ingesting Google Drive / Docs
// push notifications. Drive does not sign payloads with an
// HMAC; instead each channel is registered with a unique
// X-Goog-Channel-Token that the receiver checks against its store.
package google

// HeaderChannelID is the Google-supplied subscription channel id
// (matches what we passed in `id` at watch time).
const HeaderChannelID = "X-Goog-Channel-ID"

// HeaderChannelToken is the per-channel shared secret echoed back
// with every push so the receiver can authenticate the source
// without inspecting the body.
const HeaderChannelToken = "X-Goog-Channel-Token" //#nosec G101 -- HTTP header name, not a credential value

// HeaderResourceState is the change kind: "sync" / "add" / "update"
// / "remove" / "trash" / "untrash".
const HeaderResourceState = "X-Goog-Resource-State"

// VerifyChannelToken returns true when the supplied header value
// matches the configured per-channel secret using a constant-time
// compare. Empty configured secret rejects everything.
func VerifyChannelToken(header, configured string) bool {
	if configured == "" || header == "" {
		return false
	}
	if len(header) != len(configured) {
		return false
	}
	var d byte
	for i := 0; i < len(header); i++ {
		d |= header[i] ^ configured[i]
	}
	return d == 0
}

// NormalizeEventKind maps the X-Goog-Resource-State header to a
// canonical signals.kind matching the github / slack shape.
func NormalizeEventKind(resourceState string) string {
	if resourceState == "" {
		return "unknown"
	}
	return "drive." + resourceState
}
