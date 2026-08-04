// Package eventbus provides a thin helper for appending rows to the
// append-only events table. It is intentionally tiny: handlers call
// [Append] inside the same request flow that mutates state so that the
// audit trail and the state change live and die together.
//
// The events table itself has no UPDATE/DELETE path; only purgeWorkspace
// removes rows. See sql/core/tables/events.sql.
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
	apierrors "github.com/nodate-flow/nodate-flow/apps/flow-api/internal/errors"
)

// validateActors enforces the three-way actor exclusion rule documented on
// [Event] and `sql/core/tables/events.sql`. At most one of ActorUserID,
// ActorAgentID, ActorSystemSource may be set per call; zero is allowed and
// represents the legacy "system actor" path. Returns an APIError carrying
// INTERNAL.EVENTBUS.ACTOR_MULTIPLE when more than one is set, so the
// caller can branch on the typed error and the API surface emits the
// canonical RFC 9457 envelope.
//
// The function is a pure predicate to keep it trivially testable; [Append]
// is the only production caller today, but the helper is exported across
// the file so future eventbus extension points (Phase 5 worker append
// path, judge Applier) reuse the same guard.
func validateActors(evt Event) error {
	set := 0
	if evt.ActorUserID != nil {
		set++
	}
	if evt.ActorAgentID != nil {
		set++
	}
	if evt.ActorSystemSource != "" {
		set++
	}
	if set > 1 {
		return apierrors.New(apierrors.InternalEventbusActorMultiple).
			WithDetail("actor_user_id_set", evt.ActorUserID != nil).
			WithDetail("actor_agent_id_set", evt.ActorAgentID != nil).
			WithDetail("actor_system_source_set", evt.ActorSystemSource != "")
	}
	return nil
}

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
//
// Actor exclusion (ADR 0008 D8). The events row supports three mutually
// exclusive actor sources — [Event.ActorUserID], [Event.ActorAgentID],
// and [Event.ActorSystemSource]. MySQL 8.4 cannot enforce this via
// CHECK because all three FK referential actions use ON DELETE SET NULL
// (`sql/core/tables/events.sql:46-48`), so [Append] validates the three-way
// exclusion at the Go layer and rejects rows that set more than one.
// Setting zero actors is still allowed and represents the legacy
// "system actor" path used by reconciliation jobs that predate the
// system-source column.
type Event struct {
	// Type is the canonical dotted event name, e.g. "task.created".
	Type string
	// WorkspaceID is the internal workspace id (never the public id).
	WorkspaceID uint32
	// ActorUserID is the internal user id of the actor; nil for system.
	ActorUserID *int64
	// ActorAgentID is the internal ai_agents.id of the agent that
	// produced this event. Today the orchestrator runner writes
	// agent-actor events directly through AppendAgentEvent so this
	// field is reserved for callers that go through [Append] with an
	// agent context. Mutually exclusive with the other two actor
	// fields.
	ActorAgentID *int64
	// ActorSystemSource names the worker-tick origin (`worker.calendar`,
	// `worker.retention`, ...) when the event was emitted by the worker
	// binary (ADR 0008 D8). Not an FK because the worker is not
	// represented in the database. Mutually exclusive with the other
	// two actor fields. The Phase 5 worker is the first writer; this
	// field exists today so the eventbus contract is stable when that
	// landing happens.
	ActorSystemSource string
	// TaskID is the internal task id when the event targets a task.
	TaskID *int64
	// ProjectID is reserved for future use; events.project_id is not yet
	// part of the schema, so this field is currently ignored by [Append].
	ProjectID *int64
	// TriggeredBySignalID is the internal signals.id when the event was
	// emitted by the Applier in response to a judged signal (ADR 0008
	// D4). Nil for events with no signal lineage. Provides the
	// traceability link surfaced as `triggeredBySignalId` on the
	// timeline DTO so the UI can render the causal chain "signal -> judge
	// verdict -> task event".
	TriggeredBySignalID *int64
	// ReversesEventID is the internal events.id this row compensates
	// (ADR 0008 D4 / J5). Set by the reversal handler when appending a
	// compensating event so the derived_state projection can cancel the
	// original out without ever UPDATEing the immutable event log. Nil
	// for normal (non-reversal) callers — every other writer leaves
	// this unset.
	ReversesEventID *int64
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
	notifyMu      sync.RWMutex
	notifyHooks   = map[int]NotifyHook{}
	notifyHookSeq int
)

