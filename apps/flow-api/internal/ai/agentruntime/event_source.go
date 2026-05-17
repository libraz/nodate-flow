package agentruntime

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"time"
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
	ListOnEventAgentsForEvent(ctx context.Context, workspaceID uint32, eventInternalID uint32) ([]OnEventAgentMatch, error)
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
}

// NotifyHook returns a closure compatible with eventbus.AddNotifyHook.
// The closure fires agent lookups off the request goroutine — it is
// best-effort and never blocks the caller. The eventInternalID is the
// events.id row that was just appended; the scoped lookup joins
// through it so schedule_scope='assigned_tasks' agents only wake when
// the source event is bound to a task they own.
func (e *EventTrigger) NotifyHook() func(ctx context.Context, workspaceID uint32, eventType string, eventInternalID uint32) {
	return func(ctx context.Context, workspaceID uint32, eventType string, eventInternalID uint32) {
		if e == nil || e.Queries == nil || e.Queue == nil {
			return
		}
		e.dispatch(ctx, workspaceID, eventType, eventInternalID)
	}
}

func (e *EventTrigger) dispatch(ctx context.Context, workspaceID uint32, eventType string, eventInternalID uint32) {
	if e.Now == nil {
		e.Now = time.Now
	}
	if e.Logger == nil {
		e.Logger = slog.Default()
	}
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
		e.Logger.Warn("on_event agent lookup failed", "err", err, "ws", workspaceID, "event", eventType)
		return
	}
	now := e.Now().UTC()
	for _, r := range rows {
		key := fmt.Sprintf("%d:event:%s:%d", r.ID, eventType, now.UnixNano())
		run := Run{
			DedupeKey:   key,
			Job:         Job{AgentID: r.ID, WsID: r.WorkspaceID, SourceEventID: eventInternalID, DedupeKey: key},
			ScheduledAt: now,
		}
		if err := e.Queue.Enqueue(ctx, run); err != nil && !errors.Is(err, ErrDuplicate) {
			e.Logger.Warn("on_event enqueue failed", "err", err, "agent", r.ID)
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

func (q *sqlcOnEventQuerier) ListOnEventAgentsForEvent(ctx context.Context, workspaceID uint32, eventInternalID uint32) ([]OnEventAgentMatch, error) {
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
