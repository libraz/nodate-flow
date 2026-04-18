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
	"strconv"
	"sync"
	"time"

	"github.com/nodate-flow/nodate-flow/apps/api/internal/db/types"
)

// ExecutorConfig controls the background auto-action loop. These values
// are used as global fallback defaults; per-workspace overrides are read
// from the ai_settings table on each tick.
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

// wsAutoActionConfig holds the per-workspace auto-action overrides read
// from the ai_settings table. When no row exists, the executor uses the
// global ExecutorConfig defaults.
type wsAutoActionConfig struct {
	enabled         bool
	intervalMinutes uint32
	threshold       float32
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

const wsAutoActionQuery = `
SELECT w.id,
       COALESCE(a.auto_action_enabled, TRUE)          AS aa_enabled,
       COALESCE(a.auto_action_interval_minutes, 5)     AS aa_interval,
       COALESCE(a.auto_action_threshold, 0.80)          AS aa_threshold
FROM workspaces w
LEFT JOIN ai_settings a ON a.workspace_id = w.id
WHERE w.enabled = TRUE
`

func (e *Executor) tick(ctx context.Context) {
	rows, err := e.DB.QueryContext(ctx, wsAutoActionQuery)
	if err != nil {
		e.Logger.Error("auto-action executor: list workspaces", "err", err)
		return
	}
	defer rows.Close()
	for rows.Next() {
		var wsID uint32
		var wsCfg wsAutoActionConfig
		if err := rows.Scan(&wsID, &wsCfg.enabled, &wsCfg.intervalMinutes, &wsCfg.threshold); err != nil {
			e.Logger.Error("auto-action executor: scan workspace", "err", err)
			continue
		}
		if !wsCfg.enabled {
			continue
		}
		// Use per-workspace threshold if configured, otherwise global.
		threshold := e.Config.ConfidenceThreshold
		if wsCfg.threshold > 0 {
			threshold = wsCfg.threshold
		}
		e.processWorkspaceWithThreshold(ctx, wsID, threshold)
	}
}

// taskQuery is now built dynamically in processWorkspaceWithThreshold
// using the minimum idle threshold from the workspace's rule config.

// rulesQuery fetches the per-workspace rule overrides. When no rows
// exist, the executor falls back to DefaultRuleConfigs().
const rulesQuery = `
SELECT kind, enabled, confidence, idle_hours
FROM auto_action_rules
WHERE workspace_id = ?
`

// loadRuleConfigs reads per-workspace rule overrides from the DB and
// returns a merged []RuleConfig. Rules not present in the DB use the
// hardcoded defaults.
func (e *Executor) loadRuleConfigs(ctx context.Context, wsID uint32) []RuleConfig {
	rows, err := e.DB.QueryContext(ctx, rulesQuery, wsID)
	if err != nil {
		e.Logger.Error("auto-action executor: load rules", "ws", wsID, "err", err)
		return DefaultRuleConfigs()
	}
	defer rows.Close()

	overrides := make(map[Kind]RuleConfig)
	for rows.Next() {
		var kind string
		var rc RuleConfig
		var confidence string
		if err := rows.Scan(&kind, &rc.Enabled, &confidence, &rc.IdleHours); err != nil {
			e.Logger.Error("auto-action executor: scan rule", "ws", wsID, "err", err)
			continue
		}
		rc.Kind = Kind(kind)
		if v, parseErr := strconv.ParseFloat(confidence, 32); parseErr == nil {
			rc.Confidence = float32(v)
		}
		overrides[rc.Kind] = rc
	}

	if len(overrides) == 0 {
		return DefaultRuleConfigs()
	}

	// Merge: use override if present, else default.
	defaults := DefaultRuleConfigs()
	result := make([]RuleConfig, len(defaults))
	for i, d := range defaults {
		if o, ok := overrides[d.Kind]; ok {
			result[i] = o
		} else {
			result[i] = d
		}
	}
	return result
}

func (e *Executor) processWorkspaceWithThreshold(ctx context.Context, wsID uint32, threshold float32) {
	rules := e.loadRuleConfigs(ctx, wsID)

	// Compute minimum idle hours across enabled rules to optimize the
	// task query filter. If no idle-based rule is enabled, only overdue
	// tasks need scanning.
	minIdleHours := uint32(0)
	hasIdleRule := false
	for _, r := range rules {
		if !r.Enabled || r.IdleHours == 0 {
			continue
		}
		if !hasIdleRule || r.IdleHours < minIdleHours {
			minIdleHours = r.IdleHours
			hasIdleRule = true
		}
	}

	// Build the task query with the dynamic idle threshold.
	idleInterval := "1 DAY" // fallback
	if hasIdleRule && minIdleHours > 0 {
		idleInterval = fmt.Sprintf("%d HOUR", minIdleHours)
	}
	dynamicTaskQuery := fmt.Sprintf(`
SELECT t.id, t.public_id, t.workspace_id, t.title, t.derived_state,
       t.priority, t.due_on, t.updated_at, t.created_at,
       EXISTS(SELECT 1 FROM task_actors ta WHERE ta.task_id = t.id AND ta.kind = 'assignee') AS has_assignee
FROM tasks t
WHERE t.workspace_id = ? AND t.enabled = TRUE
  AND t.derived_state NOT IN ('done', 'cancelled')
  AND (
    (t.due_on IS NOT NULL AND t.due_on < CURDATE())
    OR
    COALESCE(t.updated_at, t.created_at) < NOW() - INTERVAL %s
  )
LIMIT 200
`, idleInterval)

	rows, err := e.DB.QueryContext(ctx, dynamicTaskQuery, wsID)
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
		act := EvaluateWithConfig(sig, rules)
		if act == nil || act.Confidence < threshold {
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
