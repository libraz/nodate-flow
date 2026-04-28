// Package log holds structured-logging helpers for auth-api. The
// package centerpiece is a small slog-context plumbing pair —
// [WithLogger] and [LoggerFromContext] — used by the
// [middleware.LoggerContext] HTTP middleware so handlers can emit logs
// pre-tagged with request_id / actor_id / workspace_id without threading
// those values manually.
//
// Redaction stays in the shared logutil package
// (github.com/nodate-flow/nodate-flow/packages/go-shared/logutil); this
// package intentionally does not re-implement it.
package log

import (
	"context"
	"log/slog"
)

// loggerCtxKey is the unexported context key under which the
// request-scoped logger is stored. A typed empty struct is used per the
// Go convention so the key cannot be forged accidentally by another
// package storing values in the same context.
type loggerCtxKey struct{}

// requestIDCtxKey is the unexported context key for the request id
// stamp. It is read by [middleware.LoggerContext] when assembling the
// per-request logger.
type requestIDCtxKey struct{}

// WithLogger returns a derived context that carries the given logger.
// Use it from middleware to attach a request-scoped logger pre-populated
// with request-id, actor-id, and workspace-id attrs. The accompanying
// [LoggerFromContext] extracts that logger downstream.
func WithLogger(ctx context.Context, l *slog.Logger) context.Context {
	if ctx == nil {
		return context.WithValue(context.Background(), loggerCtxKey{}, l)
	}
	return context.WithValue(ctx, loggerCtxKey{}, l)
}

// LoggerFromContext returns the request-scoped logger previously stored
// by [WithLogger]. When ctx is nil or no logger has been attached,
// [slog.Default] is returned so callers can safely log without
// nil-checking.
//
// This is the canonical way for handlers to obtain a logger that already
// carries request_id / actor_id / workspace_id, so the handler does not
// have to thread those values manually through every log line.
func LoggerFromContext(ctx context.Context) *slog.Logger {
	if ctx == nil {
		return slog.Default()
	}
	if l, ok := ctx.Value(loggerCtxKey{}).(*slog.Logger); ok && l != nil {
		return l
	}
	return slog.Default()
}

// WithRequestID returns a derived context carrying the given request id.
// Counterpart of [RequestIDFromContext]; both share the same unexported
// key so middleware and helpers agree on a single carrier slot.
func WithRequestID(ctx context.Context, requestID string) context.Context {
	if ctx == nil {
		return context.WithValue(context.Background(), requestIDCtxKey{}, requestID)
	}
	return context.WithValue(ctx, requestIDCtxKey{}, requestID)
}

// RequestIDFromContext returns the request id stamped by [WithRequestID]
// (or the request-logger middleware), or "" if the context carries no
// request id.
func RequestIDFromContext(ctx context.Context) string {
	if ctx == nil {
		return ""
	}
	if s, ok := ctx.Value(requestIDCtxKey{}).(string); ok {
		return s
	}
	return ""
}
