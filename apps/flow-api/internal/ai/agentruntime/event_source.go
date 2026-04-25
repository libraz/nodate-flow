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
type OnEventAgentsQuerier interface {
	ListOnEventAgentsFor(ctx context.Context, workspaceID uint32, eventKind string) ([]OnEventAgentMatch, error)
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
// best-effort and never blocks the caller. The eventInternalID
// parameter is accepted for signature compatibility but unused: the
// dedupe key is built from agent_id + event_type + tick.
func (e *EventTrigger) NotifyHook() func(ctx context.Context, workspaceID uint32, eventType string, eventInternalID uint32) {
	return func(ctx context.Context, workspaceID uint32, eventType string, _ uint32) {
		if e == nil || e.Queries == nil || e.Queue == nil {
			return
		}
		e.dispatch(ctx, workspaceID, eventType)
	}
}

func (e *EventTrigger) dispatch(ctx context.Context, workspaceID uint32, eventType string) {
	if e.Now == nil {
		e.Now = time.Now
	}
	if e.Logger == nil {
		e.Logger = slog.Default()
	}
	rows, err := e.Queries.ListOnEventAgentsFor(ctx, workspaceID, eventType)
	if err != nil {
		e.Logger.Warn("on_event agent lookup failed", "err", err, "ws", workspaceID, "event", eventType)
		return
	}
	now := e.Now().UTC()
	for _, r := range rows {
		key := fmt.Sprintf("%d:event:%s:%d", r.ID, eventType, now.UnixNano())
		run := Run{
			DedupeKey:   key,
			Job:         Job{AgentID: r.ID, WsID: r.WorkspaceID},
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
