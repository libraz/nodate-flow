package log

import (
	"context"
	"log/slog"
	"strings"

	"github.com/nodate-flow/nodate-flow/apps/api/internal/ai"
)

// sensitiveAttrKeys are attribute keys whose values are always replaced
// with [REDACTED] regardless of content.
var sensitiveAttrKeys = map[string]struct{}{
	"authorization": {},
	"api_key":       {},
	"apikey":        {},
	"token":         {},
	"password":      {},
	"secret":        {},
}

// RedactHandler wraps another slog.Handler and scrubs secret-looking
// values from record attrs before forwarding.
type RedactHandler struct {
	inner slog.Handler
}

// NewRedactHandler returns a RedactHandler wrapping inner.
func NewRedactHandler(inner slog.Handler) *RedactHandler {
	return &RedactHandler{inner: inner}
}

// Enabled reports whether the inner handler handles records at lvl.
func (h *RedactHandler) Enabled(ctx context.Context, lvl slog.Level) bool {
	return h.inner.Enabled(ctx, lvl)
}

// Handle redacts attrs in-place on a copied record, then forwards.
func (h *RedactHandler) Handle(ctx context.Context, r slog.Record) error {
	newRec := slog.NewRecord(r.Time, r.Level, r.Message, r.PC)
	r.Attrs(func(a slog.Attr) bool {
		newRec.AddAttrs(redactAttr(a))
		return true
	})
	return h.inner.Handle(ctx, newRec)
}

// WithAttrs returns a new handler whose inner has the supplied attrs
// attached (also redacted).
func (h *RedactHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	red := make([]slog.Attr, len(attrs))
	for i, a := range attrs {
		red[i] = redactAttr(a)
	}
	return &RedactHandler{inner: h.inner.WithAttrs(red)}
}

// WithGroup returns a new handler with the supplied group name pushed on
// the inner handler.
func (h *RedactHandler) WithGroup(name string) slog.Handler {
	return &RedactHandler{inner: h.inner.WithGroup(name)}
}

// redactAttr returns a copy of a with its value scrubbed if necessary.
func redactAttr(a slog.Attr) slog.Attr {
	if _, hit := sensitiveAttrKeys[strings.ToLower(a.Key)]; hit {
		return slog.String(a.Key, "[REDACTED]")
	}
	v := a.Value
	switch v.Kind() {
	case slog.KindString:
		return slog.String(a.Key, ai.Redact(v.String()))
	case slog.KindGroup:
		attrs := v.Group()
		out := make([]any, 0, len(attrs))
		for _, inner := range attrs {
			red := redactAttr(inner)
			out = append(out, red)
		}
		return slog.Group(a.Key, out...)
	case slog.KindLogValuer:
		resolved := v.Resolve()
		return redactAttr(slog.Attr{Key: a.Key, Value: resolved})
	default:
		return a
	}
}
