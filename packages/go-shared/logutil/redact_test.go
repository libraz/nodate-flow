package logutil

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"testing"
)

func newTestLogger(buf *bytes.Buffer) *slog.Logger {
	base := slog.NewJSONHandler(buf, &slog.HandlerOptions{Level: slog.LevelDebug})
	return slog.New(NewRedactHandler(base))
}

func TestRedactHandler_SensitiveKeysAreRedacted(t *testing.T) {
	t.Parallel()

	keys := []string{
		"authorization",
		"authorization_code",
		"api_key",
		"apikey",
		"client_secret",
		"code",
		"id_token",
		"password",
		"refresh_token",
		"secret",
		"token",
	}
	for _, key := range keys {
		t.Run(key, func(t *testing.T) {
			t.Parallel()

			var buf bytes.Buffer
			l := newTestLogger(&buf)
			l.Info("test", key, "some-value-that-should-be-hidden")

			var rec map[string]any
			if err := json.Unmarshal(buf.Bytes(), &rec); err != nil {
				t.Fatalf("invalid json output: %v: %s", err, buf.String())
			}
			got, ok := rec[key]
			if !ok {
				t.Fatalf("key %q not found in log output: %s", key, buf.String())
			}
			if got != "[REDACTED]" {
				t.Fatalf("key %q was not redacted: got %q, want [REDACTED]", key, got)
			}
			if strings.Contains(buf.String(), "some-value-that-should-be-hidden") {
				t.Fatalf("raw value leaked for key %q: %s", key, buf.String())
			}
		})
	}
}

func TestRedactHandler_CamelCaseSensitiveKeysAreRedacted(t *testing.T) {
	t.Parallel()

	keys := []string{"clientSecret", "idToken", "refreshToken", "authorizationCode"}
	for _, key := range keys {
		t.Run(key, func(t *testing.T) {
			t.Parallel()

			var buf bytes.Buffer
			l := newTestLogger(&buf)
			l.Info("test", key, "some-value-that-should-be-hidden")

			if strings.Contains(buf.String(), "some-value-that-should-be-hidden") {
				t.Fatalf("raw value leaked for key %q: %s", key, buf.String())
			}
			if !strings.Contains(buf.String(), `"`+key+`":"[REDACTED]"`) {
				t.Fatalf("key %q was not redacted: %s", key, buf.String())
			}
		})
	}
}

func TestRedactHandler_NormalKeysNotRedacted(t *testing.T) {
	t.Parallel()

	keys := []struct {
		key string
		val string
	}{
		{"user_id", "12345"},
		{"email", "test@example.com"},
		{"status", "active"},
		{"path", "/api/v1/tasks"},
		{"method", "GET"},
	}
	for _, tc := range keys {
		t.Run(tc.key, func(t *testing.T) {
			t.Parallel()

			var buf bytes.Buffer
			l := newTestLogger(&buf)
			l.Info("test", tc.key, tc.val)

			var rec map[string]any
			if err := json.Unmarshal(buf.Bytes(), &rec); err != nil {
				t.Fatalf("invalid json output: %v: %s", err, buf.String())
			}
			got, ok := rec[tc.key]
			if !ok {
				t.Fatalf("key %q not found in log output: %s", tc.key, buf.String())
			}
			if got != tc.val {
				t.Fatalf("key %q was incorrectly modified: got %q, want %q", tc.key, got, tc.val)
			}
		})
	}
}