// AddNotifyHook registers a subscriber on the notify fan-out and
// returns a stable handle for [RemoveNotifyHook]. Registration is
// purely additive: the SSE tap, notification fan-out, MCP event
// source, on_event triggers, and webhook worker all coexist off the
// same dispatch, so each subscriber must be able to unregister itself
// without disturbing the others. The handle stays valid regardless of
// how many other subscribers are added or removed in the meantime,
// which is what lets several servers share the process-global registry
// (the test harness runs a long-lived shared server alongside
// short-lived per-test servers) without clobbering each other's hooks.
func AddNotifyHook(hook NotifyHook) int {
	notifyMu.Lock()
	defer notifyMu.Unlock()
	notifyHookSeq++
	handle := notifyHookSeq
	notifyHooks[handle] = hook
	return handle
}

// RemoveNotifyHook unregisters the subscriber previously registered
// under handle. Unknown or already-removed handles are ignored so
// double teardown is safe.
func RemoveNotifyHook(handle int) {
	notifyMu.Lock()
	defer notifyMu.Unlock()
	delete(notifyHooks, handle)
}

// ClearNotifyHooks drops every registered subscriber. Used by tests
// that want a clean slate between runs.
func ClearNotifyHooks() {
	notifyMu.Lock()
	defer notifyMu.Unlock()
	notifyHooks = map[int]NotifyHook{}
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
	hooks := make([]NotifyHook, 0, len(notifyHooks))
	for _, h := range notifyHooks {
		hooks = append(hooks, h)
	}
	notifyMu.RUnlock()
	for _, h := range hooks {
		h(ctx, workspaceInternalID, eventType, eventInternalID)
	}
}

// judgeEventKinds enumerates the event kinds that may only be emitted
// from the signaljudge Applier (ADR 0008 D4). [Append] rejects any
// caller that tries to use them so the Applier remains the sole writer
// of judge-driven task state. The Applier itself uses [AppendJudgeEvent]
// to bypass this gate while still going through the shared INSERT path.
//
// Keep this set in sync with the Kind constants in
// packages/go-shared/eventbus/kinds.go — specifically the
// "Task events driven by the signaljudge Applier" and the
// SignalJudged / SignalApplied / SignalRejected entries under
// "Signal events". SignalAttached is intentionally NOT in this set
// because it is emitted by the public POST /signals handler before
// any judge run exists.
var judgeEventKinds = map[string]bool{
	TaskAutoCompleted: true,
	TaskRetroDrafted:  true,
	SignalJudged:      true,
	SignalApplied:     true,
	SignalRejected:    true,
}

// IsJudgeEventKind reports whether t is one of the event kinds reserved
// for the signaljudge Applier. Exposed so callers (tests, the Applier
// itself) can assert the set without duplicating the literal map.
func IsJudgeEventKind(t string) bool { return judgeEventKinds[t] }

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
//
// Judge-kind guard (ADR 0008 D4). Event kinds listed in
// [judgeEventKinds] are reserved for the signaljudge Applier. Any
// other caller using [Append] with one of those kinds short-circuits
// with INTERNAL.EVENTBUS.JUDGE_KIND_OUTSIDE_APPLIER so the invariant
// "Applier is the sole writer of judge-driven task state" is enforced
// at runtime rather than relying on grep / code review. The Applier
// itself uses [AppendJudgeEvent] which sets an internal flag so the
// guard lets the row through.
func Append(ctx context.Context, db DBTX, evt Event) error {
	return appendInternal(ctx, db, evt, false)
}

// AppendJudgeEvent is the signaljudge Applier's entry point into the
// shared INSERT path. Behaves identically to [Append] for non-judge
// kinds but additionally permits the kinds listed in [judgeEventKinds]
// (ADR 0008 D4).
//
// Restricted contract: this function MUST only be called from
// apps/flow-api/internal/ai/signaljudge/. The runtime guard in
// [Append] is the gate; this function is the bypass for that gate and
// exists so the Applier shares one INSERT path (and one deadlock
// retry, one actor-exclusion check, one notify fan-out, ...) with
// every other event writer.
func AppendJudgeEvent(ctx context.Context, db DBTX, evt Event) error {
	return appendInternal(ctx, db, evt, true)
}

