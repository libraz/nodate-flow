// Package eventbus provides a thin helper for appending rows to the
// append-only events table. It is intentionally tiny: handlers call
// [Append] inside the same request flow that mutates state so that the
// audit trail and the state change live and die together.
//
// The events table itself has no UPDATE/DELETE path; only purgeWorkspace
// removes rows. See sql/tables/events.sql.
package eventbus

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"sync"
	"time"

	"github.com/nodate-flow/nodate-flow/apps/flow-api/internal/db/generated"
	"github.com/nodate-flow/nodate-flow/apps/flow-api/internal/db/types"
)

// DBTX is the minimal sqlc DBTX surface needed by [Append]. Both *sql.DB
// and *sql.Tx satisfy it; passing a *sql.Tx keeps the event row in the
// same transaction as the underlying state change.
type DBTX interface {
	ExecContext(ctx context.Context, query string, args ...interface{}) (sql.Result, error)
	PrepareContext(ctx context.Context, query string) (*sql.Stmt, error)
	QueryContext(ctx context.Context, query string, args ...interface{}) (*sql.Rows, error)
	QueryRowContext(ctx context.Context, query string, args ...interface{}) *sql.Row
}

// Event is the high-level shape callers pass to [Append]. The optional
// TaskID and ActorUserID accept *int64 so callers can express NULL by
// passing nil. Payload is JSON-marshalled before insertion; pass nil for
// an empty payload.
type Event struct {
	// Type is the canonical dotted event name, e.g. "task.created".
	Type string
	// WorkspaceID is the internal workspace id (never the public id).
	WorkspaceID uint32
	// ActorUserID is the internal user id of the actor; nil for system.
	ActorUserID *int64
	// TaskID is the internal task id when the event targets a task.
	TaskID *int64
	// ProjectID is reserved for future use; events.project_id is not yet
	// part of the schema, so this field is currently ignored by [Append].
	ProjectID *int64
	// Payload is an arbitrary JSON-encodable value stored in payload_json.
	Payload any
}

// NotifyHook is a post-append fan-out subscriber. Multiple
// subscribers are supported: the stream tap publishes realtime SSE
// events, and the agentruntime event-source enqueues on_event agent
// runs, both off the same dispatch.
//
// Hooks must be non-blocking and must never panic. Leave unset for
// the no-op behaviour.
type NotifyHook = func(ctx context.Context, workspaceInternalID uint32, eventType string)

var (
	notifyMu    sync.RWMutex
	notifyHooks []NotifyHook
)

// SetNotifyHook replaces the hook list with a single subscriber.
// Kept for backwards compatibility with the previous single-hook
// API; new call sites should prefer [AddNotifyHook]. Calling
// SetNotifyHook(nil) clears the list.
func SetNotifyHook(hook NotifyHook) {
	notifyMu.Lock()
	defer notifyMu.Unlock()
	if hook == nil {
		notifyHooks = nil
		return
	}
	notifyHooks = []NotifyHook{hook}
}

// AddNotifyHook appends a subscriber to the notify fan-out. Returns
// an index that can be passed to [RemoveNotifyHook]; tests use this
// to unregister a hook when they tear down.
func AddNotifyHook(hook NotifyHook) int {
	notifyMu.Lock()
	defer notifyMu.Unlock()
	notifyHooks = append(notifyHooks, hook)
	return len(notifyHooks) - 1
}

// ClearNotifyHooks drops every registered subscriber. Used by tests
// that want a clean slate between runs.
func ClearNotifyHooks() {
	notifyMu.Lock()
	defer notifyMu.Unlock()
	notifyHooks = nil
}

func fireNotifyHooks(ctx context.Context, workspaceInternalID uint32, eventType string) {
	notifyMu.RLock()
	hooks := notifyHooks
	notifyMu.RUnlock()
	for _, h := range hooks {
		h(ctx, workspaceInternalID, eventType)
	}
}

// Append inserts a single event row using the provided DBTX. When db is
// a *sql.Tx the event is part of that transaction.
func Append(ctx context.Context, db DBTX, evt Event) error {
	var raw json.RawMessage
	if evt.Payload == nil {
		raw = json.RawMessage(`{}`)
	} else {
		b, err := json.Marshal(evt.Payload)
		if err != nil {
			return fmt.Errorf("eventbus: marshal payload: %w", err)
		}
		raw = b
	}
	q := generated.New(db)
	taskID := sql.NullInt32{}
	if evt.TaskID != nil {
		taskID = sql.NullInt32{Int32: int32(*evt.TaskID), Valid: true}
	}
	actorID := sql.NullInt32{}
	if evt.ActorUserID != nil {
		actorID = sql.NullInt32{Int32: int32(*evt.ActorUserID), Valid: true}
	}
	_, err := q.AppendEvent(ctx, generated.AppendEventParams{
		PublicID:    types.New(),
		WorkspaceID: evt.WorkspaceID,
		TaskID:      taskID,
		ActorUserID: actorID,
		Type:        evt.Type,
		PayloadJson: raw,
		OccurredAt:  time.Now().UTC(),
	})
	if err == nil {
		fireNotifyHooks(ctx, evt.WorkspaceID, evt.Type)
	}
	return err
}