func TestRedactHandler_PrefixedSecretsInValues(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name   string
		value  string
		prefix string
	}{
		{"sk-ant-prefix", "sk-ant-api03-abcdef123456", "sk-ant-"},
		{"sk-prefix", "sk-proj-abc123", "sk-"},
		{"ghp-prefix", "ghp_aB1cD2eF3gH4iJ5kL6mN7oP8qR9sT0", "ghp_"},
		{"xoxb-prefix", "xoxb-123456789-987654321-abc", "xoxb-"},
		{"github_pat-prefix", "github_pat_11AABCDE0deadbeef1234", "github_pat_"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			var buf bytes.Buffer
			l := newTestLogger(&buf)
			l.Info("secret found", "some_field", tc.value)

			out := buf.String()
			expected := "[REDACTED:" + tc.prefix + "]"
			if !strings.Contains(out, expected) {
				t.Fatalf("expected prefix redaction %q in output, got: %s", expected, out)
			}
			// The raw secret body after the prefix must not appear.
			afterPrefix := tc.value[len(tc.prefix):]
			if strings.Contains(out, afterPrefix) {
				t.Fatalf("raw secret body leaked in output: %s", out)
			}
		})
	}
}

func TestRedactHandler_NestedGroupAttributes(t *testing.T) {
	t.Parallel()

	var buf bytes.Buffer
	l := newTestLogger(&buf)
	l.LogAttrs(context.Background(), slog.LevelInfo, "nested test",
		slog.Group("request",
			slog.String("method", "POST"),
			slog.String("authorization", "Bearer secret-token-xyz"),
		),
	)

	out := buf.String()

	// The authorization key inside the group must be redacted.
	if !strings.Contains(out, `"authorization":"[REDACTED]"`) {
		t.Fatalf("nested authorization not redacted: %s", out)
	}

	// The method key inside the group must be preserved.
	if !strings.Contains(out, `"method":"POST"`) {
		t.Fatalf("nested normal key was incorrectly modified: %s", out)
	}
}

func TestRedactHandler_WithAttrs(t *testing.T) {
	t.Parallel()

	var buf bytes.Buffer
	l := newTestLogger(&buf).With("token", "anything-here")
	l.Info("msg")

	if !strings.Contains(buf.String(), `"token":"[REDACTED]"`) {
		t.Fatalf("WithAttrs did not redact token: %s", buf.String())
	}
}

func TestRedactHandler_WithGroup(t *testing.T) {
	t.Parallel()

	var buf bytes.Buffer
	l := newTestLogger(&buf)
	gl := l.WithGroup("auth")
	gl.Info("check", "password", "hunter2")

	out := buf.String()
	if !strings.Contains(out, `"password":"[REDACTED]"`) {
		t.Fatalf("WithGroup did not redact password: %s", out)
	}
	if strings.Contains(out, "hunter2") {
		t.Fatalf("raw password leaked: %s", out)
	}
}

func TestRedact_PlainStringWithPrefix(t *testing.T) {
	t.Parallel()

	input := "key is sk-ant-secret123 and another ghp_token456"
	got := Redact(input)

	if strings.Contains(got, "secret123") {
		t.Fatalf("sk-ant- secret not redacted: %s", got)
	}
	if strings.Contains(got, "token456") {
		t.Fatalf("ghp_ secret not redacted: %s", got)
	}
	if !strings.Contains(got, "[REDACTED:sk-ant-]") {
		t.Fatalf("missing sk-ant- redaction marker: %s", got)
	}
	if !strings.Contains(got, "[REDACTED:ghp_]") {
		t.Fatalf("missing ghp_ redaction marker: %s", got)
	}
}

func TestRedact_NoPrefix_Unchanged(t *testing.T) {
	t.Parallel()

	input := "this is a normal log line with no secrets"
	got := Redact(input)
	if got != input {
		t.Fatalf("Redact modified non-secret string: got %q, want %q", got, input)
	}
}

func TestRedact_EmptyString(t *testing.T) {
	t.Parallel()

	if got := Redact(""); got != "" {
		t.Fatalf("Redact of empty string should be empty, got %q", got)
	}
}

