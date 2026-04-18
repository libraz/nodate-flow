// Package autoactions executor applies auto-action proposals
// autonomously. The Executor runs on a configurable interval,
// evaluates all active tasks via the deterministic rule engine, and
// applies concrete mutations (priority bumps, state transitions)
// for actions above the confidence threshold. Every applied action
// is recorded as an event so the audit trail is complete.
package autoactions

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/nodate-flow/nodate-flow/apps/api/internal/db/types"
)

// ExecutorConfig controls the background auto-action loop.
type ExecutorConfig struct {
	// Interval between evaluation passes. Zero disables the executor.
	Interval time.Duration
	// ConfidenceThreshold is the minimum confidence score for an
	// action to be applied automatically. Actions below this are
	// still shown in the Glass Dock but not executed.
	ConfidenceThreshold float32
	// DryRun logs what would be applied without mutating the database.
	DryRun bool
}

// Executor is the background worker that turns auto-action proposals
// into real mutations. It is started once in main.go alongside the
// agent scheduler.
type Executor struct {
	DB     *sql.DB
	Config ExecutorConfig
	Logger *slog.Logger

	stopOnce sync.Once
	stopCh   chan struct{}
}

// Start launches the background loop. It blocks until ctx is
// cancelled. Safe to call from a goroutine.
func (e *Executor) Start(ctx context.Context) {
	if e.Config.Interval <= 0 {
		e.Logger.Info("auto-action executor disabled (interval=0)")
		return
	}
	e.stopCh = make(chan struct{})
	e.Logger.Info("auto-action executor started",
		"interval", e.Config.Interval,
		"threshold", e.Config.ConfidenceThreshold,
		"dry_run", e.Config.DryRun,
	)

	ticker := time.NewTicker(e.Config.Interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-e.stopCh:
			return
		case <-ticker.C:
			e.tick(ctx)
		}
	}
}

// Stop signals the loop to exit.
func (e *Executor) Stop() {
	e.stopOnce.Do(func() {
		if e.stopCh != nil {
			close(e.stopCh)
		}
	})
}

// taskRow holds the fields needed for evaluation and mutation.
type taskRow struct {
	id           uint32
	publicID     types.PublicID
	workspaceID  uint32
	title        string
	derivedState string
	priority     int32
	dueOn        sql.NullTime
	updatedAt    sql.NullTime
	createdAt    time.Time
	hasAssignee  bool
}

func (e *Executor) tick(ctx context.Context) {
	rows, err := e.DB.QueryContext(ctx, "SELECT id FROM workspaces WHERE enabled = TRUE")
	if err != nil {
		e.Logger.Error("auto-action executor: list workspaces", "err", err)
		return
	}
	defer rows.Close()
	for rows.Next() {
		var wsID uint32
		if err := rows.Scan(&wsID); err != nil {
			e.Logger.Error("auto-action executor: scan workspace", "err", err)
			continue
		}
		e.processWorkspace(ctx, wsID)
	}
}

const taskQuery = `
SELECT t.id, t.public_id, t.workspace_id, t.title, t.derived_state,
       t.priority, t.due_on, t.updated_at, t.created_at,
       EXISTS(SELECT 1 FROM task_actors ta WHERE ta.task_id = t.id AND ta.kind = 'assignee') AS has_assignee
FROM tasks t
WHERE t.workspace_id = ? AND t.enabled = TRUE
  AND t.derived_state NOT IN ('done', 'cancelled')
LIMIT 200
`

func (e *Executor) processWorkspace(ctx context.Context, wsID uint32) {
	rows, err := e.DB.QueryContext(ctx, taskQuery, wsID)
	if err != nil {
		e.Logger.Error("auto-action executor: list tasks", "ws", wsID, "err", err)
		return
	}
	defer rows.Close()

	now := time.Now().UTC()
	for rows.Next() {
		var r taskRow
		if err := rows.Scan(
			&r.id, &r.publicID, &r.workspaceID, &r.title,
			&r.derivedState, &r.priority, &r.dueOn, &r.updatedAt,
			&r.createdAt, &r.hasAssignee,
		); err != nil {
			e.Logger.Error("auto-action executor: scan task", "err", err)
			continue
		}

		sig := Signals{
			State:       State(r.derivedState),
			HasAssignee: r.hasAssignee,
			Now:         now,
		}
		if r.updatedAt.Valid {
			sig.UpdatedAt = r.updatedAt.Time
		} else {
			sig.UpdatedAt = r.createdAt
		}
		if r.dueOn.Valid {
			sig.HasDueOn = true
			sig.DueOn = r.dueOn.Time
		}
		act := Evaluate(sig)
		if act == nil || act.Confidence < e.Config.ConfidenceThreshold {
			continue
		}
		e.applyAction(ctx, r, act)
	}
}

