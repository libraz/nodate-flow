package log

import (
	"context"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
)

type ctxKey int

const (
	ctxKeyLogger ctxKey = iota
	ctxKeyRequestID
)

// statusRecorder captures the response status for logging.
type statusRecorder struct {
	http.ResponseWriter
	status int
}

// WriteHeader records the status code before delegating.
func (s *statusRecorder) WriteHeader(code int) {
	s.status = code
	s.ResponseWriter.WriteHeader(code)
}

// RequestLogger returns an HTTP middleware that assigns a UUID v7
// request_id, injects a request-scoped logger into the context, and logs
// one structured line per request when it finishes.
func RequestLogger(logger *slog.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			reqID := newRequestID()
			reqLogger := logger.With(slog.String("request_id", reqID))

			ctx := context.WithValue(r.Context(), ctxKeyLogger, reqLogger)
			ctx = context.WithValue(ctx, ctxKeyRequestID, reqID)

			rec := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
			start := time.Now()
			next.ServeHTTP(rec, r.WithContext(ctx))
			dur := time.Since(start)

			reqLogger.LogAttrs(ctx, slog.LevelInfo, "http_request",
				slog.String("method", r.Method),
				slog.String("path", sanitizePath(r)),
				slog.Int("status", rec.status),
				slog.Int64("duration_ms", dur.Milliseconds()),
				slog.String("remote_addr", r.RemoteAddr),
			)
		})
	}
}

// FromContext returns the request-scoped logger previously injected by
// RequestLogger, falling back to slog.Default.
func FromContext(ctx context.Context) *slog.Logger {
	if ctx == nil {
		return slog.Default()
	}
	if l, ok := ctx.Value(ctxKeyLogger).(*slog.Logger); ok && l != nil {
		return l
	}
	return slog.Default()
}

// RequestIDFromContext returns the request_id injected by RequestLogger,
// or "" if none is present.
func RequestIDFromContext(ctx context.Context) string {
	if ctx == nil {
		return ""
	}
	if s, ok := ctx.Value(ctxKeyRequestID).(string); ok {
		return s
	}
	return ""
}

// tokenPathPrefixes are path prefixes whose next segment is an unguessable
// capability token (share/lens links). The tokens are random with no
// redactable prefix, so Redact cannot catch them; the trailing segment is
// masked explicitly instead.
var tokenPathPrefixes = []string{"/share/cal/", "/public/lenses/"}

// sanitizePath returns a log-safe request path. It prefers the chi route
// pattern (already fully templated, e.g. "/share/cal/{token}") and falls
// back to masking the token segment of known capability-link prefixes when
// no route context is available. The query string is never included.
func sanitizePath(r *http.Request) string {
	if rctx := chi.RouteContext(r.Context()); rctx != nil {
		if pat := rctx.RoutePattern(); pat != "" {
			return pat
		}
	}
	return maskTokenPath(r.URL.Path)
}

// maskTokenPath replaces the segment following a known capability-link
// prefix with "{token}", preserving any deeper path. Paths that do not match
// a known prefix are returned unchanged.
func maskTokenPath(path string) string {
	for _, prefix := range tokenPathPrefixes {
		if !strings.HasPrefix(path, prefix) {
			continue
		}
		rest := path[len(prefix):]
		if slash := strings.IndexByte(rest, '/'); slash >= 0 {
			return prefix + "{token}" + rest[slash:]
		}
		return prefix + "{token}"
	}
	return path
}

// newRequestID returns a UUID v7 string, or a v4 fallback on error.
func newRequestID() string {
	if id, err := uuid.NewV7(); err == nil {
		return id.String()
	}
	return uuid.NewString()
}
