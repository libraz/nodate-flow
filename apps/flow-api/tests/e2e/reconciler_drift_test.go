package e2e

import (
	"context"
	"database/sql"
	"log/slog"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/nodate-flow/nodate-flow/apps/flow-api/internal/reconciler"
	"github.com/nodate-flow/nodate-flow/apps/flow-api/tests/helpers"
)

// recordingMetrics is a test-local MetricsSink that counts calls.
type recordingMetrics struct {
	mu            sync.Mutex
	inconsistency map[string]int
	heal          map[string]int
	runs          int
	errs          int
}

func newRecordingMetrics() *recordingMetrics {
	return &recordingMetrics{
		inconsistency: map[string]int{},
		heal:          map[string]int{},
	}
}

func (r *recordingMetrics) IncInconsistency(kind string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.inconsistency[kind]++
}

func (r *recordingMetrics) IncHeal(kind string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.heal[kind]++
}

func (r *recordingMetrics) IncRun() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.runs++
}

func (r *recordingMetrics) IncError() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.errs++
}

// TestReconcilerHealsDueOnDrift seeds a task and a linked calendar
// event whose DATE(start_at) disagrees with task.due_on, runs the
// reconciler once, and asserts that the task row now matches the event.
func TestReconcilerHealsDueOnDrift(t *testing.T) {
	bootstrap(t)
	t.Parallel()

	tenant := newTenant(t)
	t.Cleanup(func() { helpers.PurgeWorkspace(t, testDB, tenant.WorkspacePublicID) })

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	wsID, userID := lookupWorkspaceAndOwner(ctx, t, tenant.WorkspacePublicID)
	calID := seedPersonalCalendar(ctx, t, wsID, userID, "drift-due")

	taskID := seedTask(ctx, t, wsID, userID, "drift due task", "2026-06-01")
	seedLinkedEvent(ctx, t, wsID, calID, userID, taskID, "due",
		"2026-06-08 09:00:00", "2026-06-08 10:00:00")

	sink := newRecordingMetrics()
	rec := &reconciler.Reconciler{DB: testDB, Logger: slog.Default(), Metrics: sink}
	rec.RunOnce(ctx)

	var dueOn sql.NullString
	require.NoError(t, testDB.QueryRowContext(ctx,
		`SELECT DATE_FORMAT(due_on, '%Y-%m-%d') FROM tasks WHERE id = ?`, taskID).
		Scan(&dueOn))
	require.True(t, dueOn.Valid)
	require.Equal(t, "2026-06-08", dueOn.String)

	require.GreaterOrEqual(t, sink.inconsistency["date_drift_due"], 1)
	require.GreaterOrEqual(t, sink.heal["date_drift_due"], 1)
}

// TestReconcilerHealsEnabledMismatch seeds a disabled task with a
// still-enabled linked event, runs the reconciler, asserts the event
// is now disabled.
func TestReconcilerHealsEnabledMismatch(t *testing.T) {
	bootstrap(t)
	t.Parallel()

	tenant := newTenant(t)
	t.Cleanup(func() { helpers.PurgeWorkspace(t, testDB, tenant.WorkspacePublicID) })

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	wsID, userID := lookupWorkspaceAndOwner(ctx, t, tenant.WorkspacePublicID)
	calID := seedPersonalCalendar(ctx, t, wsID, userID, "drift-enabled")
	taskID := seedTask(ctx, t, wsID, userID, "orphaned task", "2026-07-01")
	eventID := seedLinkedEvent(ctx, t, wsID, calID, userID, taskID, "due",
		"2026-07-01 00:00:00", "2026-07-01 01:00:00")

	// Disable the task directly (simulating a crashed DeleteTask tx
	// that updated tasks but not calendar_events).
	_, err := testDB.ExecContext(ctx,
		`UPDATE tasks SET enabled = FALSE WHERE id = ?`, taskID)
	require.NoError(t, err)

	sink := newRecordingMetrics()
	rec := &reconciler.Reconciler{DB: testDB, Logger: slog.Default(), Metrics: sink}
	rec.RunOnce(ctx)

	var eventEnabled bool
	require.NoError(t, testDB.QueryRowContext(ctx,
		`SELECT enabled FROM calendar_events WHERE id = ?`, eventID).Scan(&eventEnabled))
	require.False(t, eventEnabled, "event should be disabled after reconcile")
	require.GreaterOrEqual(t, sink.heal["enabled_mismatch"], 1)
}

