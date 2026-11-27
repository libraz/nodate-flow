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

	"github.com/libraz/nodate-flow/apps/flow-api/internal/ai/duedate"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/generated"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/types"
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

	// HandoffLoopLimit caps automated handoff_to_user emissions per
	// task. Zero falls back to defaultHandoffLoopLimit. Wired from
	// NF_AGENT_HANDOFF_LOOP_LIMIT alongside the runtime field.
	HandoffLoopLimit int

	// Now is time.Now except in tests. The per-workspace interval is
	// measured against it.
	Now func() time.Time

	// lastRun is when each workspace was last evaluated by this
	// executor, so ai_settings.auto_action_interval_minutes can be
	// honoured on a tick loop that runs at the global interval. It is
	// rebuilt from the workspace list on every pass, so a workspace
	// that disappears takes its entry with it.
	//
	// This is per process. Two replicas each keep their own clock, so a
	// workspace can be evaluated once per replica per interval; the
	// actions themselves are idempotent (each re-checks the row it is
	// about to change) and the pass before this existed ran on every
	// tick in every replica regardless.
	lastRunMu sync.Mutex
	lastRun   map[uint32]time.Time
}

// now returns the executor's clock.
func (e *Executor) now() time.Time {
	if e.Now != nil {
		return e.Now()
	}
	return time.Now()
}

// startedMessage is logged once per run, at the point the loop begins.
// It is a constant so that "a run is under way" has one spelling
// shared by the loop and by anything watching for it.
const startedMessage = "auto-action executor started"

// Start launches the background loop. It blocks until ctx is
// cancelled. Safe to call from a goroutine.
//
// Cancelling ctx is the only way to end a run, and it is the way the
// supervisor understands: a loop that returns while its context is
// still live is indistinguishable from one that died, and gets
// restarted. Every point the loop can block on — the ticker wait and
// each database call a pass makes — takes ctx, so the cancel is
// observed wherever the run happens to be.
func (e *Executor) Start(ctx context.Context) {
	if e.Config.Interval <= 0 {
		e.Logger.Info("auto-action executor disabled (interval=0)")
		return
	}
	e.Logger.Info(startedMessage,
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
		case <-ticker.C:
			if err := e.tick(ctx); err != nil {
				e.Logger.Error("auto-action executor: pass incomplete", "err", err)
			}
		}
	}
}

// RunOnce performs a single evaluation pass synchronously, mirroring
// what the background loop does on every ticker tick. It exists for
// integration tests that need deterministic execution without waiting
// on the interval, and for one-shot CLI invocations. Production code
// uses [Start].
//
// The error reports that the pass did not reach every workspace, not
// that a particular workspace failed: per-workspace problems are logged
// and skipped so one bad tenant cannot starve the rest. A caller that
// needs to know its own workspace was evaluated must check this.
func (e *Executor) RunOnce(ctx context.Context) error {
	return e.tick(ctx)
}

// taskRow holds the fields needed for evaluation and mutation.
//
// The task's title is deliberately absent. The rule engine decides on
// state, dates and actor facts alone, so reading the title would put a
// task's own content on the wire for a pass that has no reader to show
// it to and no actor to scope it by.
type taskRow struct {
	id           uint32
	publicID     types.PublicID
	workspaceID  uint32
	derivedState string
	priority     int32
	dueOn        sql.NullTime
	updatedAt    sql.NullTime
	createdAt    time.Time
	hasAssignee  bool

	// completionPolicy is tasks.completion_policy; review_required tasks
	// complete only through an approved submission.
	completionPolicy string

	// Agent-assignee facts populated when the task has at least one
	// enabled task_actors row with kind='agent' and role='assignee'.
	// Used by KindHandoffToUser; otherwise unused. agentMemo is the
	// raw JSON blob from tasks.agent_memo or nil when unset.
	hasAgentAssignee bool
	agentID          uint32
	agentPublicID    types.PublicID
	agentMemo        []byte
}

// wsAutoActionQuery reads the per-workspace auto-action settings.
//
// has_settings distinguishes "no ai_settings row, use the column
// defaults" from "a row exists and this is what it says". Without it a
// configured threshold of 0 — which the API accepts, and which means
// "apply every action the rule engine proposes" — was read as unset and
// silently replaced by the global default.
const wsAutoActionQuery = `
SELECT w.id,
       a.workspace_id IS NOT NULL                      AS has_settings,
       COALESCE(a.auto_action_enabled, TRUE)           AS aa_enabled,
       COALESCE(a.auto_action_interval_minutes, 5)     AS aa_interval,
       COALESCE(a.auto_action_threshold, 0.80)         AS aa_threshold
FROM workspaces w
LEFT JOIN ai_settings a ON a.workspace_id = w.id
WHERE w.enabled = TRUE
`