func TestRedact_NewSecretPrefixes(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name   string
		value  string
		prefix string
	}{
		{"AKIA AWS key", "AKIAIOSFODNN7EXAMPLE", "AKIA"},
		{"glpat GitLab PAT", "glpat-abc123def456ghi789", "glpat-"},
		{"SG. SendGrid", "SG.abc123def456.ghi789jkl012mno345pqr678stu901vwx", "SG."},
		{"rk_live_ Stripe restricted", "rk_live_" + "abc123def456ghi789jkl012", "rk_live_"},
		{"sk_live_ Stripe secret", "sk_live_" + "abc123def456ghi789jkl012", "sk_live_"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			got := Redact(tc.value)
			expected := "[REDACTED:" + tc.prefix + "]"
			if !strings.Contains(got, expected) {
				t.Fatalf("expected prefix redaction %q in output, got: %s", expected, got)
			}
			// The raw secret body after the prefix must not appear.
			afterPrefix := tc.value[len(tc.prefix):]
			if strings.Contains(got, afterPrefix) {
				t.Fatalf("raw secret body leaked in output: %s", got)
			}
		})
	}
}

// TestRedact_PrefixOnlyMatchesAtAWordStart covers both directions of the
// boundary rule: a prefix that begins a word is a secret, a prefix that
// continues one is ordinary prose.
//
// The must-redact half enumerates the byte that realistically precedes a
// credential in the wild — header, JSON, env dump, bracketed, newline,
// start of string — because the boundary rule is the only thing standing
// between those shapes and a leak.
func TestRedact_PrefixOnlyMatchesAtAWordStart(t *testing.T) {
	t.Parallel()

	const antBody = "sk-ant-" + "api03exampletokenbody01" //#nosec G101 -- synthetic test fixture, never a real key

	cases := []struct {
		name       string
		input      string
		wantMarker string // "" means nothing may be redacted
		wantExact  string // asserted when wantMarker is empty
	}{
		// A prefix that starts a word is a secret.
		{
			name:       "bearer header",
			input:      "Authorization: Bearer " + antBody,
			wantMarker: "[REDACTED:sk-ant-]",
		},
		{
			name:       "json string value",
			input:      `{"apiKeyHint":"` + antBody + `"}`,
			wantMarker: "[REDACTED:sk-ant-]",
		},
		{
			name:       "env style assignment",
			input:      "ANTHROPIC_KEY=" + antBody,
			wantMarker: "[REDACTED:sk-ant-]",
		},
		{
			name:       "parenthesised",
			input:      "(" + antBody + ")",
			wantMarker: "[REDACTED:sk-ant-]",
		},
		{
			name:       "bracketed",
			input:      "[" + antBody + "]",
			wantMarker: "[REDACTED:sk-ant-]",
		},
		{
			name:       "after a newline",
			input:      "upstream said:\n" + antBody,
			wantMarker: "[REDACTED:sk-ant-]",
		},
		{
			name:       "start of string",
			input:      antBody,
			wantMarker: "[REDACTED:sk-ant-]",
		},
		{
			name:       "after a hyphen",
			input:      "x-api-key:" + antBody,
			wantMarker: "[REDACTED:sk-ant-]",
		},
		{
			name:       "adjacent to non-ascii prose",
			input:      "キーは" + antBody + "です",
			wantMarker: "[REDACTED:sk-ant-]",
		},
		{
			name:       "pat_ at a word start",
			input:      "token=pat_abcdef0123456789",
			wantMarker: "[REDACTED:pat_]",
		},
		{
			name:       "SG. at a word start",
			input:      "key SG.abc123def456.ghi789jkl012mno345",
			wantMarker: "[REDACTED:SG.]",
		},

		// A prefix in the middle of a word is prose, not a secret.
		{
			name:      "sk- inside task-filter",
			input:     "You are a task-filter compiler for nodate-flow.",
			wantExact: "You are a task-filter compiler for nodate-flow.",
		},
		{
			name:      "sk- inside risk-adjusted",
			input:     "risk-adjusted estimate",
			wantExact: "risk-adjusted estimate",
		},
		{
			name:      "sk- inside disk-usage",
			input:     "disk-usage report",
			wantExact: "disk-usage report",
		},
		{
			name:      "pat_ inside compat_",
			input:     "compat_mode enabled",
			wantExact: "compat_mode enabled",
		},
		{
			name:      "SG. inside MSG.",
			input:     "MSG.Body was empty",
			wantExact: "MSG.Body was empty",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			got := Redact(tc.input)
			if tc.wantMarker == "" {
				if got != tc.wantExact {
					t.Fatalf("Redact(%q) = %q, want it left alone as %q", tc.input, got, tc.wantExact)
				}
				return
			}
			if !strings.Contains(got, tc.wantMarker) {
				t.Fatalf("Redact(%q) = %q, want it to contain %q", tc.input, got, tc.wantMarker)
			}
			if strings.Contains(got, antBody[len("sk-ant-"):]) {
				t.Fatalf("raw secret body survived: %q", got)
			}
		})
	}
}

