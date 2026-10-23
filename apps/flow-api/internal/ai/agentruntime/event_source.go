package agentruntime

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/libraz/nodate-flow/apps/flow-api/internal/bgloop"
)

// OnEventAgentsQuerier is the narrow contract [EventTrigger] needs
// from the sqlc bundle. Kept as an interface so unit tests can pass
// a fake without a real DB, and so this file stays import-free of
// the heavy generated package.
//
// ListOnEventAgentsForEvent resolves matching on_event agents for a
// specific appended events row id. It enforces ai_agents.schedule_scope:
// 'workspace' agents always match the event-type predicate, while
// 'assigned_tasks' agents additionally require an enabled task_actor
// (kind='agent') row tying them to the event's task_id. Events with a
// NULL task_id never wake an 'assigned_tasks' agent because there is
// no task scope to bind against.
type OnEventAgentsQuerier interface {
	ListOnEventAgentsFor(ctx context.Context, workspaceID uint32, eventKind string) ([]OnEventAgentMatch, error)
	ListOnEventAgentsForEvent(ctx context.Context, workspaceID uint32, eventInternalID uint64) ([]OnEventAgentMatch, error)
}

// OnEventAgentMatch is the narrow row returned from the on-event
// query: the internal agent id plus its workspace. The runner only
// needs these two to enqueue a Run.
type OnEventAgentMatch struct {
	ID          uint32
	WorkspaceID uint32
}

// EventTrigger bridges the eventbus notify hook to the agent
// runtime queue. For every qualifying event it looks up matching
// agents (schedule_kind='on_event' + event_trigger_types JSON_CONTAINS)
// and enqueues one Run per match. Duplicate enqueues are swallowed
// via [ErrDuplicate] so replaying the same event across replicas is
// safe.
type EventTrigger struct {
	Queries OnEventAgentsQuerier
	Queue   Queue
	Logger  *slog.Logger
	Now     func() time.Time
	// Runner is where NotifyHook submits its dispatches, normally the
	// runner the other event hooks share. Nil gives the trigger a
	// private one.
	Runner *bgloop.HookRunner

	runnerOnce sync.Once
}

// Bounds of the private runner used when no shared one is installed.
const (
	// defaultDispatchTimeout bounds a dispatch that outlives its
	// request. One lookup plus a handful of enqueues is a sub-second
	// operation; the budget only exists so a stuck query cannot leak a
	// goroutine forever.
	defaultDispatchTimeout = 30 * time.Second
	// defaultDispatchConcurrency caps how many dispatches hold a
	// database connection at once.
	defaultDispatchConcurrency = 8
)

// NotifyHook returns a closure compatible with eventbus.AddNotifyHook.
//
// The lookup and the enqueues run on the hook runner rather than inline:
// every event append would otherwise pay for one query plus an INSERT per
// matching agent before the request that caused it could return, and a
// slow query there would stall unrelated request handlers. The runner
// detaches the job from the request's cancellation, bounds how many hold
// a connection at once, and drains them at shutdown.
//
// The eventInternalID is the events.id row that was just appended; the
// scoped lookup joins through it so schedule_scope='assigned_tasks'
// agents only wake when the source event is bound to a task they own.
func (e *EventTrigger) NotifyHook() func(ctx context.Context, workspaceID uint32, eventType string, eventInternalID uint64) {
	return func(ctx context.Context, workspaceID uint32, eventType string, eventInternalID uint64) {
		if e == nil || e.Queries == nil || e.Queue == nil {
			return
		}
		e.hooks().Go(ctx, 0, func(runCtx context.Context) {
			e.dispatch(runCtx, workspaceID, eventType, eventInternalID)
		})
	}
}

// Shutdown stops accepting new dispatches and waits for the accepted
// ones, or until ctx is cancelled.
func (e *EventTrigger) Shutdown(ctx context.Context) error {
	return e.hooks().Shutdown(ctx)
}

// hooks returns the installed runner, or a private one when none was.
func (e *EventTrigger) hooks() *bgloop.HookRunner {
	e.runnerOnce.Do(func() {
		if e.Runner == nil {
			e.Runner = bgloop.NewHookRunner("agentruntime.on_event", defaultDispatchConcurrency, defaultDispatchTimeout)
		}
	})
	return e.Runner
}

func (e *EventTrigger) now() time.Time {
	if e.Now != nil {
		return e.Now()
	}
	return time.Now()
}

func (e *EventTrigger) logger() *slog.Logger {
	if e.Logger != nil {
		return e.Logger
	}
	return slog.Default()
}

