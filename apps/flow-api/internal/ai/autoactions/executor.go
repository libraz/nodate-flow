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

	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/generated"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/types"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/taskstate"
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

// HandoffQuerier is the narrow slice of [generated.Querier] the
// auto-action executor needs to apply a handoff_to_user action. Kept
// as a small interface so unit tests can pass a fake without standing
// up a real DB. Production wiring uses [generated.New] / WithTx on
// the executor's *sql.DB.
type HandoffQuerier interface {
	InsertHandoffToUserEvent(ctx context.Context, arg generated.InsertHandoffToUserEventParams) (int64, error)
	GetTaskAgentMemo(ctx context.Context, arg generated.GetTaskAgentMemoParams) (json.RawMessage, error)
	UpdateTaskAgentMemo(ctx context.Context, arg generated.UpdateTaskAgentMemoParams) error
}

// ActorDisabler disables a single (workspace_id, task_id, agent_id)
// task_actors row by setting enabled = FALSE. The production
// implementation wraps a *sql.Tx; tests pass a fake that records the
// disable for assertions.
type ActorDisabler interface {
	DisableAgentActor(ctx context.Context, workspaceID, taskID, agentID uint32) error
}

// defaultHandoffLoopLimit caps automated handoffs per task before the
// auto-action executor gives up and skips emitting further handoff
// events. Mirrors the runtime's NF_AGENT_HANDOFF_LOOP_LIMIT default
// so the same memo counter caps both code paths.
const defaultHandoffLoopLimit = 5

