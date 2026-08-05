package agentruntime

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"sync"
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

	// DispatchTimeout caps one detached dispatch. Zero means
	// [defaultDispatchTimeout].
	DispatchTimeout time.Duration
	// DispatchConcurrency caps how many dispatches touch the database
	// at once. Zero means [defaultDispatchConcurrency].
	DispatchConcurrency int

	semOnce  sync.Once
	sem      chan struct{}
	wg       sync.WaitGroup
	stopMu   sync.RWMutex
	stopping bool
}

// Defaults for the detached dispatch.
const (
	// defaultDispatchTimeout bounds a dispatch that outlives its
	// request. One lookup plus a handful of enqueues is a sub-second
	// operation; the budget only exists so a stuck query cannot leak a
	// goroutine forever.
	defaultDispatchTimeout = 30 * time.Second
	// defaultDispatchConcurrency caps how many dispatches hold a
	// database connection at once.
	//
	// Goroutines are cheap and connections are not, so the limit is on
	// the scarce resource: a burst of events parks goroutines rather
	// than draining the pool that the request handlers share. Blocking
	// the caller instead would reintroduce the very coupling this
	// removes, and dropping the dispatch would silently fail to wake an
	// agent — so excess work waits.
	defaultDispatchConcurrency = 8
)

// NotifyHook returns a closure compatible with eventbus.AddNotifyHook.
//
// The lookup and the enqueues run on their own goroutine. They used to
// run inline: every event append paid for one query plus an INSERT per
// matching agent before the request that caused it could return, so a
// workspace with several on_event agents made every write in that
// workspace slower, and a slow query there stalled unrelated request
// handlers. Nothing about waking an agent is worth blocking the write
// that woke it.
//
// The detached context keeps the request's values (trace span, logger)
// but not its cancellation, since the work must outlive the response;
// [defaultDispatchTimeout] bounds it instead. The eventInternalID is
// the events.id row that was just appended; the scoped lookup joins
// through it so schedule_scope='assigned_tasks' agents only wake when
// the source event is bound to a task they own.
func (e *EventTrigger) NotifyHook() func(ctx context.Context, workspaceID uint32, eventType string, eventInternalID uint64) {
	return func(ctx context.Context, workspaceID uint32, eventType string, eventInternalID uint64) {
		if e == nil || e.Queries == nil || e.Queue == nil {
			return
		}
		e.stopMu.RLock()
		if e.stopping {
			e.stopMu.RUnlock()
			return
		}
		e.wg.Add(1)
		e.stopMu.RUnlock()

		detached := context.WithoutCancel(ctx)
		go func() {
			defer e.wg.Done()
			defer func() {
				// A panic here would otherwise end the process: this
				// goroutine has no caller left to recover it.
				if r := recover(); r != nil {
					e.logger().Error("on_event dispatch panic",
						"recover", r, "ws", workspaceID, "event", eventType)
				}
			}()
			sem := e.semaphore()
			select {
			case sem <- struct{}{}:
			case <-detached.Done():
				return
			}
			defer func() { <-sem }()

			runCtx, cancel := context.WithTimeout(detached, e.dispatchTimeout())
			defer cancel()
			e.dispatch(runCtx, workspaceID, eventType, eventInternalID)
		}()
	}
}

// Shutdown stops accepting new dispatches and waits for the in-flight
// ones, or until ctx is cancelled. Mirrors the notification fan-out so
// process exit drains both the same way.
func (e *EventTrigger) Shutdown(ctx context.Context) error {
	e.stopMu.Lock()
	e.stopping = true
	e.stopMu.Unlock()

	done := make(chan struct{})
	go func() {
		e.wg.Wait()
		close(done)
	}()
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (e *EventTrigger) semaphore() chan struct{} {
	e.semOnce.Do(func() {
		n := e.DispatchConcurrency
		if n <= 0 {
			n = defaultDispatchConcurrency
		}
		e.sem = make(chan struct{}, n)
	})
	return e.sem
}

func (e *EventTrigger) dispatchTimeout() time.Duration {
	if e.DispatchTimeout > 0 {
		return e.DispatchTimeout
	}
	return defaultDispatchTimeout
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