func (e *Executor) applyAction(ctx context.Context, r taskRow, act *Action) {
	if e.Config.DryRun {
		e.Logger.Info("auto-action executor: dry run",
			"task", r.publicID.String(),
			"kind", act.Kind,
			"reason", act.Reason,
		)
		return
	}

	switch act.Kind {
	case KindEscalateOverdue:
		e.escalate(ctx, r, act)
	case KindCloseStaleReview:
		e.closeStaleReview(ctx, r, act)
	case KindAssignOwner, KindNudgeAssignee:
		// These require human judgment (who to assign, how to nudge).
		// Record an event so Glass Dock can surface them, but don't
		// mutate data autonomously.
		e.recordProposal(ctx, r, act)
	}
}

// escalate bumps priority by 1 (if not already max) and records an event.
func (e *Executor) escalate(ctx context.Context, r taskRow, act *Action) {
	newPriority := r.priority + 1
	if newPriority > 4 {
		newPriority = 4
	}
	if newPriority == r.priority {
		e.recordProposal(ctx, r, act)
		return
	}

	tx, err := e.DB.BeginTx(ctx, nil)
	if err != nil {
		e.Logger.Error("auto-action executor: begin tx", "err", err)
		return
	}
	defer func() { _ = tx.Rollback() }()

	if _, err := tx.ExecContext(ctx,
		"UPDATE tasks SET priority = ?, updated_at = NOW() WHERE id = ?",
		newPriority, r.id,
	); err != nil {
		e.Logger.Error("auto-action executor: update priority", "task", r.publicID.String(), "err", err)
		return
	}

	payload, _ := json.Marshal(map[string]any{
		"auto_action": string(act.Kind),
		"field":       "priority",
		"from":        r.priority,
		"to":          newPriority,
		"reason":      act.Reason,
	})
	if err := e.appendEvent(ctx, tx, r, "task.updated", payload); err != nil {
		return
	}

	if err := tx.Commit(); err != nil {
		e.Logger.Error("auto-action executor: commit", "task", r.publicID.String(), "err", err)
		return
	}
	e.Logger.Info("auto-action applied: escalate",
		"task", r.publicID.String(),
		"priority", fmt.Sprintf("%d→%d", r.priority, newPriority),
	)
}

// closeStaleReview transitions a stale review task to done.
func (e *Executor) closeStaleReview(ctx context.Context, r taskRow, act *Action) {
	tx, err := e.DB.BeginTx(ctx, nil)
	if err != nil {
		e.Logger.Error("auto-action executor: begin tx", "err", err)
		return
	}
	defer func() { _ = tx.Rollback() }()

	if _, err := tx.ExecContext(ctx,
		"UPDATE tasks SET derived_state = 'done', updated_at = NOW() WHERE id = ?",
		r.id,
	); err != nil {
		e.Logger.Error("auto-action executor: transition state", "task", r.publicID.String(), "err", err)
		return
	}

	payload, _ := json.Marshal(map[string]any{
		"auto_action": string(act.Kind),
		"from":        r.derivedState,
		"to":          "done",
		"reason":      act.Reason,
	})
	if err := e.appendEvent(ctx, tx, r, "task.transition.complete", payload); err != nil {
		return
	}

	if err := tx.Commit(); err != nil {
		e.Logger.Error("auto-action executor: commit", "task", r.publicID.String(), "err", err)
		return
	}
	e.Logger.Info("auto-action applied: close stale review",
		"task", r.publicID.String(),
		"from", r.derivedState,
	)
}

// recordProposal writes an event recording the AI proposal without
// mutating the task.
func (e *Executor) recordProposal(ctx context.Context, r taskRow, act *Action) {
	payload, _ := json.Marshal(map[string]any{
		"kind":       string(act.Kind),
		"confidence": act.Confidence,
		"reason":     act.Reason,
	})
	if _, err := e.DB.ExecContext(ctx,
		`INSERT INTO events (public_id, workspace_id, task_id, type, payload_json, occurred_at)
		 VALUES (?, ?, ?, ?, ?, ?)`,
		types.New(), r.workspaceID, r.id,
		"ai.auto_action.proposed", payload, time.Now().UTC(),
	); err != nil {
		e.Logger.Error("auto-action executor: record proposal", "task", r.publicID.String(), "err", err)
	}
}

func (e *Executor) appendEvent(ctx context.Context, tx *sql.Tx, r taskRow, eventType string, payload json.RawMessage) error {
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO events (public_id, workspace_id, task_id, type, payload_json, occurred_at)
		 VALUES (?, ?, ?, ?, ?, ?)`,
		types.New(), r.workspaceID, r.id,
		eventType, payload, time.Now().UTC(),
	); err != nil {
		e.Logger.Error("auto-action executor: append event", "task", r.publicID.String(), "err", err)
		return err
	}
	return nil
}