// Executor is the background worker that turns auto-action proposals
// into real mutations. It is started once in main.go alongside the
// agent scheduler.
type Executor struct {
	DB     *sql.DB
	Config ExecutorConfig
	Logger *slog.Logger

	// HandoffLoopLimit caps automated handoff_to_user emissions per
	// task. Zero falls back to defaultHandoffLoopLimit. Wired from
	// NF_AGENT_HANDOFF_LOOP_LIMIT alongside the runtime field.
	HandoffLoopLimit int

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

// RunOnce performs a single evaluation pass synchronously, mirroring
// what the background loop does on every ticker tick. It exists for
// integration tests that need deterministic execution without waiting
// on the interval, and for one-shot CLI invocations. Production code
// uses [Start] / [Stop].
func (e *Executor) RunOnce(ctx context.Context) {
	e.tick(ctx)
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

	// Agent-assignee facts populated when the task has at least one
	// enabled task_actors row with kind='agent' and role='assignee'.
	// Used by KindHandoffToUser; otherwise unused. agentMemo is the
	// raw JSON blob from tasks.agent_memo or nil when unset.
	hasAgentAssignee bool
	agentID          uint32
	agentPublicID    types.PublicID
	agentMemo        []byte
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
	archiveEnabled := false
	for _, r := range rules {
		if !r.Enabled {
			continue
		}
		if r.Kind == KindAutoArchiveCompleted {
			archiveEnabled = true
		}
		if r.IdleHours == 0 {
			continue
		}
		if !hasIdleRule || r.IdleHours < minIdleHours {
			minIdleHours = r.IdleHours
			hasIdleRule = true
		}
	}

	// Build the task query with the dynamic idle threshold.
	// When auto-archive is enabled, also include done/cancelled tasks
	// that have not been archived yet.
	idleInterval := "1 DAY" // fallback
	if hasIdleRule && minIdleHours > 0 {
		idleInterval = fmt.Sprintf("%d HOUR", minIdleHours)
	}
	stateFilter := "AND t.derived_state NOT IN ('done', 'cancelled')"
	if archiveEnabled {
		stateFilter = "AND (t.derived_state NOT IN ('done', 'cancelled') OR (t.derived_state IN ('done', 'cancelled') AND t.archived_at IS NULL))"
	}
	dynamicTaskQuery := fmt.Sprintf(`
SELECT t.id, t.public_id, t.workspace_id, t.title, t.derived_state,
       t.priority, t.due_on, t.updated_at, t.created_at,
       EXISTS(SELECT 1 FROM task_actors ta WHERE ta.task_id = t.id AND ta.kind = 'assignee') AS has_assignee,
       ag.id   AS agent_id,
       ag.public_id AS agent_public_id,
       t.agent_memo
FROM tasks t
LEFT JOIN task_actors agent_ta
  ON agent_ta.task_id = t.id
  AND agent_ta.kind = 'agent'
  AND agent_ta.role = 'assignee'
  AND agent_ta.enabled = TRUE
LEFT JOIN ai_agents ag
  ON ag.id = agent_ta.agent_id
  AND ag.enabled = TRUE
WHERE t.workspace_id = ? AND t.enabled = TRUE
  %s
  AND (
    (t.due_on IS NOT NULL AND t.due_on < CURDATE())
    OR
    COALESCE(t.updated_at, t.created_at) < NOW() - INTERVAL %s
  )
LIMIT 200
`, stateFilter, idleInterval)

	rows, err := e.DB.QueryContext(ctx, dynamicTaskQuery, wsID)
	if err != nil {
		e.Logger.Error("auto-action executor: list tasks", "ws", wsID, "err", err)
		return
	}
	defer rows.Close()

	now := time.Now().UTC()
	for rows.Next() {
		var (
			r              taskRow
			agentIDNull    sql.NullInt32
			agentPubIDRaw  []byte
			agentMemoBytes []byte
		)
		if err := rows.Scan(
			&r.id, &r.publicID, &r.workspaceID, &r.title,
			&r.derivedState, &r.priority, &r.dueOn, &r.updatedAt,
			&r.createdAt, &r.hasAssignee,
			&agentIDNull, &agentPubIDRaw, &agentMemoBytes,
		); err != nil {
			e.Logger.Error("auto-action executor: scan task", "err", err)
			continue
		}
		if agentIDNull.Valid {
			r.hasAgentAssignee = true
			//#nosec G115 -- ai_agents.id is INT UNSIGNED, fits uint32
			r.agentID = uint32(agentIDNull.Int32)
			if len(agentPubIDRaw) == 16 {
				_ = r.agentPublicID.Scan(agentPubIDRaw)
			}
		}
		if len(agentMemoBytes) > 0 {
			r.agentMemo = agentMemoBytes
		}

		sig := Signals{
			State:            State(r.derivedState),
			HasAssignee:      r.hasAssignee,
			HasAgentAssignee: r.hasAgentAssignee,
			Now:              now,
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
		if r.hasAgentAssignee {
			sig.AgentAttempts, sig.AgentLastFinishedAt = decodeAgentMemo(r.agentMemo)
		}
		act := EvaluateWithConfig(sig, rules)
		if act == nil || act.Confidence < threshold {
			continue
		}
		e.applyAction(ctx, r, act)
	}
}

// decodeAgentMemo extracts the attempts and last_finished_at unix-seconds
// fields the handoff_to_user rule needs from the JSON blob stored in
// tasks.agent_memo. Missing or unparseable values yield zero — the rule
// already handles the zero case by skipping (attempts < threshold).
func decodeAgentMemo(raw []byte) (attempts int, lastFinishedAt time.Time) {
	if len(raw) == 0 {
		return 0, time.Time{}
	}
	var memo struct {
		Attempts       int   `json:"attempts"`
		LastFinishedAt int64 `json:"last_finished_at"`
	}
	if err := json.Unmarshal(raw, &memo); err != nil {
		return 0, time.Time{}
	}
	var finished time.Time
	if memo.LastFinishedAt > 0 {
		finished = time.Unix(memo.LastFinishedAt, 0).UTC()
	}
	return memo.Attempts, finished
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
	case KindAutoArchiveCompleted:
		e.autoArchive(ctx, r, act)
	case KindAutoCloseStale:
		e.autoClose(ctx, r, act)
	case KindHandoffToUser:
		e.handoffToUser(ctx, r, act)
	case KindAssignOwner, KindNudgeAssignee:
		// These require human judgment (who to assign, how to nudge).
		// Record an event so Glass Dock can surface them, but don't
		// mutate data autonomously.
		e.recordProposal(ctx, r, act)
	}
}

// handoffToUser is the production wiring around [applyHandoffToUser].
// It opens a transaction, builds the sqlc-backed HandoffQuerier and
// raw-SQL ActorDisabler, runs the shared logic, and commits.
func (e *Executor) handoffToUser(ctx context.Context, r taskRow, act *Action) {
	if !r.hasAgentAssignee || r.agentID == 0 {
		// Defensive guard: the rule already filters for an agent
		// assignee, but we re-check here so a stale row read does
		// not turn into an event with no actor.
		return
	}
	tx, err := e.DB.BeginTx(ctx, nil)
	if err != nil {
		e.Logger.Error("auto-action executor: begin tx", "err", err)
		return
	}
	defer func() { _ = tx.Rollback() }()

	q := generated.New(e.DB).WithTx(tx)
	disabler := &txActorDisabler{tx: tx}

	emitted, err := e.applyHandoffToUser(ctx, q, disabler, r, act, time.Now().UTC())
	if err != nil {
		e.Logger.Error("auto-action executor: handoff_to_user", "task", r.publicID.String(), "err", err)
		return
	}
	if err := tx.Commit(); err != nil {
		e.Logger.Error("auto-action executor: commit", "task", r.publicID.String(), "err", err)
		return
	}
	if emitted {
		e.Logger.Info("auto-action applied: handoff_to_user",
			"task", r.publicID.String(),
			"agent", r.agentPublicID.String(),
		)
	} else {
		e.Logger.Info("auto-action skipped: handoff_to_user (loop budget exhausted)",
			"task", r.publicID.String(),
			"agent", r.agentPublicID.String(),
		)
	}
}

// applyHandoffToUser is the pure logic of the handoff_to_user action.
// It is split out so unit tests can drive it with a fake HandoffQuerier
// + ActorDisabler. The bool return value indicates whether a handoff
// event was actually emitted (false when the loop budget is exhausted).
//
// Behaviour mirrors the orchestrator runner's handleHandoff:
//   - Read prior handoff_count from agent_memo.
//   - If prior >= HandoffLoopLimit (defaultHandoffLoopLimit when zero),
//     skip the emission and return (false, nil) so the caller can log.
//   - Otherwise emit agent.task.handoff_to_user with actor_agent_id set,
//     disable the agent task_actors row, and merge handoff state into
//     agent_memo (status=handed_back, reason=stuck, count=prior+1,
//     last_handoff_at=now unix seconds).
func (e *Executor) applyHandoffToUser(
	ctx context.Context,
	q HandoffQuerier,
	disabler ActorDisabler,
	r taskRow,
	act *Action,
	at time.Time,
) (bool, error) {
	limit := e.HandoffLoopLimit
	if limit <= 0 {
		limit = defaultHandoffLoopLimit
	}
	priorAttempts, priorCount := readMemoCounters(ctx, q, r.workspaceID, r.id)
	if priorCount >= limit {
		// Budget exhausted: do not emit another handoff. The
		// orchestrator runtime owns the loop-detected failure event;
		// the auto-action path simply refuses to add to the noise.
		return false, nil
	}

	payload := map[string]any{
		"reason":        "stuck",
		"agentPublicId": r.agentPublicID.String(),
		"taskPublicId":  r.publicID.String(),
		"handoffCount":  priorCount + 1,
		"attempts":      priorAttempts,
		"detectedBy":    "auto_action",
	}
	if act != nil && act.Reason != "" {
		payload["detail"] = act.Reason
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		return false, fmt.Errorf("marshal handoff payload: %w", err)
	}
	if _, err := q.InsertHandoffToUserEvent(ctx, generated.InsertHandoffToUserEventParams{
		PublicID:     types.New(),
		WorkspaceID:  r.workspaceID,
		TaskID:       sql.NullInt32{Int32: int32(r.id), Valid: true},      //#nosec G115 -- tasks.id fits int32
		ActorAgentID: sql.NullInt32{Int32: int32(r.agentID), Valid: true}, //#nosec G115 -- ai_agents.id fits int32
		PayloadJson:  raw,
		OccurredAt:   at,
	}); err != nil {
		return false, fmt.Errorf("insert handoff event: %w", err)
	}

	if err := disabler.DisableAgentActor(ctx, r.workspaceID, r.id, r.agentID); err != nil {
		return false, fmt.Errorf("disable agent actor: %w", err)
	}

	memoPatch := map[string]any{
		"handoff_status":  "handed_back",
		"handoff_reason":  "stuck",
		"handoff_count":   priorCount + 1,
		"last_handoff_at": at.Unix(),
	}
	patchRaw, err := json.Marshal(memoPatch)
	if err != nil {
		return false, fmt.Errorf("marshal memo patch: %w", err)
	}
	if err := q.UpdateTaskAgentMemo(ctx, generated.UpdateTaskAgentMemoParams{
		Column1:     patchRaw,
		WorkspaceID: r.workspaceID,
		ID:          r.id,
	}); err != nil {
		return false, fmt.Errorf("update agent_memo: %w", err)
	}
	return true, nil
}

// readMemoCounters reads attempts + handoff_count out of agent_memo so
// applyHandoffToUser can decide whether to emit and what counter to
// stamp on the new event / memo patch. Missing rows / decode errors
// yield zeros — the caller treats those as "first attempt".
func readMemoCounters(ctx context.Context, q HandoffQuerier, workspaceID, taskID uint32) (attempts, handoffCount int) {
	raw, err := q.GetTaskAgentMemo(ctx, generated.GetTaskAgentMemoParams{
		WorkspaceID: workspaceID,
		ID:          taskID,
	})
	if err != nil || len(raw) == 0 {
		return 0, 0
	}
	var memo struct {
		Attempts     int `json:"attempts"`
		HandoffCount int `json:"handoff_count"`
	}
	if err := json.Unmarshal(raw, &memo); err != nil {
		return 0, 0
	}
	return memo.Attempts, memo.HandoffCount
}

// txActorDisabler implements ActorDisabler over a *sql.Tx by issuing a
// targeted UPDATE against task_actors. We match on (workspace_id,
// task_id, agent_id, kind='agent', role='assignee') so a stale row in
// some other role for the same agent does not get disabled.
type txActorDisabler struct{ tx *sql.Tx }

// DisableAgentActor flips enabled=FALSE on the agent assignee row.
func (d *txActorDisabler) DisableAgentActor(ctx context.Context, workspaceID, taskID, agentID uint32) error {
	const q = `UPDATE task_actors
		SET enabled = FALSE
		WHERE workspace_id = ?
		  AND task_id = ?
		  AND agent_id = ?
		  AND kind = 'agent'
		  AND role = 'assignee'
		  AND enabled = TRUE`
	if _, err := d.tx.ExecContext(ctx, q, workspaceID, taskID, agentID); err != nil {
		return err
	}
	return nil
}

// escalate bumps priority by 1 (if not already max) and records an event.
// When the task is already at the priority cap there is nothing new to
// propose, so we return silently to avoid flooding the activity log
// with identical `ai.auto_action.proposed` events on every tick.
func (e *Executor) escalate(ctx context.Context, r taskRow, act *Action) {
	newPriority := r.priority + 1
	if newPriority > 4 {
		newPriority = 4
	}
	if newPriority == r.priority {
		// Already at cap (priority = 4). Nothing to escalate; skip the
		// proposal event entirely.
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

// closeStaleReview transitions a stale review task to done by going
// through the canonical state-machine helper. The executor never
// touches tasks.derived_state directly: writing the column is reserved
// to taskstate.ApplyTransitionTx, which also appends the matching
// task.transition.complete event in the same transaction.
func (e *Executor) closeStaleReview(ctx context.Context, r taskRow, act *Action) {
	e.applyStateTransition(ctx, r, act, taskstate.TransitionComplete)
}

// recordProposal writes an event recording the AI proposal without
// mutating the task. It deduplicates against the most recent
// `ai.auto_action.proposed` event for this task: if the last proposal
// had the same kind and no user-driven (non-`ai.%`) event has touched
// the task since, the new proposal is skipped. This prevents the
// activity log from filling up with identical rows every tick for
// tasks stuck in a state the rule keeps matching.
func (e *Executor) recordProposal(ctx context.Context, r taskRow, act *Action) {
	var lastKind sql.NullString
	var lastOccurred sql.NullTime
	err := e.DB.QueryRowContext(ctx, `
		SELECT JSON_UNQUOTE(JSON_EXTRACT(payload_json, '$.kind')), occurred_at
		FROM events
		WHERE task_id = ? AND type = 'ai.auto_action.proposed'
		ORDER BY occurred_at DESC
		LIMIT 1`, r.id).Scan(&lastKind, &lastOccurred)
	if err == nil && lastKind.Valid && lastKind.String == string(act.Kind) && lastOccurred.Valid {
		var userSince int
		_ = e.DB.QueryRowContext(ctx, `
			SELECT COUNT(*) FROM events
			WHERE task_id = ? AND occurred_at > ? AND type NOT LIKE 'ai.%'`,
			r.id, lastOccurred.Time).Scan(&userSince)
		if userSince == 0 {
			// Same proposal kind is already the latest, and no user
			// activity since. Nothing new to say.
			return
		}
	}

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

// autoArchive sets archived_at on a completed/cancelled task that has
// been idle longer than the configured threshold.
func (e *Executor) autoArchive(ctx context.Context, r taskRow, act *Action) {
	tx, err := e.DB.BeginTx(ctx, nil)
	if err != nil {
		e.Logger.Error("auto-action executor: begin tx", "err", err)
		return
	}
	defer func() { _ = tx.Rollback() }()

	if _, err := tx.ExecContext(ctx,
		"UPDATE tasks SET archived_at = NOW(), updated_at = NOW() WHERE id = ? AND archived_at IS NULL",
		r.id,
	); err != nil {
		e.Logger.Error("auto-action executor: archive task", "task", r.publicID.String(), "err", err)
		return
	}

	payload, _ := json.Marshal(map[string]any{
		"auto_action": string(act.Kind),
		"reason":      act.Reason,
	})
	if err := e.appendEvent(ctx, tx, r, "task.archived", payload); err != nil {
		return
	}

	if err := tx.Commit(); err != nil {
		e.Logger.Error("auto-action executor: commit", "task", r.publicID.String(), "err", err)
		return
	}
	e.Logger.Info("auto-action applied: auto-archive",
		"task", r.publicID.String(),
		"state", r.derivedState,
	)
}

// autoClose cancels a stale open task that has had no activity beyond
// the configured threshold. Like closeStaleReview, the actual
// derived_state UPDATE and matching task.transition.cancel event are
// performed by the canonical taskstate helper.
func (e *Executor) autoClose(ctx context.Context, r taskRow, act *Action) {
	e.applyStateTransition(ctx, r, act, taskstate.TransitionCancel)
}

// applyStateTransition is the shared body of [closeStaleReview] and
// [autoClose]. It opens a transaction, runs the canonical
// taskstate.ApplyTransitionTx helper (which acquires a row lock,
// validates the transition against the v1 state machine, persists the
// new derived_state, and appends the matching task.transition.<name>
// event) and commits.
//
// The executor runs as a system component, not on behalf of a user,
// so ActorUserID is left nil; the via="auto_action" tag and
// auto_action / confidence keys on the event payload mark the event
// origin so audit consumers can distinguish it from human transitions.
func (e *Executor) applyStateTransition(ctx context.Context, r taskRow, act *Action, transition string) {
	tx, err := e.DB.BeginTx(ctx, nil)
	if err != nil {
		e.Logger.Error("auto-action executor: begin tx", "err", err)
		return
	}
	defer func() { _ = tx.Rollback() }()

	result, spec, cause := taskstate.ApplyTransitionTx(ctx, tx, taskstate.ApplyParams{
		WorkspaceID: r.workspaceID,
		TaskID:      r.id,
		PublicID:    r.publicID,
		Transition:  transition,
		ActorUserID: nil, // system / auto-action, not a real user
		Reason:      act.Reason,
		Via:         "auto_action",
		ExtraPayload: map[string]any{
			"auto_action": string(act.Kind),
			"confidence":  act.Confidence,
		},
	})
	if spec != nil {
		// Validation rejection (TRANSITION_REJECTED / NOT_FOUND) means
		// the task drifted out of the matching state between query and
		// apply; this is benign — log and skip.
		e.Logger.Warn("auto-action executor: transition rejected",
			"task", r.publicID.String(),
			"transition", transition,
			"code", spec.Code,
			"cause", cause,
		)
		return
	}

	if err := tx.Commit(); err != nil {
		e.Logger.Error("auto-action executor: commit", "task", r.publicID.String(), "err", err)
		return
	}
	e.Logger.Info("auto-action applied: state transition",
		"task", r.publicID.String(),
		"kind", string(act.Kind),
		"from", string(result.FromState),
		"to", string(result.ToState),
	)
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
