package logutil

import (
	"bytes"
	"context"
	"encoding/json"
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

	keys := []string{"authorization", "token", "password", "secret", "api_key", "apikey"}
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
