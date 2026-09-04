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
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"

	"github.com/go-sql-driver/mysql"

	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/dbretry"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/generated"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/types"
	apierrors "github.com/libraz/nodate-flow/apps/flow-api/internal/errors"
	sharedbus "github.com/libraz/nodate-flow/packages/go-shared/eventbus"
	"github.com/libraz/nodate-flow/packages/go-shared/eventlog"
)

// mysqlDuplicateEntry is ER_DUP_ENTRY (1062).
const mysqlDuplicateEntry uint16 = 1062

// ErrAlreadyReversed is returned by [AppendReverseEvent] when a
// concurrent request already recorded a compensating event for the same
// target. The UNIQUE (workspace_id, reverses_event_id) index on `events`
// is the authority: the pre-check in the reversal handler can be passed
// by two requests at once, and exactly one INSERT survives.
//
// This is a resolved race, not a lost write — the log ends up holding
// the one compensating row it is supposed to — so callers should map it
// onto their "already reversed" answer rather than an internal error.
var ErrAlreadyReversed = errors.New("eventbus: event already reversed")

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
// the file so future eventbus extension points (the worker append
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

// isDuplicateEntry reports whether err is MySQL's ER_DUP_ENTRY. Kept
// local so the eventbus does not depend on the HTTP handler helpers.
func isDuplicateEntry(err error) bool {
	var me *mysql.MySQLError
	return errors.As(err, &me) && me.Number == mysqlDuplicateEntry
}

// workspaceSeq holds one monotonically increasing counter per
// workspace. [fireNotifyHooks] stamps each notification with the next
// value for the workspace the event belongs to, and SSE subscribers
// carry it in their payload so clients can detect gaps and reorder
// events that arrive out of order from concurrent goroutines.
//
// The counter is per workspace because a subscriber only ever sees one
// workspace's events. A single process-wide counter would show every
// other tenant's traffic as a gap — which both makes gap detection
// meaningless in a multi-tenant deployment and lets a client read the
// rest of the instance's activity rate off its own stream. Each
// workspace instead sees a dense 1, 2, 3... of its own events.
//
// A sync.Map keeps the dispatch path off a process-wide lock: counters
// are looked up far more often than they are created, and the keyspace
// is bounded by the number of workspaces this process has served.
var workspaceSeq sync.Map // map[uint32]*atomic.Int64

