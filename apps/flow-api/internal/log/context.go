package log

import (
	"context"
	"log/slog"
)

// WithLogger returns a derived context that carries the given logger. Use
// it from middleware to attach a request-scoped logger pre-populated with
// request-id, actor-id, and workspace-id attrs. The accompanying
// LoggerFromContext extracts that logger downstream.
//
// The function shares its context key with [RequestLogger] (and therefore
// with [FromContext]) so every helper agrees on a single carrier slot.
func WithLogger(ctx context.Context, l *slog.Logger) context.Context {
	if ctx == nil {
		return context.WithValue(context.Background(), ctxKeyLogger, l)
	}
	return context.WithValue(ctx, ctxKeyLogger, l)
}

// LoggerFromContext returns the request-scoped logger previously stored
// by [WithLogger] (or by [RequestLogger]). When ctx is nil or no logger
// has been attached, [slog.Default] is returned so callers can safely log
// without nil-checking.
//
// This is the canonical way for handlers to obtain a logger that already
// carries request_id / actor_id / workspace_id, so the handler does not
// have to thread those values manually through every log line.
//
// The function returns the stored logger as-is; downstream enrichment
// (workspace_id once RequireWorkspaceMember has run, etc.) is applied by
// the LoggerContext middleware which is invoked once after auth and
// once after the workspace ACL stage so the captured logger reflects
// every value in scope at handler time.
func LoggerFromContext(ctx context.Context) *slog.Logger {
	if ctx == nil {
		return slog.Default()
	}
	if l, ok := ctx.Value(ctxKeyLogger).(*slog.Logger); ok && l != nil {
		return l
	}
	return slog.Default()
}

// WithRequestID returns a derived context carrying the given request id.
// It is the writable counterpart of [RequestIDFromContext]. The
// production [RequestLogger] middleware always sets this; the helper is
// exported so tests and out-of-band fan-out goroutines can stamp the
// same id on derived contexts without re-implementing the storage.
func WithRequestID(ctx context.Context, requestID string) context.Context {
	if ctx == nil {
		return context.WithValue(context.Background(), ctxKeyRequestID, requestID)
	}
	return context.WithValue(ctx, ctxKeyRequestID, requestID)
}