// workspaceTarget is one workspace the pass has to evaluate, with the
// confidence threshold already resolved against the global default and
// the configured evaluation interval.
type workspaceTarget struct {
	id        uint32
	threshold float32
	interval  time.Duration
}

// tick evaluates every enabled workspace once and reports whether the
// pass covered all of them.
//
// The enumeration is read to completion and its cursor closed before
// any workspace is processed, rather than processing inside the scan.
// Streaming a result set pins the connection it came from for as long
// as the loop runs, so the old shape held one pooled connection for the
// entire pass while the per-workspace work below asked that same pool
// for more: with enough passes in flight the pool is fully held by
// readers waiting on writers that can never be served. Reading the ids
// first bounds the pin to the length of the read.
//
// A pass that ends early returns an error. It used to end silently, and
// silence here is the worst possible outcome: workspaces are scanned in
// id order, so the tenants that never got evaluated are always the same
// ones, and nothing anywhere said the run was partial.
func (e *Executor) tick(ctx context.Context) error {
	targets, err := e.listWorkspaceTargets(ctx)
	if err != nil {
		e.Logger.Error("auto-action executor: list workspaces", "err", err)
		return err
	}

	// One instant for the whole pass: it is both the "is this workspace
	// due?" question and the answer stamped on the ones that run, so a
	// long pass cannot push the next one past its interval.
	now := e.now()
	for i, target := range targets {
		if err := ctx.Err(); err != nil {
			e.Logger.Error("auto-action executor: pass stopped early",
				"err", err,
				"evaluated", i,
				"workspaces", len(targets),
			)
			return fmt.Errorf("autoactions: pass stopped after %d of %d workspaces: %w", i, len(targets), err)
		}
		if !e.due(target, now) {
			continue
		}
		e.processWorkspaceWithThreshold(ctx, target.id, target.threshold)
	}
	e.forgetWorkspacesOutside(targets)
	return nil
}

// due reports whether the workspace's configured interval has elapsed,
// and records the decision when it has.
//
// An interval of zero disables the executor for that workspace, which
// is what the column has always documented. Every other value was read
// out of the database and then discarded: a workspace configured to be
// evaluated hourly was evaluated on every global tick, so the setting
// the UI displayed and the behaviour the tenant got had nothing to do
// with each other.
func (e *Executor) due(target workspaceTarget, now time.Time) bool {
	if target.interval <= 0 {
		return false
	}
	e.lastRunMu.Lock()
	defer e.lastRunMu.Unlock()
	if last, seen := e.lastRun[target.id]; seen && now.Sub(last) < target.interval-dueSlack(target.interval) {
		return false
	}
	if e.lastRun == nil {
		e.lastRun = make(map[uint32]time.Time)
	}
	e.lastRun[target.id] = now
	return true
}

// dueSlack is how much early a pass may be and still count.
//
// Ticks are not perfectly spaced: one delivered late followed by one
// delivered on time can be marginally less than a period apart. Without
// slack the workspace would be skipped and wait a whole further
// interval, so a tenant configured for five minutes would sometimes get
// ten. A tenth of the interval, capped, absorbs that without letting
// the interval mean noticeably less than it says.
func dueSlack(interval time.Duration) time.Duration {
	slack := interval / 10
	if slack > 30*time.Second {
		slack = 30 * time.Second
	}
	return slack
}

// forgetWorkspacesOutside drops the schedule of every workspace that was
// not in this pass, so a disabled or deleted tenant does not keep an
// entry for the life of the process.
func (e *Executor) forgetWorkspacesOutside(targets []workspaceTarget) {
	e.lastRunMu.Lock()
	defer e.lastRunMu.Unlock()
	if len(e.lastRun) == 0 {
		return
	}
	live := make(map[uint32]struct{}, len(targets))
	for _, t := range targets {
		live[t.id] = struct{}{}
	}
	for id := range e.lastRun {
		if _, ok := live[id]; !ok {
			delete(e.lastRun, id)
		}
	}
}