// nextSeq returns the next sequence number for workspaceInternalID,
// starting at 1 for a workspace this process has not seen before.
func nextSeq(workspaceInternalID uint32) int64 {
	counter, ok := workspaceSeq.Load(workspaceInternalID)
	if !ok {
		counter, _ = workspaceSeq.LoadOrStore(workspaceInternalID, &atomic.Int64{})
	}
	return counter.(*atomic.Int64).Add(1)
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
	// Type is the canonical event kind, e.g. [TaskCreated]. A raw
	// string is not accepted: an event kind nothing subscribes to is
	// indistinguishable from a correct one at the call site and only
	// surfaces later as a missing notification.
	Type Kind
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
	// two actor fields. The worker binary is the first writer; this
	// field exists today so the eventbus contract is stable when that
	// landing happens.
	ActorSystemSource string
	// TaskID is the internal task id when the event targets a task.
	TaskID *int64
	// CalendarID is the internal calendar id when the event happened
	// inside a calendar -- the symmetric counterpart of TaskID. It is
	// what makes a per-calendar activity feed readable straight off the
	// event log instead of from a second history table that would drift
	// from it. Nil for events with no calendar subject.
	CalendarID *int64
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
	// (ADR 0008 D4). Set by the reversal handler when appending a
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
// It is uint64 rather than the uint32 used for every other internal
// id because events.id is BIGINT UNSIGNED: the log is append-only and
// unbounded, so it is the one id expected to outgrow 32 bits. Passing
// it narrower would silently rewrite source_event_id — the key that
// makes notification delivery at-least-once rather than at-most-once.
//
// Hooks must be non-blocking and must never panic. Leave unset for
// the no-op behaviour.
type NotifyHook = func(ctx context.Context, workspaceInternalID uint32, eventType string, eventInternalID uint64)

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

// SeqFromContext returns the event sequence number set by
// [fireNotifyHooks], or zero if the context was not tagged.
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

// fireNotifyHooks dispatches one event to every registered subscriber,
// stamping the context with the event's sequence number on the way.
//
// The stamp lives here because this is the one point both appenders
// reach: [Append] calls it directly and the eventlog bridge forwards
// into it (see bridge.go). Numbering at either appender leaves the
// other's events unnumbered, and numbering at both leaves the two
// sequences disagreeing about what comes next.
func fireNotifyHooks(ctx context.Context, workspaceInternalID uint32, eventType string, eventInternalID uint64) {
	ctx = WithSeq(ctx, nextSeq(workspaceInternalID))
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

// IsJudgeEventKind reports whether t is one of the event kinds reserved
// for the signaljudge Applier (ADR 0008 D4). [Append] rejects any other
// caller that tries to use them so the Applier remains the sole writer
// of judge-driven task state; the Applier itself uses [AppendJudgeEvent]
// to bypass this gate while still going through the shared INSERT path.
//
// The set is declared beside the constants themselves
// (packages/go-shared/eventbus) rather than restated here. A local copy
// kept in step by a comment leaves a kind added to the judge loop
// appendable by anyone until somebody notices the two have drifted, and
// nothing in this file would say so.
func IsJudgeEventKind(t Kind) bool { return sharedbus.IsJudgeOnly(t) }

// Append inserts a single event row through db. With a [dbretry.Tx] the
// event is part of that transaction and the fan-out waits for its
// commit; with [dbretry.AutoCommit] the row is durable on return and the
// fan-out runs immediately.
//
// db is a [dbretry.CommitBoundary] rather than a bare statement
// executor because the fan-out this append triggers has to observe a
// committed row. A transaction whose commit nobody reports would leave
// every subscriber with an event they cannot read, so that handle is not
// expressible here.
//
// The retry policy comes from the handle for the same reason it has to:
// on auto-commit the INSERT is its own transaction and re-issuing it
// clears the FK-lock contention parallel writers produce, while inside a
// transaction a deadlock has already invalidated everything and only the
// caller's [dbretry.InTx] can retry the unit that matters.
//
// Judge-kind guard (ADR 0008 D4). Event kinds listed in
// [IsJudgeEventKind] are reserved for the signaljudge Applier. Any
// other caller using [Append] with one of those kinds short-circuits
// with INTERNAL.EVENTBUS.JUDGE_KIND_OUTSIDE_APPLIER so the invariant
// "Applier is the sole writer of judge-driven task state" is enforced
// at runtime rather than relying on grep / code review. The Applier
// itself uses [AppendJudgeEvent] which sets an internal flag so the
// guard lets the row through.
func Append(ctx context.Context, db dbretry.CommitBoundary, evt Event) error {
	return appendInternal(ctx, db, evt, appendMode{})
}

// AppendBestEffort appends evt and, when the INSERT fails, records what
// was lost instead of failing the caller's operation.
//
// It exists so that "this call site accepts a dropped event" is written
// down and greppable. Assigning the error to the blank identifier is the
// same behaviour with none of the information: no reason, no call site,
// and no payload, which leaves nothing to replay from. Choosing between
// the two forms is a real decision, so a static test rejects the
// discarded one (see no_swallowed_append_test.go).
//
// What decides between the two forms is the boundary, not the mutation.
//
// Call [Append] and propagate when the append shares the mutation's
// transaction. Failing there takes the mutation down with it, which is
// the outcome that keeps the two in step, and [dbretry.InTx] enforces it
// whatever the call site does with the returned error.
//
// Use this form when the mutation is already durable — an append that
// follows a committed transaction, or one issued on
// [dbretry.AutoCommit] after the write it describes. There is nothing
// left to undo, so reporting failure would tell the client nothing
// happened when it did, and would skip whatever the handler still had to
// do afterwards, typically its audit entry.
//
// Anything a projection reads must be on the first path. `derived_state`
// is built from the `task.transition.*` kinds and no others (see
// internal/constraint/engine/replay.go), and those appends are made
// inside the transaction that moves the task; losing one is a wrong
// state that nothing later corrects. Every other kind is a notification
// or a timeline entry, where losing one costs a delivery.
//
// callSite names the operation, e.g. "mcp.create_task". The payload is
// logged alongside it because the row does not exist: this line is the
// only remaining description of the change, and any state derived from
// the log stays wrong until someone replays it.
func AppendBestEffort(ctx context.Context, db dbretry.CommitBoundary, evt Event, callSite string) {
	if err := appendInternal(ctx, db, evt, appendMode{bestEffort: true}); err != nil {
		// Append already logged the driver error; this line adds what it
		// could not know — who dropped it and what the row would have
		// said.
		attrs := []any{
			slog.String("call_site", callSite),
			slog.String("type", string(evt.Type)),
			slog.Uint64("workspace_id", uint64(evt.WorkspaceID)),
			slog.Any("error", err),
		}
		if evt.TaskID != nil {
			attrs = append(attrs, slog.Int64("task_id", *evt.TaskID))
		}
		if payload, merr := json.Marshal(evt.Payload); merr == nil {
			attrs = append(attrs, slog.String("payload", string(payload)))
		}
		slog.ErrorContext(ctx, "eventbus: event dropped; state derived from the log is incomplete until it is replayed", attrs...)
	}
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
func AppendJudgeEvent(ctx context.Context, db dbretry.CommitBoundary, evt Event) error {
	return appendInternal(ctx, db, evt, appendMode{fromApplier: true})
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

// AppendReverseEvent is the dedicated entry point for the reversal
// flow (ADR 0008 D4 / `POST /workspaces/{wsId}/events/{eventPublicId}/reverse`).
// It bypasses the judge-kind guard because reversing a judge-only
// event (e.g. `task.auto_completed`) is, by construction, a
// user-driven exception to the "Applier is the sole writer" rule —
// the reverser is the user undoing the Applier's prior action and the
// compensating event re-uses the original event's type so the
// projection can cancel both rows out symmetrically.
//
// Restricted contract: this function MUST only be called from
// apps/flow-api/internal/http/handlers/events/ (the reversal
// handler). Every other writer goes through [Append] / [AppendJudgeEvent]
// so the judge-kind guard stays effective for accidental misuse. The
// three-way actor exclusion ([validateActors]) still runs, and
// ReversesEventID must be set or the call is a logic error on the
// caller's part (the caller is expected to have already loaded the
// target via queries.FindEventForReverse).
//
// Returns the new event's public_id + occurred_at so the handler can
// echo them in the 201 response without a follow-up SELECT.
func AppendReverseEvent(ctx context.Context, db dbretry.CommitBoundary, evt Event) (ReverseAppendResult, error) {
	return appendInternalWithMeta(ctx, db, evt, appendMode{fromApplier: true})
}

// appendMode carries what the entry points differ by, so the shared
// implementation reads as the property it is branching on rather than as
// a pair of positional booleans.
type appendMode struct {
	// fromApplier signals that the caller is the signaljudge Applier and
	// judge-kind events should pass the guard.
	fromApplier bool
	// bestEffort marks the one entry point that accepts a dropped row.
	// Every other one reports its failure to the commit boundary, which
	// refuses to commit a transaction that lost an event; best effort is
	// the deliberate exception and must stay able to fail inside a
	// transaction without taking it down.
	bestEffort bool
}

// appendInternal is the shared implementation of [Append],
// [AppendBestEffort] and [AppendJudgeEvent].
func appendInternal(ctx context.Context, db dbretry.CommitBoundary, evt Event, mode appendMode) error {
	_, err := appendInternalWithMeta(ctx, db, evt, mode)
	return err
}

// appendInternalWithMeta is the shared implementation that also
// surfaces the new event's public_id + occurred_at to callers that
// need to echo them back (currently only [AppendReverseEvent]).
// Everything else goes through [appendInternal] which discards the
// metadata to keep the existing zero-result contract.
//
// Every failure is reported to db through [dbretry.CommitBoundary.Fail]
// unless the caller asked for best effort. Task state is derived from
// the event log (CLAUDE.md rule 8), so a transaction that mutated a task
// and then failed to record why must not commit the mutation on its own
// — and whether it does is not the call site's to decide.
func appendInternalWithMeta(ctx context.Context, db dbretry.CommitBoundary, evt Event, mode appendMode) (ReverseAppendResult, error) {
	// fail reports the failure to the commit boundary on the way out. In a
	// transaction that refuses the commit, so discarding the returned
	// error does not buy the caller a mutation without the event that
	// describes it. [AppendBestEffort] is the one entry point exempt from
	// this: accepting a dropped row is what it is for.
	fail := func(err error) (ReverseAppendResult, error) {
		if !mode.bestEffort {
			db.Fail(err)
		}
		return ReverseAppendResult{}, err
	}
	// Judge-kind guard (ADR 0008 D4). See IsJudgeEventKind for the set.
	// Reject early so we do not log noise or wake notify hooks for an
	// event the Applier-only invariant says should never reach the
	// INSERT path from this caller.
	if !mode.fromApplier && IsJudgeEventKind(evt.Type) {
		err := apierrors.New(apierrors.InternalEventbusJudgeKindOutsideApplier).
			WithDetail("type", evt.Type).
			WithDetail("workspace_id", evt.WorkspaceID)
		slog.ErrorContext(ctx, "eventbus: judge-kind appended outside Applier",
			"type", evt.Type,
			"workspace_id", evt.WorkspaceID,
			"error", err,
		)
		return fail(err)
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
		return fail(err)
	}
	// Internal ids must not reach the payload. The rail lives on the
	// append path, shared with eventlog.Append, because a payload map is
	// assembled at runtime and only the finished value says what will be
	// stored. See eventlog.ValidatePayloadIDs.
	if err := eventlog.ValidatePayloadIDs(evt.Payload); err != nil {
		slog.ErrorContext(ctx, "eventbus: payload carries internal identifiers",
			"type", evt.Type,
			"workspace_id", evt.WorkspaceID,
			"error", err,
		)
		return fail(err)
	}
	var raw json.RawMessage
	if evt.Payload == nil {
		raw = json.RawMessage(`{}`)
	} else {
		b, err := json.Marshal(evt.Payload)
		if err != nil {
			return fail(fmt.Errorf("eventbus: marshal payload: %w", err))
		}
		raw = b
	}
	q := generated.New(db)
	taskID := sql.NullInt32{}
	if evt.TaskID != nil {
		taskID = sql.NullInt32{Int32: int32(*evt.TaskID), Valid: true} //#nosec G115 -- task_id is tasks.id (BIGINT UNSIGNED), fits int32 within realistic deployments
	}
	calendarID := sql.NullInt32{}
	if evt.CalendarID != nil {
		calendarID = sql.NullInt32{Int32: int32(*evt.CalendarID), Valid: true} //#nosec G115 -- calendars.id is INT UNSIGNED, fits int32 within realistic deployments
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
			CalendarID:          calendarID,
			ActorAgentID:        agentID,
			TriggeredBySignalID: signalID,
			ReversesEventID:     reversesID,
			Type:                string(evt.Type),
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
			CalendarID:          calendarID,
			ActorUserID:         actorID,
			ActorSystemSource:   sysSource,
			TriggeredBySignalID: signalID,
			ReversesEventID:     reversesID,
			Type:                string(evt.Type),
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
	// The handle decides whether the INSERT may be retried on its own: a
	// transaction is invalidated whole by a deadlock, so only the
	// auto-commit path can re-issue the statement.
	err := db.RunStatement(ctx, "eventbus.Append", insert)
	if err != nil {
		if evt.ReversesEventID != nil && isDuplicateEntry(err) {
			// Two requests reversed the same event at once and the
			// UNIQUE (workspace_id, reverses_event_id) index rejected
			// the second INSERT. Nothing was lost: the compensating row
			// the loser wanted is already in the log, written by the
			// winner. Report it as its own condition so the handler can
			// answer idempotently, and log it at info — an operator
			// paged by "append failed" would find a log that is exactly
			// as it should be. Both the sentinel and the driver error
			// stay reachable via errors.Is / errors.As.
			slog.InfoContext(ctx, "eventbus: reverse already recorded by a concurrent request",
				"type", evt.Type,
				"workspace_id", evt.WorkspaceID,
				"reverses_event_id", *evt.ReversesEventID,
			)
			return fail(fmt.Errorf("%w (%w)", ErrAlreadyReversed, err))
		}
		slog.ErrorContext(ctx, "eventbus: append failed",
			"type", evt.Type,
			"workspace_id", evt.WorkspaceID,
			"error", err,
		)
		return fail(err)
	}
	// LastInsertId is a positive int64 produced by AUTO_INCREMENT, and
	// events.id is BIGINT UNSIGNED, so uint64 carries it without loss.
	eventInternalID := uint64(lastID) //#nosec G115 -- AUTO_INCREMENT LastInsertId is non-negative
	// Fan-out (SSE tap, notification goroutines, on_event triggers) must
	// observe a committed row, and must not run while this call's
	// enclosing transaction still holds locks on the just-inserted rows
	// — the notification goroutine writes rows that FK-reference the new
	// event/task and would otherwise block on this transaction's own
	// locks, a self-inflicted deadlock that InnoDB resolves by rolling
	// one side back. AfterCommit holds the dispatch until the
	// transaction commits; on the auto-commit path the row is already
	// durable, so it fires immediately.
	//
	// A transaction with no observable commit has neither property, and
	// waking subscribers there hands them an id nothing else can resolve
	// yet — the delivery looks sent and arrives nowhere. There is no
	// branch for that case because [dbretry.CommitBoundary] leaves no way
	// to reach it.
	db.AfterCommit(func() {
		fireNotifyHooks(ctx, evt.WorkspaceID, string(evt.Type), eventInternalID)
	})
	return ReverseAppendResult{PublicID: pubID, OccurredAt: now}, nil
}
