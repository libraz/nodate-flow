package log

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

func TestRedactHandler_RedactsPrefixedSecret(t *testing.T) {
	var buf bytes.Buffer
	l := newTestLogger(&buf)
	l.Info("provider call", "api_key_material", "sk-ant-abc123xyz")

	out := buf.String()
	if !strings.Contains(out, "[REDACTED:sk-ant-]") {
		t.Fatalf("expected sk-ant- prefix to be redacted, got: %s", out)
	}
	if strings.Contains(out, "abc123xyz") {
		t.Fatalf("raw secret leaked: %s", out)
	}
}

func TestRedactHandler_RedactsSensitiveKey(t *testing.T) {
	var buf bytes.Buffer
	l := newTestLogger(&buf)
	l.Info("incoming", "authorization", "Bearer opaque-value-not-a-prefix")

	var rec map[string]any
	if err := json.Unmarshal(buf.Bytes(), &rec); err != nil {
		t.Fatalf("invalid json: %v: %s", err, buf.String())
	}
	if got := rec["authorization"]; got != "[REDACTED]" {
		t.Fatalf("authorization not fully redacted: %v", got)
	}
}

func TestRedactHandler_WithAttrs(t *testing.T) {
	var buf bytes.Buffer
	l := newTestLogger(&buf).With("token", "anything")
	l.Info("msg")
	if !strings.Contains(buf.String(), `"token":"[REDACTED]"`) {
		t.Fatalf("WithAttrs did not redact: %s", buf.String())
	}
}

func TestRedactHandler_GroupedSecret(t *testing.T) {
	var buf bytes.Buffer
	l := newTestLogger(&buf)
	l.LogAttrs(context.Background(), slog.LevelInfo, "g",
		slog.Group("payload", slog.String("api_key", "sk-abc123")),
	)
	out := buf.String()
	if !strings.Contains(out, "[REDACTED]") {
		t.Fatalf("grouped sensitive key not redacted: %s", out)
	}
}