// listWorkspaceTargets reads every enabled workspace and its resolved
// threshold. A scan error drops the affected row and is logged; an
// error that ended the iteration itself fails the whole pass, because
// the rows that were never delivered are indistinguishable from rows
// that do not exist.
func (e *Executor) listWorkspaceTargets(ctx context.Context) ([]workspaceTarget, error) {
	rows, err := e.DB.QueryContext(ctx, wsAutoActionQuery)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	var targets []workspaceTarget
	for rows.Next() {
		var wsID uint32
		var hasSettings bool
		var wsCfg wsAutoActionConfig
		if err := rows.Scan(&wsID, &hasSettings, &wsCfg.enabled, &wsCfg.intervalMinutes, &wsCfg.threshold); err != nil {
			e.Logger.Error("auto-action executor: scan workspace", "err", err)
			continue
		}
		if !wsCfg.enabled {
			continue
		}
		// A workspace that has settings is governed by them, including a
		// threshold of 0 — the API accepts it and it means "apply
		// everything the rule engine proposes". Only a workspace with no
		// row at all falls back to the global default.
		threshold := e.Config.ConfidenceThreshold
		if hasSettings {
			threshold = wsCfg.threshold
		}
		targets = append(targets, workspaceTarget{
			id:        wsID,
			threshold: threshold,
			interval:  time.Duration(wsCfg.intervalMinutes) * time.Minute,
		})
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("autoactions: workspace scan ended early after %d rows: %w", len(targets), err)
	}
	return targets, nil
}

// taskScanTemplate is the per-workspace task scan, completed at run time
// by [Executor.processWorkspaceWithThreshold] with the derived_state
// filter and the idle interval its rule config resolves to. Both of those
// are the substituted fragments; nothing else about the statement varies.
//
// What it projects is the rule engine's whole input. No title, no
// description, no notes: the engine decides on state, dates and actor
// facts, so reading a task's own words would put them on the wire for a
// pass with no reader. agent_memo is the exception and is read as
// counters — see [decodeAgentMemo].
const taskScanTemplate = `
SELECT t.id, t.public_id, t.workspace_id, t.derived_state, t.completion_policy,
       t.priority, t.due_on, t.updated_at, t.created_at,
       -- role, not kind. task_actors.kind is the actor type
       -- ('user' | 'agent') and role is the relationship
       -- ('assignee' | 'reviewer' | ...), so kind = 'assignee' matches
       -- nothing the enum can hold: has_assignee was false for every
       -- task, and the executor kept proposing an owner for tasks that
       -- already had one while never nudging the assignees that did
       -- exist. Both rules read this one column.
       EXISTS(
         SELECT 1 FROM task_actors ta
         WHERE ta.task_id = t.id AND ta.role = 'assignee' AND ta.enabled = TRUE
       ) AS has_assignee,
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
`

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

// processWorkspaceWithThreshold evaluates one workspace's tasks against
// its resolved rule config and applies every action that clears the
// confidence threshold.
//
// task-visibility: not-applicable — this pass runs on a timer rather than
// for a reader, so there is no actor whose visibility could scope it and
// a predicate written against a zero actor would quietly reduce it to
// evaluating nothing. The scan is bounded by workspace_id instead, and
// the one content column it reads, agent_memo, is decoded by
// [decodeAgentMemo] into an attempt count and a timestamp; neither the
// memo nor anything derived from it is copied into an event payload, a
// log line or a response, so no task text leaves this function
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
	dynamicTaskQuery := fmt.Sprintf(taskScanTemplate, stateFilter, idleInterval)

	rows, err := e.DB.QueryContext(ctx, dynamicTaskQuery, wsID)
	if err != nil {
		e.Logger.Error("auto-action executor: list tasks", "ws", wsID, "err", err)
		return
	}
	defer rows.Close()

	now := time.Now().UTC()
	zone := duedate.WorkspaceZone(ctx, generated.New(e.DB), wsID)
	for rows.Next() {
		var (
			r              taskRow
			agentIDNull    sql.NullInt32
			agentPubIDRaw  []byte
			agentMemoBytes []byte
		)
		if err := rows.Scan(
			&r.id, &r.publicID, &r.workspaceID,
			&r.derivedState, &r.completionPolicy, &r.priority, &r.dueOn, &r.updatedAt,
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
			Zone:             zone,
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