// ReverseAppendResult is the metadata the reversal handler returns
// from the appended compensating event row. PublicID is the UUID v7
// emitted as the new event's id (the value the HTTP response carries
// as `publicId`). OccurredAt is the wall-clock instant the row was
// stamped with (unix seconds on the wire — the handler does the
// translation).
type ReverseAppendResult struct {
	PublicID   types.PublicID
	OccurredAt time.Time
}

// AppendReverseEvent is the dedicated entry point for the J5 reversal
// flow (ADR 0008 D4 / `POST /workspaces/{wsId}/events/{eventPublicId}/reverse`).
// It bypasses the judge-kind guard because reversing a judge-only
// event (e.g. `task.auto_completed`) is, by construction, a
// user-driven exception to the "Applier is the sole writer" rule —
// the reverser is the user undoing the Applier's prior action and the
// compensating event re-uses the original event's type so the
// projection can cancel both rows out symmetrically.
//
// Restricted contract: this function MUST only be called from
// apps/flow-api/internal/http/handlers/events/ (the J5 reversal
// handler). Every other writer goes through [Append] / [AppendJudgeEvent]
// so the judge-kind guard stays effective for accidental misuse. The
// three-way actor exclusion ([validateActors]) still runs, and
// ReversesEventID must be set or the call is a logic error on the
// caller's part (the caller is expected to have already loaded the
// target via queries.FindEventForReverse).
//
// Returns the new event's public_id + occurred_at so the handler can
// echo them in the 201 response without a follow-up SELECT.
func AppendReverseEvent(ctx context.Context, db DBTX, evt Event) (ReverseAppendResult, error) {
	return appendInternalWithMeta(ctx, db, evt, true)
}

// appendInternal is the shared implementation of [Append] and
// [AppendJudgeEvent]. fromApplier signals that the caller is the
// signaljudge Applier and judge-kind events should pass the guard.
func appendInternal(ctx context.Context, db DBTX, evt Event, fromApplier bool) error {
	_, err := appendInternalWithMeta(ctx, db, evt, fromApplier)
	return err
}

