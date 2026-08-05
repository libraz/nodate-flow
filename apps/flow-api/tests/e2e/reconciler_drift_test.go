package e2e

import (
	"context"
	"database/sql"
	"log/slog"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/libraz/nodate-flow/apps/flow-api/internal/reconciler"
	"github.com/libraz/nodate-flow/apps/flow-api/tests/helpers"
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

	requireEveryDriftHealed(t, sink, "date_drift_due")
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
	requireEveryDriftHealed(t, sink, "enabled_mismatch")
}

// requireEveryDriftHealed asserts the reconciler healed every drift the
// pass detected, for the given kind.
//
// The obvious assertion — this pass healed at least one — cannot be
// made here. The reconciler scans the whole instance, and each test in
// this file runs its own pass against the same database, so a sibling's
// pass can reach this test's pair first. The winner counts the heal on
// its own sink; the pair's owner then finds nothing left to fix and
// counts nothing, which is indistinguishable from a reconciler that
// does not record its work at all.
//
// What no pass may do is spot a drift and leave it: the heal is skipped
// on error and only logged, so a detected-but-unhealed pair is exactly
// the silent failure this suite is here to catch. That relation holds
// no matter which pass got there first, and the state assertion above
// covers the invariant itself.
func requireEveryDriftHealed(t *testing.T, sink *recordingMetrics, kind string) {
	t.Helper()
	require.GreaterOrEqualf(t, sink.heal[kind], sink.inconsistency[kind],
		"the pass detected %d %s drifts but healed only %d",
		sink.inconsistency[kind], kind, sink.heal[kind])
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

	// The error counter is deliberately not asserted. It counts every
	// scan and heal failure the pass met anywhere in the instance, and
	// the pass walks the workspaces every other test in this suite is
	// holding — so a heal that lost a lock race in someone else's data
	// would fail this test, which is about one task not being touched.
	// A counter that cannot be attributed to this test's rows says
	// nothing about this test's subject, and an assertion that can fail
	// for reasons unrelated to what it claims to check is worse than no
	// assertion at all.
}

// TestReconcilerHealsDueOnDriftInTheEventTimezone seeds a Tokyo morning
// meeting whose deadline carries the UTC date — the exact state the
// old derivation produced — and asserts the reconciler heals it to the
// Tokyo date.
//
// Nothing about this pair looks wrong in UTC: DATE(start_at) and due_on
// agree, which is why the SQL-side comparison walked past it.
func TestReconcilerHealsDueOnDriftInTheEventTimezone(t *testing.T) {
	bootstrap(t)
	t.Parallel()

	tenant := newTenant(t)
	t.Cleanup(func() { helpers.PurgeWorkspace(t, testDB, tenant.WorkspacePublicID) })

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	wsID, userID := lookupWorkspaceAndOwner(ctx, t, tenant.WorkspacePublicID)
	calID := seedPersonalCalendar(ctx, t, wsID, userID, "drift-tokyo")

	// 2026-06-08 08:00 Asia/Tokyo.
	taskID := seedTask(ctx, t, wsID, userID, "tokyo standup task", "2026-06-07")
	seedLinkedEventInZone(ctx, t, wsID, calID, userID, taskID, "due",
		"2026-06-07 23:00:00", "2026-06-08 00:00:00", "Asia/Tokyo")

	sink := newRecordingMetrics()
	rec := &reconciler.Reconciler{DB: testDB, Logger: slog.Default(), Metrics: sink}
	rec.RunOnce(ctx)

	var dueOn sql.NullString
	require.NoError(t, testDB.QueryRowContext(ctx,
		`SELECT DATE_FORMAT(due_on, '%Y-%m-%d') FROM tasks WHERE id = ?`, taskID).
		Scan(&dueOn))
	require.True(t, dueOn.Valid)
	require.Equal(t, "2026-06-08", dueOn.String,
		"the deadline belongs to the Tokyo day the meeting is on")

	requireEveryDriftHealed(t, sink, "date_drift_due")
}

