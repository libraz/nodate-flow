package e2e

import (
	"context"
	"database/sql"
	"log/slog"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/libraz/nodate-flow/apps/flow-api/internal/ai/autoactions"
	"github.com/libraz/nodate-flow/apps/flow-api/tests/helpers"
)

// TestAutoActionExecutorClosesStaleReviewViaCanonicalPath verifies the
// fix for the "executor bypasses derived_state engine" bug: the
// auto-action executor must transition tasks through the canonical
// taskstate helper rather than UPDATE-ing tasks.derived_state directly.
//
// Setup: seed a task that has been sitting in review for longer than
// the close_stale_review idle threshold, with no other signals that
// would beat it in urgency order.
//
// Expectation: after a single executor pass, the task is in done and a
// task.transition.complete event has been appended carrying the
// auto_action / via=auto_action provenance keys. No
// ai.auto_action.proposed event is emitted (the executor applied the
// action instead of merely proposing it).
func TestAutoActionExecutorClosesStaleReviewViaCanonicalPath(t *testing.T) {
	bootstrap(t)
	t.Parallel()

	tenant := newTenant(t)
	t.Cleanup(func() { helpers.PurgeWorkspace(t, testDB, tenant.WorkspacePublicID) })

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	wsID, userID := lookupWorkspaceAndOwner(ctx, t, tenant.WorkspacePublicID)

	// The per-workspace auto_action_threshold COALESCEs to 0.80, which
	// is above the close_stale_review default confidence (0.70). Lower
	// the workspace threshold so the action is allowed to apply.
	setWorkspaceAutoActionThreshold(ctx, t, wsID, "0.50")

	// Seed a task in review state that has been idle long enough to
	// trigger close_stale_review (default idle = 120 hours = 5 days).
	taskID := seedTask(ctx, t, wsID, userID, "stale review task", "")
	setTaskStateAndUpdatedAt(ctx, t, taskID,
		"review",
		time.Now().Add(-10*24*time.Hour),
	)

	exec := &autoactions.Executor{
		DB: testDB,
		Config: autoactions.ExecutorConfig{
			// 1ns is non-zero so the executor passes the disabled check;
			// we drive the loop manually via RunOnce so the value never
			// triggers an actual ticker.
			Interval:            time.Nanosecond,
			ConfidenceThreshold: 0.5,
			DryRun:              false,
		},
		Logger: slog.Default(),
	}
	requireCompletePass(ctx, t, exec)

	// Assert: derived_state moved to done.
	derived := readDerivedState(ctx, t, taskID)
	require.Equal(t, "done", derived,
		"close_stale_review must transition the task through the canonical state machine")

	// Assert: a task.transition.complete event row was appended in the
	// same transaction, with the auto_action provenance keys.
	requireExactlyOneTransitionEvent(ctx, t, taskID, "task.transition.complete",
		"close_stale_review", "auto_action")

	// Assert: no leftover proposal event was emitted; the executor
	// applied the action rather than only proposing it.
	requireNoProposalEvent(ctx, t, taskID)
}

// TestAutoActionExecutorAutoClosesStaleViaCanonicalPath is the cancel-
// path mirror of the close_stale_review test. It seeds an ancient
// idle open task with auto_close_stale enabled at the workspace level
// and asserts the executor cancels the task via the canonical helper.
func TestAutoActionExecutorAutoClosesStaleViaCanonicalPath(t *testing.T) {
	bootstrap(t)
	t.Parallel()

	tenant := newTenant(t)
	t.Cleanup(func() { helpers.PurgeWorkspace(t, testDB, tenant.WorkspacePublicID) })

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	wsID, userID := lookupWorkspaceAndOwner(ctx, t, tenant.WorkspacePublicID)

	// Drop the workspace-level threshold below the auto_close_stale
	// rule confidence (0.80) so the action is allowed to apply.
	setWorkspaceAutoActionThreshold(ctx, t, wsID, "0.50")

	// The rule engine evaluates kinds in urgency order (escalate,
	// assign_owner, nudge_assignee, close_stale_review,
	// auto_close_stale) and the first match wins. To force the cancel
	// path we disable every kind that would otherwise outrank
	// auto_close_stale on an idle open task, then opt this workspace
	// into auto_close_stale with a short idle threshold.
	enableAutoActionRule(ctx, t, wsID, "escalate_overdue", false, "0.85", 0)
	enableAutoActionRule(ctx, t, wsID, "assign_owner", false, "0.75", 24)
	enableAutoActionRule(ctx, t, wsID, "nudge_assignee", false, "0.70", 72)
	enableAutoActionRule(ctx, t, wsID, "close_stale_review", false, "0.70", 120)
	enableAutoActionRule(ctx, t, wsID, "auto_close_stale", true, "0.80", 24)

	// Seed an open task that has been idle for 30+ days, no assignee.
	taskID := seedTask(ctx, t, wsID, userID, "ancient idle task", "")
	setTaskStateAndUpdatedAt(ctx, t, taskID,
		"open",
		time.Now().Add(-31*24*time.Hour),
	)

	exec := &autoactions.Executor{
		DB: testDB,
		Config: autoactions.ExecutorConfig{
			Interval:            time.Nanosecond,
			ConfidenceThreshold: 0.5,
			DryRun:              false,
		},
		Logger: slog.Default(),
	}
	requireCompletePass(ctx, t, exec)

	derived := readDerivedState(ctx, t, taskID)
	require.Equal(t, "cancelled", derived,
		"auto_close_stale must transition the task through the canonical state machine")

	requireExactlyOneTransitionEvent(ctx, t, taskID, "task.transition.cancel",
		"auto_close_stale", "auto_action")
	requireNoProposalEvent(ctx, t, taskID)
}

