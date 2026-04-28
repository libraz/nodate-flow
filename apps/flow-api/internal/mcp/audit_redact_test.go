package mcp

import (
	"encoding/json"
	"strings"
	"testing"
)

// TestRedactAuditPayloadsScrubsSensitiveJSONKeys locks down the
// invariant that mcp_invocations.{arguments,result}_redacted_json must
// never carry raw values for the canonical sensitive JSON field names.
// The set of keys (api_key, token, password, secret, authorization,
// apikey) lives in packages/go-shared/logutil/redact.go and is shared
// with the slog redact handler.
func TestRedactAuditPayloadsScrubsSensitiveJSONKeys(t *testing.T) {
	t.Parallel()

	args := json.RawMessage(`{"name":"hello","api_key":"sk-live-superraw-1234567890"}`)
	result := json.RawMessage(`{"ok":true,"token":"ghp_aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","details":{"password":"hunter2","note":"keep"}}`)

	gotArgs, gotRes := redactAuditPayloads(args, result)

	for _, blob := range []json.RawMessage{gotArgs, gotRes} {
		s := string(blob)
		// Sensitive values must not appear in the audited bytes.
		for _, leak := range []string{"superraw-1234567890", "ghp_aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", "hunter2"} {
			if strings.Contains(s, leak) {
				t.Errorf("audited blob still contains raw secret %q in %s", leak, s)
			}
		}
		// The redaction marker must replace the value.
		if !strings.Contains(s, "[REDACTED") {
			t.Errorf("audited blob missing [REDACTED] marker: %s", s)
		}
	}

	// Non-sensitive fields must survive intact so audit rows remain
	// useful for debugging and observability.
	if !strings.Contains(string(gotArgs), `"name":"hello"`) {
		t.Errorf("non-sensitive arg field dropped: %s", gotArgs)
	}
	if !strings.Contains(string(gotRes), `"note":"keep"`) {
		t.Errorf("non-sensitive nested result field dropped: %s", gotRes)
	}
}

// TestRedactAuditPayloadsScrubsSecretPrefixes verifies that raw secret
// tokens carrying a registered prefix (sk-, mcp_, ghp_, AKIA, ...) are
// scrubbed even when they live under a non-sensitive JSON key. This is
// the second redaction pass performed by ai.RedactJSONFields.
func TestRedactAuditPayloadsScrubsSecretPrefixes(t *testing.T) {
	t.Parallel()

	// "credential" is NOT in the sensitive-key list, so the value is
	// only scrubbed because the SECRET PREFIX scanner catches it.
	in := json.RawMessage(`{"credential":"sk-ant-abcdefghijklmnopqrstuvwxyz0123456789"}`)
	_, gotRes := redactAuditPayloads(nil, in)
	s := string(gotRes)
	if strings.Contains(s, "abcdefghijklmnopqrstuvwxyz0123456789") {
		t.Errorf("prefix-scanner failed to scrub anthropic key body: %s", s)
	}
	if !strings.Contains(s, "[REDACTED:sk-ant-]") {
		t.Errorf("expected [REDACTED:sk-ant-] marker, got: %s", s)
	}
}

// TestRedactAuditPayloadsEmptyInputs verifies that nil / empty inputs
// are normalised to a literal "{}" so the audit row never carries an
// invalid JSON document.
func TestRedactAuditPayloadsEmptyInputs(t *testing.T) {
	t.Parallel()

	a, r := redactAuditPayloads(nil, nil)
	if string(a) != "{}" {
		t.Errorf("empty args should normalise to {}, got %q", a)
	}
	if string(r) != "{}" {
		t.Errorf("empty result should normalise to {}, got %q", r)
	}

	a, r = redactAuditPayloads(json.RawMessage(""), json.RawMessage(""))
	if string(a) != "{}" || string(r) != "{}" {
		t.Errorf("zero-length inputs should normalise to {}, got args=%q result=%q", a, r)
	}
}

// TestRedactAuditPayloadsIdempotent confirms that redacting an already-
// redacted blob is a no-op. The success-path call site in
// handleToolCall pre-redacts the result before passing it to audit;
// audit then redacts again as a defensive belt-and-braces measure. Both
// paths must agree on the final bytes.
func TestRedactAuditPayloadsIdempotent(t *testing.T) {
	t.Parallel()

	in := json.RawMessage(`{"api_key":"sk-live-zzz","name":"x"}`)
	_, once := redactAuditPayloads(nil, in)
	_, twice := redactAuditPayloads(nil, once)
	if string(once) != string(twice) {
		t.Errorf("redactAuditPayloads is not idempotent:\n once = %s\n twice = %s", once, twice)
	}
}

// TestRedactAuditPayloadsCompactsJSON verifies that pretty-printed
// input is compacted before redaction, matching the storage contract
// for mcp_invocations (compact JSON only).
func TestRedactAuditPayloadsCompactsJSON(t *testing.T) {
	t.Parallel()

	in := json.RawMessage("{\n  \"name\": \"x\",\n  \"token\": \"abc\"\n}")
	_, got := redactAuditPayloads(nil, in)
	s := string(got)
	if strings.Contains(s, "\n") || strings.Contains(s, "  ") {
		t.Errorf("expected compact JSON, got: %q", s)
	}
}