// TestReconcilerCleanStateLeavesTaskAlone verifies that the
// reconciler does not alter a task whose due_on already matches its
// linked event's start date. (Drift counts cannot be asserted to 0
// here because other parallel tests share the DB.)
func TestReconcilerCleanStateLeavesTaskAlone(t *testing.T) {
	bootstrap(t)
	t.Parallel()

	tenant := newTenant(t)
	t.Cleanup(func() { helpers.PurgeWorkspace(t, testDB, tenant.WorkspacePublicID) })

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	wsID, userID := lookupWorkspaceAndOwner(ctx, t, tenant.WorkspacePublicID)
	calID := seedPersonalCalendar(ctx, t, wsID, userID, "clean")
	taskID := seedTask(ctx, t, wsID, userID, "clean task", "2026-08-15")
	seedLinkedEvent(ctx, t, wsID, calID, userID, taskID, "due",
		"2026-08-15 14:00:00", "2026-08-15 15:00:00")

	sink := newRecordingMetrics()
	rec := &reconciler.Reconciler{DB: testDB, Logger: slog.Default(), Metrics: sink}
	rec.RunOnce(ctx)

	var dueOn sql.NullString
	require.NoError(t, testDB.QueryRowContext(ctx,
		`SELECT DATE_FORMAT(due_on, '%Y-%m-%d') FROM tasks WHERE id = ?`, taskID).
		Scan(&dueOn))
	require.True(t, dueOn.Valid)
	require.Equal(t, "2026-08-15", dueOn.String, "reconciler must not alter clean rows")
	require.Equal(t, 1, sink.runs)
	require.Equal(t, 0, sink.errs, "reconciler must not raise errors in a clean scan")
}

// ---- seed helpers (local to this test file) --------------------------------

func lookupWorkspaceAndOwner(ctx context.Context, t *testing.T, wsPublicID string) (uint32, uint32) {
	t.Helper()
	var wsID, userID uint32
	err := testDB.QueryRowContext(ctx,
		`SELECT w.id, wm.user_id
		 FROM workspaces w
		 JOIN workspace_members wm ON wm.workspace_id = w.id AND wm.role = 'owner'
		 WHERE w.public_id = UUID_TO_BIN(?, 0)
		 LIMIT 1`, wsPublicID).Scan(&wsID, &userID)
	require.NoError(t, err)
	return wsID, userID
}

func seedPersonalCalendar(ctx context.Context, t *testing.T, wsID, userID uint32, name string) uint32 {
	t.Helper()
	res, err := testDB.ExecContext(ctx,
		`INSERT INTO calendars (public_id, workspace_id, kind, name, owner_user_id)
		 VALUES (UUID_TO_BIN(UUID(), 0), ?, 'personal', ?, ?)`,
		wsID, name, userID)
	require.NoError(t, err)
	id, err := res.LastInsertId()
	require.NoError(t, err)
	return uint32(id) //#nosec G115 -- LastInsertId in test seed, fits uint32
}

// seedTask inserts a minimal tasks row. dueOn is empty string when the
// caller wants NULL.
func seedTask(ctx context.Context, t *testing.T, wsID, userID uint32, title, dueOn string) uint32 {
	t.Helper()
	// Every task needs a project. Look up the tenant's default project.
	var projID uint32
	err := testDB.QueryRowContext(ctx,
		`SELECT id FROM projects WHERE workspace_id = ? ORDER BY id LIMIT 1`, wsID).Scan(&projID)
	require.NoError(t, err)

	var dOn sql.NullString
	if dueOn != "" {
		dOn = sql.NullString{String: dueOn, Valid: true}
	}
	res, err := testDB.ExecContext(ctx,
		`INSERT INTO tasks (public_id, workspace_id, project_id, title,
		                    due_on, created_by_user_id)
		 VALUES (UUID_TO_BIN(UUID(), 0), ?, ?, ?, ?, ?)`,
		wsID, projID, title, dOn, userID)
	require.NoError(t, err)
	id, err := res.LastInsertId()
	require.NoError(t, err)
	return uint32(id) //#nosec G115 -- LastInsertId in test seed, fits uint32
}

// seedLinkedEvent inserts a calendar_events row linked to taskID with
// the given task_role. startAt / endAt are 'YYYY-MM-DD HH:MM:SS'.
func seedLinkedEvent(ctx context.Context, t *testing.T, wsID, calID, userID, taskID uint32,
	role, startAt, endAt string,
) uint32 {
	t.Helper()
	res, err := testDB.ExecContext(ctx,
		`INSERT INTO calendar_events
		 (public_id, workspace_id, calendar_id, title, start_at, end_at, timezone,
		  owner_user_id, created_by_user_id, task_id, task_role)
		 VALUES (UUID_TO_BIN(UUID(), 0), ?, ?, 'linked', ?, ?, 'UTC',
		         ?, ?, ?, ?)`,
		wsID, calID, startAt, endAt, userID, userID, taskID, role)
	require.NoError(t, err)
	id, err := res.LastInsertId()
	require.NoError(t, err)
	return uint32(id) //#nosec G115 -- LastInsertId in test seed, fits uint32
}
