// Package logutil entity helpers.
//
// These helpers exist to enforce CLAUDE.md convention 18: internal
// `id INT UNSIGNED` is for FK / JOIN only. Only `public_id BINARY(16)`
// (UUID v7) may be exposed externally — including in structured logs.
//
// The previous pattern of `slog.Int64("workspace_id", int64(ws.ID))` is
// forbidden by the forbidigo lint rule; use [LogEntity] instead.
package logutil

import (
	"log/slog"

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