// ---- local seed / assertion helpers ---------------------------------------

// setTaskStateAndUpdatedAt force-writes derived_state and back-dates
// updated_at via raw SQL so the executor sees a task that matches its
// idle thresholds. This bypasses the state machine on purpose: the
// system under test is the executor's transition path, not the seeding
// path.
func setTaskStateAndUpdatedAt(ctx context.Context, t *testing.T, taskID uint32, state string, updatedAt time.Time) {
	t.Helper()
	// trg_tasks_derived_state_guard rejects a non-engine derived_state write
	// unless @nf_derived_state_engine = 1 on the same connection. The session
	// variable is connection-scoped, so pin one connection for the SET +
	// UPDATE (mirrors internal/taskstate/state.go); db.ExecContext on the pool
	// would run them on different connections and the guard would fire.
	conn, err := testDB.Conn(ctx)
	require.NoError(t, err)
	defer conn.Close()
	_, err = conn.ExecContext(ctx, "SET @nf_derived_state_engine = 1")
	require.NoError(t, err)
	_, err = conn.ExecContext(ctx,
		`UPDATE tasks SET derived_state = ?, updated_at = ? WHERE id = ?`,
		state, updatedAt.UTC(), taskID,
	)
	require.NoError(t, err)
	_, _ = conn.ExecContext(ctx, "SET @nf_derived_state_engine = NULL")
}

// setWorkspaceAutoActionThreshold upserts an ai_settings row that
// lowers the per-workspace auto_action_threshold so test rules can
// fire below the production default (0.80).
func setWorkspaceAutoActionThreshold(ctx context.Context, t *testing.T, wsID uint32, threshold string) {
	t.Helper()
	_, err := testDB.ExecContext(ctx,
		`INSERT INTO ai_settings (workspace_id, auto_action_threshold)
		 VALUES (?, ?)
		 ON DUPLICATE KEY UPDATE auto_action_threshold = VALUES(auto_action_threshold)`,
		wsID, threshold,
	)
	require.NoError(t, err)
}

// enableAutoActionRule upserts a per-workspace rule override into
// auto_action_rules so the executor reads non-default thresholds.
func enableAutoActionRule(ctx context.Context, t *testing.T, wsID uint32, kind string, enabled bool, confidence string, idleHours uint32) {
	t.Helper()
	_, err := testDB.ExecContext(ctx,
		`INSERT INTO auto_action_rules (public_id, workspace_id, kind, enabled, confidence, idle_hours)
		 VALUES (UUID_TO_BIN(UUID(), 0), ?, ?, ?, ?, ?)
		 ON DUPLICATE KEY UPDATE enabled=VALUES(enabled), confidence=VALUES(confidence), idle_hours=VALUES(idle_hours)`,
		wsID, kind, enabled, confidence, idleHours,
	)
	require.NoError(t, err)
}

// readDerivedState reads tasks.derived_state directly so the assertion
// is independent of any sqlc-mapped enum type.
func readDerivedState(ctx context.Context, t *testing.T, taskID uint32) string {
	t.Helper()
	var s string
	err := testDB.QueryRowContext(ctx,
		`SELECT derived_state FROM tasks WHERE id = ?`, taskID).Scan(&s)
	require.NoError(t, err)
	return s
}

