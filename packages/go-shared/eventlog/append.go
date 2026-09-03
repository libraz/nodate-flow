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
	"encoding/json"
	"fmt"
	"log/slog"
	"time"

	"github.com/libraz/nodate-flow/packages/go-shared/dbretry"
	"github.com/libraz/nodate-flow/packages/go-shared/dbtype"
	"github.com/libraz/nodate-flow/packages/go-shared/eventbus"
)

// Event is the canonical shape passed to Append. Optional FKs accept
// pointers so the caller can express NULL by passing nil. Payload is
// marshalled to JSON; passing nil produces `{}`.
type Event struct {
	// Type is the event kind, e.g. eventbus.ItemScheduled. The canonical
	// set is packages/go-shared/eventbus/kinds.go; a raw string is not
	// accepted here so an unconsumed kind cannot be minted at a call site.
	Type eventbus.Kind
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
// hooks via [RegisterHook] at process startup and can drop one again
// with [RemoveHook].
//
// eventInternalID is the events.id row that was just written.
// Subscribers need it to resolve the event they were told about —
// webhook deliveries dedupe on its public id, notifications anchor on
// source_event_id — so a hook that only knows the workspace and the
// type cannot deliver anything. The signature matches flow-api's
// eventbus.NotifyHook exactly so one bridge can forward appends from
// either log to the same set of subscribers.
type NotifyHook = func(ctx context.Context, workspaceID uint32, eventType string, eventInternalID uint64)

// Append inserts a single event row through db and returns the
// inserted public_id on success. With a [dbretry.Tx] the row is part of
// that transaction and the hooks wait for its commit; with
// [dbretry.AutoCommit] the row is durable on return and the hooks run
// immediately.
//
// db is a [dbretry.CommitBoundary] rather than a bare statement
// executor because [NotifyHook] subscribers read the event row on their
// own connection: a handle whose commit nobody reports would wake them
// for a row they cannot see, and every delivery would silently resolve
// to nothing. That handle is therefore not expressible here.
//
// The retry policy comes from the handle, matching [eventbus.Append] in
// flow-api. On auto-commit the INSERT is its own transaction, so
// re-issuing it clears the FK-lock contention parallel writers produce
// on shared parents (workspaces, tasks, users). Inside a transaction a
// deadlock has invalidated the whole thing, so the only meaningful unit
// of retry is the caller's transaction, which [dbretry.InTx] wraps.
//
// A failure is reported to db as well as returned. Inside a transaction
// that means the transaction will not commit, so discarding the returned
// error does not buy the caller a half-recorded change.
func Append(ctx context.Context, db dbretry.CommitBoundary, evt Event) (dbtype.PublicID, error) {
	// fail reports the loss to the commit boundary before handing it back.
	// A transaction that changed something and then failed to record the
	// event describing it must not commit the change alone, and leaving
	// that to the caller is what produced logs full of appends nobody
	// acted on. Inside a transaction this refuses the commit; on the
	// auto-commit path there is nothing left to withhold and it is a
	// no-op.
	fail := func(err error) (dbtype.PublicID, error) {
		db.Fail(err)
		return dbtype.PublicID{}, err
	}
	// Internal ids must not reach the payload; see ValidatePayloadIDs.
	// The check is here rather than at each builder because this is the
	// only place every payload passes through.
	if err := ValidatePayloadIDs(evt.Payload); err != nil {
		return fail(fmt.Errorf("eventlog: append %s: %w", evt.Type, err))
	}
	var raw json.RawMessage
	if evt.Payload == nil {
		raw = json.RawMessage(`{}`)
	} else {
		b, err := json.Marshal(evt.Payload)
		if err != nil {
			return fail(fmt.Errorf("eventlog: marshal payload: %w", err))
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
		res, err := db.ExecContext(ctx, q, pubID, evt.WorkspaceID, taskArg, actorArg, string(evt.Type), raw, occurred)
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
	err := db.RunStatement(ctx, "eventlog.Append", insert)
	if err != nil {
		slog.ErrorContext(ctx, "eventlog: append failed",
			slog.String("type", string(evt.Type)),
			slog.Uint64("workspace_id", uint64(evt.WorkspaceID)),
			slog.String("error", err.Error()))
		return fail(err)
	}

	// LastInsertId is a positive int64 from AUTO_INCREMENT and events.id
	// is BIGINT UNSIGNED, so uint64 carries it without loss.
	eventInternalID := uint64(lastID) //#nosec G115 -- AUTO_INCREMENT LastInsertId is non-negative
	// Subscribers read the event row on their own connection, so they
	// must not be woken before it is visible there. Inside a transaction
	// AfterCommit holds them until the commit; on the auto-commit path
	// the row is already durable and they fire now. There is no third
	// case: a handle whose commit is unobservable cannot reach here.
	db.AfterCommit(func() {
		fireHooks(ctx, evt.WorkspaceID, string(evt.Type), eventInternalID)
	})
	return pubID, nil
}