func (e *EventTrigger) dispatch(ctx context.Context, workspaceID uint32, eventType string, eventInternalID uint64) {
	// The defaults are read, never written. Filling the fields lazily
	// was safe while this ran on the caller's goroutine one at a time;
	// now that several dispatches run at once it would be two
	// goroutines writing the same field.
	log := e.logger()
	// Prefer the event-scoped query when the eventInternalID is known
	// (every real eventbus.Append delivers one). Falls back to the
	// legacy workspace-wide lookup for callers that pass 0 — tests
	// historically use that shape and don't exercise schedule_scope.
	var (
		rows []OnEventAgentMatch
		err  error
	)
	if eventInternalID != 0 {
		rows, err = e.Queries.ListOnEventAgentsForEvent(ctx, workspaceID, eventInternalID)
	} else {
		rows, err = e.Queries.ListOnEventAgentsFor(ctx, workspaceID, eventType)
	}
	if err != nil {
		log.Warn("on_event agent lookup failed", "err", err, "ws", workspaceID, "event", eventType)
		return
	}
	now := e.now().UTC()
	for _, r := range rows {
		key := fmt.Sprintf("%d:event:%s:%d", r.ID, eventType, now.UnixNano())
		run := Run{
			DedupeKey:   key,
			Job:         Job{AgentID: r.ID, WsID: r.WorkspaceID, SourceEventID: eventInternalID, DedupeKey: key},
			ScheduledAt: now,
		}
		if err := e.Queue.Enqueue(ctx, run); err != nil && !errors.Is(err, ErrDuplicate) {
			log.Warn("on_event enqueue failed", "err", err, "agent", r.ID)
		}
	}
}

// Compile-time guard that the generated sqlc bundle will satisfy
// OnEventAgentsQuerier via the [NewSqlcOnEventQuerier] adapter below.
var _ OnEventAgentsQuerier = (*sqlcOnEventQuerier)(nil)

// sqlcOnEventQuerier adapts the generated ListOnEventAgents query
// (which uses row structs with a PublicID we don't need here) to
// the narrow [OnEventAgentsQuerier] interface agentruntime consumes.
type sqlcOnEventQuerier struct {
	db *sql.DB
}

// NewSqlcOnEventQuerier returns an [OnEventAgentsQuerier] backed by
// a raw SQL lookup. Using raw SQL rather than the generated query
// avoids pulling the sqlc package into the agentruntime import
// graph and keeps this package a pure "pull + dispatch" layer.
func NewSqlcOnEventQuerier(db *sql.DB) OnEventAgentsQuerier {
	return &sqlcOnEventQuerier{db: db}
}

const onEventAgentsQuery = `SELECT id, workspace_id
FROM ai_agents
WHERE enabled = TRUE
  AND paused = FALSE
  AND schedule_kind = 'on_event'
  AND workspace_id = ?
  AND JSON_CONTAINS(event_trigger_types, JSON_QUOTE(?))`

func (q *sqlcOnEventQuerier) ListOnEventAgentsFor(ctx context.Context, workspaceID uint32, eventKind string) ([]OnEventAgentMatch, error) {
	rows, err := q.db.QueryContext(ctx, onEventAgentsQuery, workspaceID, eventKind)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]OnEventAgentMatch, 0)
	for rows.Next() {
		var m OnEventAgentMatch
		if err := rows.Scan(&m.ID, &m.WorkspaceID); err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

// onEventAgentsForEventQuery resolves on_event agents bound to a
// specific events row, enforcing ai_agents.schedule_scope:
//
//   - 'workspace' (default) — matches any agent whose event_trigger_types
//     contains the event's type, mirroring [onEventAgentsQuery].
//   - 'assigned_tasks' — additionally requires an enabled task_actor
//     (kind='agent') tying the agent to the event's task_id. Events with
//     a NULL task_id never wake an assigned-task agent because there is
//     no task scope to bind against.
const onEventAgentsForEventQuery = `SELECT a.id, a.workspace_id
FROM ai_agents a
INNER JOIN events e ON e.id = ? AND e.workspace_id = a.workspace_id
WHERE a.enabled = TRUE
  AND a.paused = FALSE
  AND a.schedule_kind = 'on_event'
  AND a.workspace_id = ?
  AND JSON_CONTAINS(a.event_trigger_types, JSON_QUOTE(e.type))
  AND (
    a.schedule_scope = 'workspace'
    OR (
      a.schedule_scope = 'assigned_tasks'
      AND e.task_id IS NOT NULL
      AND EXISTS (
        SELECT 1 FROM task_actors ta
        WHERE ta.workspace_id = a.workspace_id
          AND ta.task_id = e.task_id
          AND ta.agent_id = a.id
          AND ta.kind = 'agent'
          AND ta.enabled = TRUE
      )
    )
  )`

func (q *sqlcOnEventQuerier) ListOnEventAgentsForEvent(ctx context.Context, workspaceID uint32, eventInternalID uint64) ([]OnEventAgentMatch, error) {
	rows, err := q.db.QueryContext(ctx, onEventAgentsForEventQuery, eventInternalID, workspaceID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]OnEventAgentMatch, 0)
	for rows.Next() {
		var m OnEventAgentMatch
		if err := rows.Scan(&m.ID, &m.WorkspaceID); err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}