// appendInternalWithMeta is the shared implementation that also
// surfaces the new event's public_id + occurred_at to callers that
// need to echo them back (currently only [AppendReverseEvent]).
// Everything else goes through [appendInternal] which discards the
// metadata to keep the existing zero-result contract.
func appendInternalWithMeta(ctx context.Context, db DBTX, evt Event, fromApplier bool) (ReverseAppendResult, error) {
	// Judge-kind guard (ADR 0008 D4). See judgeEventKinds for the set.
	// Reject early so we do not log noise or wake notify hooks for an
	// event the Applier-only invariant says should never reach the
	// INSERT path from this caller.
	if !fromApplier && judgeEventKinds[evt.Type] {
		err := apierrors.New(apierrors.InternalEventbusJudgeKindOutsideApplier).
			WithDetail("type", evt.Type).
			WithDetail("workspace_id", evt.WorkspaceID)
		slog.ErrorContext(ctx, "eventbus: judge-kind appended outside Applier",
			"type", evt.Type,
			"workspace_id", evt.WorkspaceID,
			"error", err,
		)
		return ReverseAppendResult{}, err
	}
	// Three-way actor exclusion (ADR 0008 D8). MySQL cannot enforce this
	// via CHECK constraint because all three actor FKs are declared with
	// ON DELETE SET NULL — MySQL 8.4 forbids CHECK constraints that
	// reference columns used in FK referential actions. The guard lives
	// here so every Append() caller (handlers, MCP tools, the future
	// worker append path) shares the same rail.
	if err := validateActors(evt); err != nil {
		slog.ErrorContext(ctx, "eventbus: actor exclusion violated",
			"type", evt.Type,
			"workspace_id", evt.WorkspaceID,
			"error", err,
		)
		return ReverseAppendResult{}, err
	}
	var raw json.RawMessage
	if evt.Payload == nil {
		raw = json.RawMessage(`{}`)
	} else {
		b, err := json.Marshal(evt.Payload)
		if err != nil {
			return ReverseAppendResult{}, fmt.Errorf("eventbus: marshal payload: %w", err)
		}
		raw = b
	}
	q := generated.New(db)
	taskID := sql.NullInt32{}
	if evt.TaskID != nil {
		taskID = sql.NullInt32{Int32: int32(*evt.TaskID), Valid: true} //#nosec G115 -- task_id is tasks.id (BIGINT UNSIGNED), fits int32 within realistic deployments
	}
	signalID := sql.NullInt32{}
	if evt.TriggeredBySignalID != nil {
		signalID = sql.NullInt32{Int32: int32(*evt.TriggeredBySignalID), Valid: true} //#nosec G115 -- signals.id is INT UNSIGNED, fits int32 within realistic deployments
	}
	reversesID := sql.NullInt64{}
	if evt.ReversesEventID != nil {
		reversesID = sql.NullInt64{Int64: *evt.ReversesEventID, Valid: true}
	}
	now := time.Now().UTC()
	pubID := types.New()
	// Branch on actor kind. The three INSERTs map 1:1 onto the three
	// actor sources documented on [Event] and `sql/core/tables/events.sql`:
	//   ActorAgentID set -> AppendAgentEvent (binds actor_agent_id,
	//                       carries triggered_by_signal_id for Applier
	//                       writes — ADR 0008 D4)
	//   otherwise        -> AppendEvent (binds actor_user_id +
	//                       actor_system_source, the latter being the
	//                       worker-tick path per ADR 0008 D8)
	// validateActors above has already rejected any row that set more
	// than one source, so the branch is decidable without further
	// guards.
	var insert func(ctx context.Context) error
	var lastID int64
	if evt.ActorAgentID != nil {
		agentID := sql.NullInt32{Int32: int32(*evt.ActorAgentID), Valid: true} //#nosec G115 -- ai_agents.id is INT UNSIGNED, fits int32 within realistic deployments
		agentParams := generated.AppendAgentEventParams{
			PublicID:            pubID,
			WorkspaceID:         evt.WorkspaceID,
			TaskID:              taskID,
			ActorAgentID:        agentID,
			TriggeredBySignalID: signalID,
			ReversesEventID:     reversesID,
			Type:                evt.Type,
			PayloadJson:         raw,
			OccurredAt:          now,
		}
		insert = func(ctx context.Context) error {
			id, err := q.AppendAgentEvent(ctx, agentParams)
			if err != nil {
				return err
			}
			lastID = id
			return nil
		}
	} else {
		actorID := sql.NullInt32{}
		if evt.ActorUserID != nil {
			actorID = sql.NullInt32{Int32: int32(*evt.ActorUserID), Valid: true} //#nosec G115 -- actor user id sourced from session, fits int32 within realistic deployments
		}
		sysSource := sql.NullString{}
		if evt.ActorSystemSource != "" {
			sysSource = sql.NullString{String: evt.ActorSystemSource, Valid: true}
		}
		params := generated.AppendEventParams{
			PublicID:            pubID,
			WorkspaceID:         evt.WorkspaceID,
			TaskID:              taskID,
			ActorUserID:         actorID,
			ActorSystemSource:   sysSource,
			TriggeredBySignalID: signalID,
			ReversesEventID:     reversesID,
			Type:                evt.Type,
			PayloadJson:         raw,
			OccurredAt:          now,
		}
		insert = func(ctx context.Context) error {
			id, err := q.AppendEvent(ctx, params)
			if err != nil {
				return err
			}
			lastID = id
			return nil
		}
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
		return ReverseAppendResult{}, err
	}
	// LastInsertId is a positive int64 produced by AUTO_INCREMENT; cast to
	// uint32 for the hook signature (events.id is INT UNSIGNED).
	eventInternalID := uint32(lastID) //#nosec G115 -- LastInsertId for events.id (BIGINT UNSIGNED), fits uint32 within realistic deployments
	seq := globalSeq.Add(1)
	seqCtx := WithSeq(ctx, seq)
	// Fan-out (SSE tap, notification goroutines, on_event triggers) must
	// observe a committed row, and must not run while this call's
	// enclosing transaction still holds locks on the just-inserted rows
	// — the notification goroutine writes rows that FK-reference the new
	// event/task and would otherwise block on this transaction's own
	// locks, a self-inflicted deadlock that InnoDB resolves by rolling
	// one side back. AddCommitHook defers the fan-out until the
	// transaction commits when the caller ran us via dbretry.InTx; on
	// the auto-commit path (and for callers managing their own
	// transaction) it fires immediately, preserving prior behavior.
	dbretry.AddCommitHook(seqCtx, func() {
		fireNotifyHooks(seqCtx, evt.WorkspaceID, evt.Type, eventInternalID)
	})
	return ReverseAppendResult{PublicID: pubID, OccurredAt: now}, nil
}