// TestReconcilerLeavesACorrectZonedDueDateAlone is the one that matters
// most: a correctly-dated pair must survive the loop.
//
// Judged in UTC this pair looks drifted, so the reconciler used to
// "heal" it backwards every five minutes — overwriting both itemkit's
// correct write and any manual correction, with no error anywhere. A
// background loop that reverts the fix makes the bug unfixable from the
// outside.
func TestReconcilerLeavesACorrectZonedDueDateAlone(t *testing.T) {
	bootstrap(t)
	t.Parallel()

	tenant := newTenant(t)
	t.Cleanup(func() { helpers.PurgeWorkspace(t, testDB, tenant.WorkspacePublicID) })

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	wsID, userID := lookupWorkspaceAndOwner(ctx, t, tenant.WorkspacePublicID)
	calID := seedPersonalCalendar(ctx, t, wsID, userID, "clean-tokyo")

	taskID := seedTask(ctx, t, wsID, userID, "correctly dated task", "2026-06-08")
	seedLinkedEventInZone(ctx, t, wsID, calID, userID, taskID, "due",
		"2026-06-07 23:00:00", "2026-06-08 00:00:00", "Asia/Tokyo")

	sink := newRecordingMetrics()
	rec := &reconciler.Reconciler{DB: testDB, Logger: slog.Default(), Metrics: sink}
	// Two passes, because the failure this guards against is a loop that
	// re-asserts itself on every tick. One pass cannot tell a loop that
	// leaves the row alone from one that has not come round yet.
	rec.RunOnce(ctx)
	rec.RunOnce(ctx)

	var dueOn sql.NullString
	require.NoError(t, testDB.QueryRowContext(ctx,
		`SELECT DATE_FORMAT(due_on, '%Y-%m-%d') FROM tasks WHERE id = ?`, taskID).
		Scan(&dueOn))
	require.True(t, dueOn.Valid)
	require.Equal(t, "2026-06-08", dueOn.String,
		"the reconciler must not drag a correct deadline back to its UTC date")
}

// TestReconcilerReportsAnUnresolvableTimezone asserts a row naming a
// zone that does not exist is surfaced rather than healed toward a
// UTC-derived date, which would look plausible and be wrong.
func TestReconcilerReportsAnUnresolvableTimezone(t *testing.T) {
	bootstrap(t)
	t.Parallel()

	tenant := newTenant(t)
	t.Cleanup(func() { helpers.PurgeWorkspace(t, testDB, tenant.WorkspacePublicID) })

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	wsID, userID := lookupWorkspaceAndOwner(ctx, t, tenant.WorkspacePublicID)
	calID := seedPersonalCalendar(ctx, t, wsID, userID, "bad-zone")

	taskID := seedTask(ctx, t, wsID, userID, "unresolvable zone task", "2026-06-07")
	seedLinkedEventInZone(ctx, t, wsID, calID, userID, taskID, "due",
		"2026-06-07 23:00:00", "2026-06-08 00:00:00", "Mars/Olympus")

	sink := newRecordingMetrics()
	rec := &reconciler.Reconciler{DB: testDB, Logger: slog.Default(), Metrics: sink}
	rec.RunOnce(ctx)

	require.GreaterOrEqual(t, sink.inconsistency["event_timezone_invalid"], 1,
		"an unresolvable zone must be reported")

	var dueOn sql.NullString
	require.NoError(t, testDB.QueryRowContext(ctx,
		`SELECT DATE_FORMAT(due_on, '%Y-%m-%d') FROM tasks WHERE id = ?`, taskID).
		Scan(&dueOn))
	require.True(t, dueOn.Valid)
	require.Equal(t, "2026-06-07", dueOn.String,
		"a row with no resolvable zone has no date to heal toward")
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
//
// These tests exist to prove the reconciler heals drift, so they have to
// manufacture drift that the normal write path cannot produce. That
// means writing a task-projected row directly, which the projection
// guard refuses unless the session declares itself the engine. The
// declaration is scoped to a pinned connection and cleared before the
// connection returns to the pool, so no later query inherits it.
func seedLinkedEvent(ctx context.Context, t *testing.T, wsID, calID, userID, taskID uint32,
	role, startAt, endAt string,
) uint32 {
	t.Helper()
	return seedLinkedEventInZone(ctx, t, wsID, calID, userID, taskID, role, startAt, endAt, "UTC")
}

// seedLinkedEventInZone is seedLinkedEvent with the event's timezone
// spelled out. startAt / endAt stay UTC instants — the zone is what the
// event claims its wall clock is read in, which is what decides the
// calendar date the deadline should carry.
func seedLinkedEventInZone(ctx context.Context, t *testing.T, wsID, calID, userID, taskID uint32,
	role, startAt, endAt, tz string,
) uint32 {
	t.Helper()
	conn, err := testDB.Conn(ctx)
	require.NoError(t, err)
	defer func() { _ = conn.Close() }()

	_, err = conn.ExecContext(ctx, "SET @nf_item_projection_engine = 1")
	require.NoError(t, err)
	defer func() { _, _ = conn.ExecContext(ctx, "SET @nf_item_projection_engine = NULL") }()

	res, err := conn.ExecContext(ctx,
		`INSERT INTO calendar_events
		 (public_id, workspace_id, calendar_id, title, start_at, end_at, timezone,
		  owner_user_id, created_by_user_id, task_id, task_role)
		 VALUES (UUID_TO_BIN(UUID(), 0), ?, ?, 'linked', ?, ?, ?,
		         ?, ?, ?, ?)`,
		wsID, calID, startAt, endAt, tz, userID, userID, taskID, role)
	require.NoError(t, err)
	id, err := res.LastInsertId()
	require.NoError(t, err)
	return uint32(id) //#nosec G115 -- LastInsertId in test seed, fits uint32
}
