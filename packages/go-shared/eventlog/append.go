// Package eventlog provides a service-agnostic appender for the
// shared `events` table. It uses raw SQL (no sqlc dependency) so
// cross-service code — itemkit, memberkit, reconciler — can write
// event rows without coupling to flow-api or auth-api generated
// types.
//
// Per ADR 0005 the events table is append-only. The sole deletion
// path is the per-tenant purge used by test fixtures.
package eventlog

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"log/slog"
	"time"

	"github.com/libraz/nodate-flow/packages/go-shared/dbtype"
)

// DBTX is the minimal surface accepted by Append. Both *sql.DB and
// *sql.Tx satisfy it; passing a *sql.Tx keeps the event row in the
// same transaction as the underlying state change.
type DBTX interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
}

// Event is the canonical shape passed to Append. Optional FKs accept
// pointers so the caller can express NULL by passing nil. Payload is
// marshalled to JSON; passing nil produces `{}`.
type Event struct {
	// Type is the dotted event kind string, e.g. "item.scheduled".
	// See packages/go-shared/eventbus/kinds.go for the canonical set.
	Type string
	// WorkspaceID is the internal workspace id (never the public id).
	WorkspaceID uint32
	// ActorUserID is the acting user's internal id; nil for system.
	ActorUserID *uint32
	// TaskID is the internal task id when the event targets a task.
	TaskID *uint32
	// Payload is any JSON-encodable value stored in payload_json.
	Payload any
	// OccurredAt overrides the insert timestamp; zero means "now".
	OccurredAt time.Time
}

// NotifyHook fires AFTER a successful append. Hooks are synchronous
// but must be cheap; long-running work belongs on a worker. The
// caller registers hooks via RegisterHook / ClearHooks at process
// startup (flow-api wires this to the SSE notifier + agent runtime).
type NotifyHook = func(ctx context.Context, workspaceID uint32, eventType string)

// Append inserts a single event row using the provided DBTX. When db
// is a *sql.Tx the event is part of that transaction. Returns the
// inserted public_id on success.
func Append(ctx context.Context, db DBTX, evt Event) (dbtype.PublicID, error) {
	var raw json.RawMessage
	if evt.Payload == nil {
		raw = json.RawMessage(`{}`)
	} else {
		b, err := json.Marshal(evt.Payload)
		if err != nil {
			return dbtype.PublicID{}, fmt.Errorf("eventlog: marshal payload: %w", err)
		}
		raw = b
	}
	occurred := evt.OccurredAt
	if occurred.IsZero() {
		occurred = time.Now().UTC()
	}
	pubID := dbtype.New()

	var actorArg, taskArg any
	if evt.ActorUserID != nil {
		actorArg = *evt.ActorUserID
	}
	if evt.TaskID != nil {
		taskArg = *evt.TaskID
	}

	const q = `INSERT INTO events (public_id, workspace_id, task_id, actor_user_id, type, payload_json, occurred_at)
	           VALUES (?, ?, ?, ?, ?, ?, ?)`
	if _, err := db.ExecContext(ctx, q, pubID, evt.WorkspaceID, taskArg, actorArg, evt.Type, raw, occurred); err != nil {
		slog.ErrorContext(ctx, "eventlog: append failed",
			slog.String("type", evt.Type),
			slog.Uint64("workspace_id", uint64(evt.WorkspaceID)),
			slog.String("error", err.Error()))
		return dbtype.PublicID{}, err
	}
	fireHooks(ctx, evt.WorkspaceID, evt.Type)
	return pubID, nil
}
