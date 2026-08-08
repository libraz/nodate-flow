// Package logutil entity helpers.
//
// These helpers exist to enforce CLAUDE.md convention 18: internal
// `id INT UNSIGNED` is for FK / JOIN only. Only `public_id BINARY(16)`
// (UUID v7) may be exposed externally — including in structured logs.
//
// The previous pattern of `slog.Int64("workspace_id", int64(ws.ID))` is
// forbidden by the forbidigo lint rule; use [LogEntity] instead.
//
// The lint rule is a nudge, not the boundary. It can only match on the
// callee name, so it never sees `slog.Any("workspace_id", ws.ID)` nor the
// loose `logger.Warn(msg, "workspace_id", id)` key/value form. The
// boundary that actually holds is [RedactHandler], which inspects the
// resolved record: see [IsInternalIDKey].
package logutil

import (
	"log/slog"
	"strings"

	"github.com/google/uuid"

	"github.com/libraz/nodate-flow/packages/go-shared/dbtype"
)

// LogEntity returns a slog.Attr for a domain entity, keyed
// "<name>_public_id" with the canonical UUID string form as value.
//
// Use this in place of slog.Int64("<name>_id", ...) for any
// user-facing entity (workspace, task, user, project, page, label,
// comment, event, calendar, share, invite, ...). Internal numeric ids
// must never appear in logs.
//
// Example:
//
//	slog.ErrorContext(ctx, "eventbus.Append failed",
//	    slog.Any("err", err),
//	    logutil.LogEntity("workspace", ws.PublicID),
//	    logutil.LogEntity("task", task.PublicID),
//	)
func LogEntity(name string, publicID uuid.UUID) slog.Attr {
	return slog.String(name+"_public_id", publicID.String())
}

// LogEntityPID is the dbtype.PublicID twin of LogEntity for callers that
// already hold the project's PublicID wrapper (typically code that has
// just round-tripped through sqlc).
func LogEntityPID(name string, publicID dbtype.PublicID) slog.Attr {
	return slog.String(name+"_public_id", publicID.String())
}

// InternalIDPlaceholder replaces the value of any numeric attr whose key
// names a row id. It is deliberately loud: an operator who sees it knows
// the call site still has to be moved onto [LogEntity].
const InternalIDPlaceholder = "[REDACTED:internal-id]"

// internalIDKeySuffixes are the attr key endings that name an internal
// database row id in this codebase. "_internal" covers the worker-side
// spelling (workspace_internal, signal_internal, ...); "_id" covers the
// plain one. Both are checked case-insensitively.
var internalIDKeySuffixes = []string{"_id", "_internal", "_internal_id"}

// IsInternalIDKey reports whether an attr key names an internal row id
// and therefore must not carry a numeric value into a log line.
//
// The predicate is on the key, not the value, because the key is the only
// thing available at every point the rule has to hold: the call site, the
// slog.Any escape hatch, the loose key/value form, and the handler. A
// "_public_id" key is exempt — that is the sanctioned spelling and its
// value is a UUID string, never a sequence.
//
// The rule is intentionally blunt. A numeric attr under an id-shaped key
// is either an internal sequence (a leak) or a badly named counter (a
// naming bug); redacting both is the safe direction, and callers with a
// genuine number can rename or use [LogNumber] under a non-id key.
func IsInternalIDKey(key string) bool {
	k := strings.ToLower(strings.TrimSpace(key))
	if k == "id" {
		return true
	}
	if strings.HasSuffix(k, "_public_id") {
		return false
	}
	for _, suffix := range internalIDKeySuffixes {
		if strings.HasSuffix(k, suffix) {
			return true
		}
	}
	return false
}

// numeric is the set of integer types a caller might hand [LogNumber]. It
// is a type set rather than a plain int64 parameter so call sites do not
// sprout conversions: an unconverted len() and a uint32 DB column both fit.
type numeric interface {
	~int | ~int8 | ~int16 | ~int32 | ~int64 | ~uint | ~uint8 | ~uint16 | ~uint32 | ~uint64
}

// LogNumber returns a slog.Attr for a numeric value that is not an entity
// identifier — a count, a byte size, a duration in milliseconds, an HTTP
// status code.
//
// It exists because slog.Int / slog.Int64 / slog.Uint64 are forbidden in
// service code. A callee-name-only lint rule cannot tell
// slog.Int64("row_count", n) from slog.Int64("task_id", id), so the raw
// constructors are banned outright and the two legitimate shapes get named
// helpers: [LogEntity] for identity, LogNumber for everything else.
//
// Passing an id-shaped key redacts the value rather than logging it, so
// routing an internal id through LogNumber cannot recreate the leak the
// ban exists to prevent.
func LogNumber[T numeric](name string, v T) slog.Attr {
	if IsInternalIDKey(name) {
		return slog.String(name, InternalIDPlaceholder)
	}
	//nolint:forbidigo // the sanctioned wrapper; every other caller goes through here
	return slog.Int64(name, int64(v))
}
