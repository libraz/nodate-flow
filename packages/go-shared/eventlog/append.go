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

	"github.com/libraz/nodate-flow/packages/go-shared/dbretry"
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

// NotifyHook fires after a successful append, and after the enclosing
// transaction commits when there is one. Hooks are synchronous but must
// be cheap; long-running work belongs on a worker. The caller registers
// hooks via RegisterHook / ClearHooks at process startup.
//
// eventInternalID is the events.id row that was just written.
// Subscribers need it to resolve the event they were told about —
// webhook deliveries dedupe on its public id, notifications anchor on
// source_event_id — so a hook that only knows the workspace and the
// type cannot deliver anything. The signature matches flow-api's
// eventbus.NotifyHook exactly so one bridge can forward appends from
// either log to the same set of subscribers.
type NotifyHook = func(ctx context.Context, workspaceID uint32, eventType string, eventInternalID uint64)

// Append inserts a single event row using the provided DBTX. When db
// is a *sql.Tx the event is part of that transaction. Returns the
// inserted public_id on success.
//
// Retries mirror [eventbus.Append] in flow-api, and so does the split
// between the two cases:
//
//   - On a *sql.DB (auto-commit) the INSERT is wrapped in a deadlock
//     retry. Parallel writers contend on FK record locks for shared
//     parents — workspaces, tasks, users — and InnoDB resolves the
//     contention by rolling one side back with ER_LOCK_DEADLOCK.
//     Re-issuing the statement clears it.
//   - On a *sql.Tx the caller owns the retry boundary and Append must
//     not retry: a deadlock invalidates the entire transaction, so
//     re-running this one statement would issue it against a
//     transaction the server has already rolled back. The unit that has
//     to be retried is the caller's transaction, which is what
//     [dbretry.InTx] wraps. Callers that open a transaction by hand get
//     no retry at all, and a deadlock surfaces to the user as a 500 for
//     work that would have succeeded on a second attempt.
func Append(ctx context.Context, db DBTX, evt Event) (dbtype.PublicID, error) {
	// Internal ids must not reach the payload; see ValidatePayloadIDs.
	// The check is here rather than at each builder because this is the
	// only place every payload passes through.
	if err := ValidatePayloadIDs(evt.Payload); err != nil {
		return dbtype.PublicID{}, fmt.Errorf("eventlog: append %s: %w", evt.Type, err)
	}
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
	var lastID int64
	insert := func(ctx context.Context) error {
		res, err := db.ExecContext(ctx, q, pubID, evt.WorkspaceID, taskArg, actorArg, evt.Type, raw, occurred)
		if err != nil {
			return err
		}
		id, err := res.LastInsertId()
		if err != nil {
			return err
		}
		lastID = id
		return nil
	}
	_, isTx := db.(*sql.Tx)
	var err error
	if isTx {
		err = insert(ctx)
	} else {
		err = dbretry.Do(ctx, "eventlog.Append", insert)
	}
	if err != nil {
		slog.ErrorContext(ctx, "eventlog: append failed",
			slog.String("type", evt.Type),
			slog.Uint64("workspace_id", uint64(evt.WorkspaceID)),
			slog.String("error", err.Error()))
		return dbtype.PublicID{}, err
	}

	// LastInsertId is a positive int64 from AUTO_INCREMENT and events.id
	// is BIGINT UNSIGNED, so uint64 carries it without loss.
	eventInternalID := uint64(lastID) //#nosec G115 -- AUTO_INCREMENT LastInsertId is non-negative
	// Subscribers read the event row on their own connection, so they
	// must not be woken before it is visible there. With a collector on
	// the context (dbretry.InTx) the fan-out waits for the commit; on the
	// auto-commit path the row is already durable and it fires now.
	//
	// A transaction the caller opened by hand has neither property: the
	// hooks would be handed an id nothing else can resolve yet, and every
	// subscriber would quietly deliver nothing. Refuse instead, and name
	// the caller — the same rule flow-api's eventbus applies.
	if isTx && !dbretry.HasCommitHooks(ctx) && hasHooks() {
		slog.ErrorContext(ctx, "eventlog: fan-out skipped; append ran in a hand-rolled transaction without a commit boundary (use dbretry.InTx)",
			slog.String("type", evt.Type),
			slog.Uint64("workspace_id", uint64(evt.WorkspaceID)),
			slog.Uint64("event_id", eventInternalID))
		return pubID, nil
	}
	dbretry.AddCommitHook(ctx, func() {
		fireHooks(ctx, evt.WorkspaceID, evt.Type, eventInternalID)
	})
	return pubID, nil
}