// TestRedact_SecondPassNestsMarkers pins that Redact is not idempotent.
// Callers redact exactly once, where the string is built; a marker's own
// prefix sits after a colon, which is a word start, so a second pass
// wraps it again. A boundary rule that swallowed that case would also be
// swallowing a genuine secret written after a colon.
func TestRedact_SecondPassNestsMarkers(t *testing.T) {
	t.Parallel()

	once := Redact("key " + "sk-ant-" + "api03body01") //#nosec G101 -- synthetic test fixture, never a real key
	twice := Redact(once)
	if !strings.Contains(twice, "[REDACTED:[REDACTED:sk-ant-]") {
		t.Fatalf("second pass did not nest: first %q, second %q", once, twice)
	}
}

func TestRedactJSONFields_OAuthKeys(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		key  string
	}{
		{"client_secret", "client_secret"},
		{"refresh_token", "refresh_token"},
		{"authorization_code", "authorization_code"},
		{"id_token", "id_token"},
		{"code", "code"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			input := `{"` + tc.key + `":"super-sensitive-value-xyz"}`
			got := RedactJSONFields(input)

			if strings.Contains(got, "super-sensitive-value-xyz") {
				t.Fatalf("raw value leaked for key %q: %s", tc.key, got)
			}
			expected := `"` + tc.key + `":"[REDACTED]"`
			if !strings.Contains(got, expected) {
				t.Fatalf("expected %q in output, got: %s", expected, got)
			}
		})
	}
}

func TestRedactJSONFields_CamelCaseOAuthKeys(t *testing.T) {
	t.Parallel()

	for _, key := range []string{"clientSecret", "idToken", "refreshToken", "authorizationCode"} {
		t.Run(key, func(t *testing.T) {
			t.Parallel()

			input := `{"` + key + `":"super-sensitive-value-xyz"}`
			got := RedactJSONFields(input)

			if strings.Contains(got, "super-sensitive-value-xyz") {
				t.Fatalf("raw value leaked for key %q: %s", key, got)
			}
			expected := `"` + key + `":"[REDACTED]"`
			if !strings.Contains(got, expected) {
				t.Fatalf("expected %q in output, got: %s", expected, got)
			}
		})
	}
}

// stringerSecret is a fmt.Stringer whose textual form embeds a prefixed
// secret, mirroring config/DTO values that are logged via slog.Any.
type stringerSecret struct {
	token string
}

func (s stringerSecret) String() string {
	return "upstream config token=" + s.token
}

func TestRedactHandler_SecretEmbeddedInErrorViaAny(t *testing.T) {
	t.Parallel()

	// Simulate the dominant leak vector: an upstream OAuth error whose
	// message embeds a prefixed secret from the response body, logged as
	// slog.Any("err", err). forbidigo only matches slog.* helpers, so the
	// error path here (slog.Any) is the exact shape that must be scrubbed.
	err := fmt.Errorf("token exchange failed: upstream returned sk-ant-api03-leakedsecret123")

	var buf bytes.Buffer
	l := newTestLogger(&buf)
	l.LogAttrs(context.Background(), slog.LevelError, "oauth failure",
		slog.Any("err", err),
	)

	out := buf.String()
	if strings.Contains(out, "leakedsecret123") {
		t.Fatalf("raw secret body leaked through slog.Any(error): %s", out)
	}
	if !strings.Contains(out, "[REDACTED:sk-ant-]") {
		t.Fatalf("expected sk-ant- redaction marker for error value, got: %s", out)
	}
}

