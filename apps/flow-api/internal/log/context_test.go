package log

import (
	"bytes"
	"context"
	"log/slog"
	"strings"
	"testing"
)

// TestLoggerFromContext_Default verifies that LoggerFromContext returns
// the package default logger when no value has been attached, regardless
// of whether the supplied context is nil or a fresh background context.
func TestLoggerFromContext_Default(t *testing.T) {
	t.Run("nil ctx", func(t *testing.T) {
		got := LoggerFromContext(nil) //nolint:staticcheck // intentionally exercises the nil branch
		if got == nil {
			t.Fatal("expected non-nil logger fallback")
		}
		if got != slog.Default() {
			t.Fatalf("expected slog.Default fallback, got %p", got)
		}
	})
	t.Run("empty ctx", func(t *testing.T) {
		got := LoggerFromContext(context.Background())
		if got == nil {
			t.Fatal("expected non-nil logger fallback")
		}
		if got != slog.Default() {
			t.Fatalf("expected slog.Default fallback, got %p", got)
		}
	})
}

// TestWithLogger_RoundTrip verifies that a logger stored via WithLogger
// can be retrieved with LoggerFromContext and that emitted records carry
// the attrs attached to the stored logger.
func TestWithLogger_RoundTrip(t *testing.T) {
	var buf bytes.Buffer
	base := slog.NewJSONHandler(&buf, &slog.HandlerOptions{Level: slog.LevelInfo})
	scoped := slog.New(base).With(slog.String("request_id", "rid-1"))

	ctx := WithLogger(context.Background(), scoped)
	got := LoggerFromContext(ctx)
	if got != scoped {
		t.Fatalf("expected scoped logger to round-trip, got %p want %p", got, scoped)
	}

	got.LogAttrs(ctx, slog.LevelInfo, "hello")
	out := buf.String()
	for _, needle := range []string{`"msg":"hello"`, `"request_id":"rid-1"`} {
		if !strings.Contains(out, needle) {
			t.Fatalf("missing %s in output: %s", needle, out)
		}
	}
}

// TestWithLogger_NilContext makes sure the helper does not panic when the
// caller hands it a nil context (defensive for edge cases inside test
// helpers and background fan-out goroutines).
func TestWithLogger_NilContext(t *testing.T) {
	scoped := slog.New(slog.NewJSONHandler(&bytes.Buffer{}, nil))
	ctx := WithLogger(nil, scoped) //nolint:staticcheck // intentionally exercises the nil branch
	if ctx == nil {
		t.Fatal("expected non-nil context")
	}
	if got := LoggerFromContext(ctx); got != scoped {
		t.Fatalf("expected scoped logger to round-trip, got %p want %p", got, scoped)
	}
}
