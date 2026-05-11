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
	"log/slog"
	"sync"
	"sync/atomic"
	"time"

	"github.com/nodate-flow/nodate-flow/apps/flow-api/internal/db/dbretry"
	"github.com/nodate-flow/nodate-flow/apps/flow-api/internal/db/generated"
	"github.com/nodate-flow/nodate-flow/apps/flow-api/internal/db/types"
)

// globalSeq is a monotonically increasing counter that assigns a
// sequence number to every event notification. SSE subscribers include
// the sequence in their payload so clients can detect gaps and reorder
// events that arrive out of order from concurrent goroutines.
var globalSeq atomic.Int64

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
// eventInternalID is the events.id row that was just appended; hooks
// that need to dedupe (notification fan-out) anchor on it via
// notifications.source_event_id, while hooks that only need the
// signal (SSE tap, on_event triggers) ignore it.
//
// Hooks must be non-blocking and must never panic. Leave unset for
// the no-op behaviour.
type NotifyHook = func(ctx context.Context, workspaceInternalID uint32, eventType string, eventInternalID uint32)

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

type seqCtxKey struct{}

// WithSeq returns a copy of ctx carrying the event sequence number.
// Hooks can retrieve it via SeqFromContext to include in their
// notifications, allowing clients to detect gaps and reorder.
func WithSeq(ctx context.Context, seq int64) context.Context {
	return context.WithValue(ctx, seqCtxKey{}, seq)
}

// SeqFromContext returns the event sequence number set by Append,
// or zero if the context was not tagged.
func SeqFromContext(ctx context.Context) int64 {
	if ctx == nil {
		return 0
	}
	v, _ := ctx.Value(seqCtxKey{}).(int64)
	return v
}

type actorAgentCtxKey struct{}

// WithActorAgentID returns a copy of ctx tagged with the internal id of
// the AI agent on whose behalf the wrapped work is running. The
// orchestrator runner sets this around each agent tick so downstream
// emitters (eventbus.Append, MCP tool calls) can attribute their event
// rows to actor_agent_id instead of actor_user_id. Passing zero is a
// no-op so callers can use it unconditionally.
func WithActorAgentID(ctx context.Context, agentID uint32) context.Context {
	if agentID == 0 {
		return ctx
	}
	return context.WithValue(ctx, actorAgentCtxKey{}, agentID)
}

// ActorAgentIDFromContext returns the agent id previously set via
// WithActorAgentID, or zero when the context is not tagged. A non-zero
// return indicates the caller should use AppendAgentEvent (binding
// actor_agent_id) instead of the user-actor AppendEvent path.
func ActorAgentIDFromContext(ctx context.Context) uint32 {
	if ctx == nil {
		return 0
	}
	v, _ := ctx.Value(actorAgentCtxKey{}).(uint32)
	return v
}

func fireNotifyHooks(ctx context.Context, workspaceInternalID uint32, eventType string, eventInternalID uint32) {
	notifyMu.RLock()
	hooks := notifyHooks
	notifyMu.RUnlock()
	for _, h := range hooks {
		h(ctx, workspaceInternalID, eventType, eventInternalID)
	}
}

// Append inserts a single event row using the provided DBTX. When db is
// a *sql.Tx the event is part of that transaction.
//
// When db is a *sql.DB (auto-commit, no enclosing transaction) the
// INSERT is wrapped in a deadlock retry loop: parallel handlers and
// fan-out goroutines compete on FK record locks for shared parents
// (workspaces, tasks, users), and InnoDB occasionally rolls one back
// with ER_LOCK_DEADLOCK (1213). Re-issuing the statement reliably
// resolves the contention. Callers passing a *sql.Tx own the retry
// boundary themselves — InnoDB invalidates the whole tx on deadlock,
// so retrying just the INSERT inside the dead tx would not help.
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
		taskID = sql.NullInt32{Int32: int32(*evt.TaskID), Valid: true} //#nosec G115 -- task_id is tasks.id (BIGINT UNSIGNED), fits int32 within realistic deployments
	}
	actorID := sql.NullInt32{}
	if evt.ActorUserID != nil {
		actorID = sql.NullInt32{Int32: int32(*evt.ActorUserID), Valid: true} //#nosec G115 -- actor user id sourced from session, fits int32 within realistic deployments
	}
	params := generated.AppendEventParams{
		PublicID:    types.New(),
		WorkspaceID: evt.WorkspaceID,
		TaskID:      taskID,
		ActorUserID: actorID,
		Type:        evt.Type,
		PayloadJson: raw,
		OccurredAt:  time.Now().UTC(),
	}
	var lastID int64
	insert := func(ctx context.Context) error {
		id, err := q.AppendEvent(ctx, params)
		if err != nil {
			return err
		}
		lastID = id
		return nil
	}
	var err error
	if _, isTx := db.(*sql.Tx); isTx {
		// Caller owns the transaction boundary; do not retry inside it.
		err = insert(ctx)
	} else {
		err = dbretry.Do(ctx, "eventbus.Append", insert)
	}
	if err != nil {
		slog.ErrorContext(ctx, "eventbus: append failed",
			"type", evt.Type,
			"workspace_id", evt.WorkspaceID,
			"error", err,
		)
		return err
	}
	// LastInsertId is a positive int64 produced by AUTO_INCREMENT; cast to
	// uint32 for the hook signature (events.id is INT UNSIGNED).
	eventInternalID := uint32(lastID) //#nosec G115 -- LastInsertId for events.id (BIGINT UNSIGNED), fits uint32 within realistic deployments
	seq := globalSeq.Add(1)
	seqCtx := WithSeq(ctx, seq)
	fireNotifyHooks(seqCtx, evt.WorkspaceID, evt.Type, eventInternalID)
	return nil
}
