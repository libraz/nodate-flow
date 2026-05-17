package signaljudge

import (
	"encoding/json"
	"strings"
	"testing"
)

// TestRedactJSONAccessToken asserts a top-level access_token value is
// replaced with [REDACTED] while neighbouring benign keys survive.
func TestRedactJSONAccessToken(t *testing.T) {
	t.Parallel()
	in := json.RawMessage(`{"access_token":"abcdef","user":"alice"}`)
	out := redactJSON(in)
	var parsed map[string]any
	if err := json.Unmarshal(out, &parsed); err != nil {
		t.Fatalf("redactJSON output not valid JSON: %v (%s)", err, string(out))
	}
	if parsed["access_token"] != redactionMarker {
		t.Fatalf("access_token = %v, want %q", parsed["access_token"], redactionMarker)
	}
	if parsed["user"] != "alice" {
		t.Fatalf("user = %v, want alice (benign keys must survive)", parsed["user"])
	}
}

// TestRedactJSONNestedKeys asserts the JSON walk reaches keys nested
// under maps and arrays.
func TestRedactJSONNestedKeys(t *testing.T) {
	t.Parallel()
	in := json.RawMessage(`{
		"outer": {
			"refresh_token": "rrrrr",
			"detail": {"client_secret": "ccccc", "ok": true}
		},
		"list": [
			{"token": "tttt"},
			{"benign": "value"}
		]
	}`)
	out := redactJSON(in)
	s := string(out)
	for _, want := range []string{"refresh_token", "client_secret", "token"} {
		// Each blocked key must still be present in the output (we
		// redact the value, not the key) and its value must be the
		// marker.
		if !strings.Contains(s, `"`+want+`":"`+redactionMarker+`"`) {
			t.Fatalf("expected %q to be redacted to %q in %s", want, redactionMarker, s)
		}
	}
	if !strings.Contains(s, `"benign":"value"`) {
		t.Fatalf("benign value should survive nested redaction: %s", s)
	}
	if !strings.Contains(s, `"ok":true`) {
		t.Fatalf("benign sibling under redacted parent should survive: %s", s)
	}
}

// TestRedactFreeFormBearer asserts a Bearer token in prose is replaced.
func TestRedactFreeFormBearer(t *testing.T) {
	t.Parallel()
	in := "When the user shows up, Bearer abcdef1234, deny the request."
	got := redactFreeFormText(in)
	if strings.Contains(got, "abcdef1234") {
		t.Fatalf("bearer token survived redaction: %q", got)
	}
	if !strings.Contains(got, redactionMarker) {
		t.Fatalf("redaction marker missing from %q", got)
	}
	if !strings.Contains(got, "When the user shows up,") {
		t.Fatalf("benign prose lost during redaction: %q", got)
	}
}

// TestRedactFreeFormBenignText asserts text without any secret prefix
// passes through byte-for-byte.
func TestRedactFreeFormBenignText(t *testing.T) {
	t.Parallel()
	in := "Standard policy: prefer noop when ambiguous. No tokens here."
	got := redactFreeFormText(in)
	if got != in {
		t.Fatalf("benign text mutated: got %q want %q", got, in)
	}
}

// TestRedactFreeFormEmpty asserts empty input is safe.
func TestRedactFreeFormEmpty(t *testing.T) {
	t.Parallel()
	if got := redactFreeFormText(""); got != "" {
		t.Fatalf("empty input mutated to %q", got)
	}
}

// TestRedactJSONEmpty asserts an empty payload is safe.
func TestRedactJSONEmpty(t *testing.T) {
	t.Parallel()
	if got := redactJSON(nil); len(got) != 0 {
		t.Fatalf("nil input mutated: %s", string(got))
	}
	if got := redactJSON(json.RawMessage("")); len(got) != 0 {
		t.Fatalf("empty input mutated: %s", string(got))
	}
}

// TestRedactJSONInvalidFallback asserts non-JSON bytes still get
// scrubbed via the substring path.
func TestRedactJSONInvalidFallback(t *testing.T) {
	t.Parallel()
	in := json.RawMessage("not json at all sk-abcdef trailing")
	out := redactJSON(in)
	if strings.Contains(string(out), "sk-abcdef") {
		t.Fatalf("invalid JSON path failed to redact: %s", string(out))
	}
	if !strings.Contains(string(out), redactionMarker) {
		t.Fatalf("invalid JSON path missing marker: %s", string(out))
	}
}

// TestRedactJSONStringValueBearer asserts a string VALUE inside a
// JSON object that contains a bearer token gets scrubbed even when
// its key is not on the blocklist.
func TestRedactJSONStringValueBearer(t *testing.T) {
	t.Parallel()
	in := json.RawMessage(`{"description":"call the API with Bearer abc123 attached"}`)
	out := redactJSON(in)
	if strings.Contains(string(out), "abc123") {
		t.Fatalf("nested string value bearer survived: %s", string(out))
	}
}

// TestRedactCaseInsensitiveKeys asserts the JSON walk matches keys
// regardless of case (Authorization vs authorization).
func TestRedactCaseInsensitiveKeys(t *testing.T) {
	t.Parallel()
	in := json.RawMessage(`{"Authorization":"Bearer xxx","API_KEY":"yyy"}`)
	out := redactJSON(in)
	s := string(out)
	if strings.Contains(s, `"xxx"`) || strings.Contains(s, `"yyy"`) {
		t.Fatalf("case-insensitive key match failed: %s", s)
	}
}