// requireExactlyOneTransitionEvent asserts that exactly one event of
// the given canonical type was appended for the task and that its
// payload carries the auto-action provenance keys (auto_action, via).
// The actionKindPayload arg is the expected value of the
// `auto_action` key on the payload (e.g. "close_stale_review").
func requireExactlyOneTransitionEvent(ctx context.Context, t *testing.T, taskID uint32, eventType, actionKindPayload, viaPayload string) {
	t.Helper()
	var (
		count       int
		auto        sql.NullString
		via         sql.NullString
		fromState   sql.NullString
		toState     sql.NullString
		actorUserID sql.NullInt32
	)
	err := testDB.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM events WHERE task_id = ? AND type = ?`,
		taskID, eventType).Scan(&count)
	require.NoError(t, err)
	require.Equal(t, 1, count,
		"expected exactly one %s event for task %d", eventType, taskID)

	err = testDB.QueryRowContext(ctx,
		`SELECT JSON_UNQUOTE(JSON_EXTRACT(payload_json, '$.auto_action')),
		        JSON_UNQUOTE(JSON_EXTRACT(payload_json, '$.via')),
		        JSON_UNQUOTE(JSON_EXTRACT(payload_json, '$.fromState')),
		        JSON_UNQUOTE(JSON_EXTRACT(payload_json, '$.toState')),
		        actor_user_id
		 FROM events WHERE task_id = ? AND type = ? LIMIT 1`,
		taskID, eventType).Scan(&auto, &via, &fromState, &toState, &actorUserID)
	require.NoError(t, err)
	require.True(t, auto.Valid, "event payload missing auto_action key")
	require.Equal(t, actionKindPayload, auto.String)
	require.True(t, via.Valid, "event payload missing via key")
	require.Equal(t, viaPayload, via.String)
	require.True(t, fromState.Valid, "event payload missing fromState")
	require.True(t, toState.Valid, "event payload missing toState")
	require.False(t, actorUserID.Valid,
		"auto-action transitions must record NULL actor_user_id (system origin), got %d",
		actorUserID.Int32)
}

// requireNoProposalEvent asserts the executor did NOT also write a
// stale ai.auto_action.proposed row for this task; auto-applied
// actions are recorded as the canonical task.transition.<name> event,
// not as a proposal.
func requireNoProposalEvent(ctx context.Context, t *testing.T, taskID uint32) {
	t.Helper()
	var n int
	err := testDB.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM events WHERE task_id = ? AND type = 'ai.auto_action.proposed'`,
		taskID).Scan(&n)
	require.NoError(t, err)
	require.Zero(t, n, "executor must not also emit ai.auto_action.proposed when it applies the action")
}

// requireCompletePass drives one evaluation pass and fails unless it
// reached every workspace.
//
// The executor scans the whole instance, which is what it does in
// production, so a pass raised by this test also walks the workspaces
// every other test in the suite is holding. If that walk stops early
// this test's workspace may simply never have been looked at, and
// asserting on the outcome would report a transition the executor was
// never asked to make — with the failure pointing at the state machine
// rather than at the pass that ended. Checking the pass first keeps the
// assertion about behaviour instead of about how many workspaces the
// suite happened to leave lying around.
func requireCompletePass(ctx context.Context, t *testing.T, exec *autoactions.Executor) {
	t.Helper()
	require.NoError(t, exec.RunOnce(ctx),
		"the auto-action pass did not reach every workspace, so this workspace may never have been evaluated")
}

// TestAutoActionExecutorReportsAnIncompletePass locks in the half of the
// contract that was missing: a pass that did not reach every workspace
// says so, instead of returning as though the whole instance had been
// evaluated.
//
// Silence was the damaging part. Workspaces are walked in id order, so
// the tenants a truncated pass skips are always the same ones, and
// nothing in the logs or the return value distinguished "evaluated and
// nothing to do" from "never looked at". A cancelled context is the
// deterministic stand-in here for the condition that produced it in
// practice: the pass pinning a pooled connection for its whole run
// while the per-workspace work asked the same pool for more.
func TestAutoActionExecutorReportsAnIncompletePass(t *testing.T) {
	bootstrap(t)
	t.Parallel()

	stopped, cancel := context.WithCancel(context.Background())
	cancel()

	exec := &autoactions.Executor{
		DB: testDB,
		Config: autoactions.ExecutorConfig{
			Interval:            time.Nanosecond,
			ConfidenceThreshold: 0.5,
		},
		Logger: slog.Default(),
	}

	require.Error(t, exec.RunOnce(stopped),
		"a pass that could not evaluate every workspace must report that, not return as if it had")
}

// stopsAfterFirstCheck is a context that is live for its first Err()
// call and cancelled from then on, which puts the cancellation between
// the workspace enumeration and the work that follows it. Done() is
// deliberately the embedded background channel: the enumeration query
// must be allowed to succeed, so only the executor's own per-workspace
// check is expected to observe the cancellation.
type stopsAfterFirstCheck struct {
	context.Context
	mu     sync.Mutex
	checks int
}

func (c *stopsAfterFirstCheck) Err() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.checks++
	if c.checks > 1 {
		return context.Canceled
	}
	return nil
}

// TestAutoActionExecutorReportsAPassStoppedPartWay covers the other end
// of the same contract: a pass that enumerated the workspaces and then
// stopped part way through them also reports that it was partial. This
// is the shape the production incident takes — the enumeration is quick
// and it is the per-workspace work that runs out of time.
func TestAutoActionExecutorReportsAPassStoppedPartWay(t *testing.T) {
	bootstrap(t)
	t.Parallel()

	// A tenant of our own guarantees the enumeration returns at least
	// one workspace, so the loop below is entered at all.
	newTenant(t)

	exec := &autoactions.Executor{
		DB: testDB,
		Config: autoactions.ExecutorConfig{
			Interval:            time.Nanosecond,
			ConfidenceThreshold: 0.5,
		},
		Logger: slog.Default(),
	}

	err := exec.RunOnce(&stopsAfterFirstCheck{Context: context.Background()})
	require.Error(t, err, "a pass that stopped between workspaces must report it")
	require.Contains(t, err.Error(), "stopped after",
		"the error must say the pass was partial, not that a workspace failed")
}