func TestRedactHandler_SecretInWrappedErrorViaAny(t *testing.T) {
	t.Parallel()

	base := errors.New("bad credentials ghp_aB1cD2eF3gH4iJ5kL6mN7oP8qR9sT0")
	wrapped := fmt.Errorf("refresh failed: %w", base)

	var buf bytes.Buffer
	l := newTestLogger(&buf)
	l.LogAttrs(context.Background(), slog.LevelError, "refresh failure",
		slog.Any("err", wrapped),
	)

	out := buf.String()
	if strings.Contains(out, "aB1cD2eF3gH4iJ5kL6mN7oP8qR9sT0") {
		t.Fatalf("raw secret body leaked through wrapped error: %s", out)
	}
	if !strings.Contains(out, "[REDACTED:ghp_]") {
		t.Fatalf("expected ghp_ redaction marker for wrapped error, got: %s", out)
	}
}

func TestRedactHandler_SecretInStringerViaAny(t *testing.T) {
	t.Parallel()

	var buf bytes.Buffer
	l := newTestLogger(&buf)
	l.LogAttrs(context.Background(), slog.LevelInfo, "config loaded",
		slog.Any("cfg", stringerSecret{token: "xoxb-123456789-leaked"}),
	)

	out := buf.String()
	if strings.Contains(out, "123456789-leaked") {
		t.Fatalf("raw secret body leaked through fmt.Stringer value: %s", out)
	}
	if !strings.Contains(out, "[REDACTED:xoxb-]") {
		t.Fatalf("expected xoxb- redaction marker for stringer value, got: %s", out)
	}
}

func TestRedactHandler_NonStringScalarUnchanged(t *testing.T) {
	t.Parallel()

	var buf bytes.Buffer
	l := newTestLogger(&buf)
	l.LogAttrs(context.Background(), slog.LevelInfo, "scalar",
		slog.Int("count", 42),
		slog.Bool("ok", true),
	)

	var rec map[string]any
	if err := json.Unmarshal(buf.Bytes(), &rec); err != nil {
		t.Fatalf("invalid json output: %v: %s", err, buf.String())
	}
	if rec["count"] != float64(42) {
		t.Fatalf("int scalar altered: got %v, want 42", rec["count"])
	}
	if rec["ok"] != true {
		t.Fatalf("bool scalar altered: got %v, want true", rec["ok"])
	}
}

func TestRedactHandler_NewPrefixesInLogValues(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name   string
		value  string
		prefix string
	}{
		{"AKIA in log", "AKIAIOSFODNN7EXAMPLE", "AKIA"},
		{"glpat in log", "glpat-xxxxxxxxxxxxxxxxxxxx", "glpat-"},
		{"SG. in log", "SG.sendgrid-api-key-value", "SG."},
		{"rk_live_ in log", "rk_live_" + "striperestricted", "rk_live_"},
		{"sk_live_ in log", "sk_live_" + "stripesecretkey", "sk_live_"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			var buf bytes.Buffer
			l := newTestLogger(&buf)
			l.Info("secret found", "some_field", tc.value)

			out := buf.String()
			expected := "[REDACTED:" + tc.prefix + "]"
			if !strings.Contains(out, expected) {
				t.Fatalf("expected prefix redaction %q in output, got: %s", expected, out)
			}
			afterPrefix := tc.value[len(tc.prefix):]
			if strings.Contains(out, afterPrefix) {
				t.Fatalf("raw secret body leaked in output: %s", out)
			}
		})
	}
}
