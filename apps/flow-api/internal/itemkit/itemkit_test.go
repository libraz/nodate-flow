package itemkit

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/dbretry"
	"github.com/libraz/nodate-flow/packages/go-shared/dbtype"
	"github.com/libraz/nodate-flow/packages/go-shared/testhelpers"

	"github.com/libraz/nodate-flow/apps/flow-api/internal/taskrules"
)

// itemkit tests use a shared MySQL testcontainer, so every entry point
// goes through testhelpers.SkipUnlessIntegration.
var shared = testhelpers.NewSharedMySQL(testhelpers.MySQLConfig{
	Database: "itemkit_test",
})

func TestMain(m *testing.M) {
	os.Exit(m.Run())
}

type fixtures struct {
	wsID         uint32
	userID       uint32
	projectID    uint32
	calendarID   uint32
	taskID       uint32
	taskPublicID dbtype.PublicID
}

// seed inserts the minimum rows itemkit needs to operate: a
// workspace, a user, a project, a personal calendar, and a task.
// Returns internal IDs plus the task's public_id.
func seed(ctx context.Context, t *testing.T, db *sql.DB) fixtures {
	t.Helper()
	suffix := fmt.Sprintf("%d", time.Now().UnixNano())

	exec := func(q string, args ...any) int64 {
		res, err := db.ExecContext(ctx, q, args...)
		if err != nil {
			t.Fatalf("seed exec %q: %v", q, err)
		}
		id, err := res.LastInsertId()
		if err != nil {
			t.Fatalf("seed last id: %v", err)
		}
		return id
	}

	wsID := uint32(exec( //#nosec G115 -- LastInsertId in test seed, fits uint32
		`INSERT INTO workspaces (public_id, slug, name, timezone) VALUES (?, ?, ?, 'UTC')`,
		dbtype.New(), "ws-"+suffix[:10], "ItemKit Test "+suffix,
	))
	userID := uint32(exec( //#nosec G115 -- LastInsertId in test seed, fits uint32
		`INSERT INTO users (public_id, email, display_name, locale, timezone)
		 VALUES (?, ?, ?, 'en', 'UTC')`,
		dbtype.New(), "itemkit+"+suffix+"@example.test", "ItemKit Tester",
	))
	exec(
		`INSERT INTO workspace_members (public_id, workspace_id, user_id, role, joined_at)
		 VALUES (?, ?, ?, 'owner', NOW())`,
		dbtype.New(), wsID, userID,
	)
	projectID := uint32(exec( //#nosec G115 -- LastInsertId in test seed, fits uint32
		`INSERT INTO projects (public_id, workspace_id, name, slug, identifier)
		 VALUES (?, ?, ?, ?, ?)`,
		dbtype.New(), wsID, "ItemKit Test Project", "pj-"+suffix[:10], "IKT",
	))
	calendarID := uint32(exec( //#nosec G115 -- LastInsertId in test seed, fits uint32
		`INSERT INTO calendars (public_id, workspace_id, kind, name, owner_user_id)
		 VALUES (?, ?, 'personal', 'Main', ?)`,
		dbtype.New(), wsID, userID,
	))
	taskPub := dbtype.New()
	taskID := uint32(exec( //#nosec G115 -- LastInsertId in test seed, fits uint32
		`INSERT INTO tasks (public_id, workspace_id, project_id, task_number, title, visibility)
		 VALUES (?, ?, ?, 1, 'Test task', 'public')`,
		taskPub, wsID, projectID,
	))

	return fixtures{
		wsID: wsID, userID: userID, projectID: projectID,
		calendarID: calendarID, taskID: taskID, taskPublicID: taskPub,
	}
}

// purge removes every row touched by the test so parallel runs of
// TestMain-level test suites stay isolated. FK checks are off so delete
// order does not matter, and the projection guard is armed because the
// tests create task-projected calendar_events rows — hard-deleting one is
// exactly what trg_calendar_events_projection_guard_del refuses. Teardown
// is a legitimate engine-side write, so it opts in the way itemkit does.
//
// Everything runs on one pinned *sql.Conn. Both settings are
// session-scoped, so issuing them through the pool would let the DELETEs
// land on a different connection than the SETs and silently leave rows
// behind.
func purge(t *testing.T, db *sql.DB, wsID uint32) {
	t.Helper()
	ctx := context.Background()
	conn, err := db.Conn(ctx)
	if err != nil {
		t.Logf("purge: acquire conn: %v", err)
		return
	}
	defer func() { _ = conn.Close() }()

	if _, err := conn.ExecContext(ctx, "SET FOREIGN_KEY_CHECKS = 0"); err != nil {
		t.Logf("purge: FK off: %v", err)
	}
	if _, err := conn.ExecContext(ctx, "SET @nf_item_projection_engine = 1"); err != nil {
		t.Logf("purge: arm projection guard: %v", err)
	}
	for _, q := range []string{
		`DELETE FROM events WHERE workspace_id = ?`,
		`DELETE FROM calendar_events WHERE workspace_id = ?`,
		`DELETE FROM tasks WHERE workspace_id = ?`,
		`DELETE FROM calendars WHERE workspace_id = ?`,
		`DELETE FROM projects WHERE workspace_id = ?`,
		`DELETE FROM workspace_members WHERE workspace_id = ?`,
		`DELETE FROM workspaces WHERE id = ?`,
	} {
		if _, err := conn.ExecContext(ctx, q, wsID); err != nil {
			t.Logf("purge %q: %v", q, err)
		}
	}
	if _, err := conn.ExecContext(ctx, "SET @nf_item_projection_engine = NULL"); err != nil {
		t.Logf("purge: disarm projection guard: %v", err)
	}
	if _, err := conn.ExecContext(ctx, "SET FOREIGN_KEY_CHECKS = 1"); err != nil {
		t.Logf("purge: FK on: %v", err)
	}
}

func startDB(t *testing.T) *sql.DB {
	t.Helper()
	testhelpers.SkipUnlessIntegration(t)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	inst, err := shared.Start(ctx)
	if err != nil {
		t.Fatalf("start mysql: %v", err)
	}
	return inst.DB
}

// withTx runs fn inside the transaction type itemkit writes through and
// commits. It goes through dbretry.InTx rather than db.BeginTx because
// itemkit appends to the event log, and the appender only accepts a
// transaction whose commit it can wait for.
func withTx(t *testing.T, db *sql.DB, fn func(tx TX)) {
	t.Helper()
	err := dbretry.InTx(context.Background(), db, "itemkit.test", nil,
		func(_ context.Context, tx *dbretry.Tx) error {
			fn(tx)
			return nil
		})
	if err != nil {
		t.Fatalf("tx: %v", err)
	}
}

// inTxErr is withTx for the cases that expect a failure: fn's error is
// returned to the caller, and returning it also rolls the transaction
// back.
func inTxErr(ctx context.Context, db *sql.DB, fn func(tx TX) error) error {
	return dbretry.InTx(ctx, db, "itemkit.test", nil,
		func(_ context.Context, tx *dbretry.Tx) error { return fn(tx) })
}

// --- Scenarios ---------------------------------------------------------------

func TestScheduleTaskCreatesLink(t *testing.T) {
	db := startDB(t)
	fx := seed(context.Background(), t, db)
	defer purge(t, db, fx.wsID)

	start := time.Date(2030, 6, 3, 10, 0, 0, 0, time.UTC)
	end := start.Add(time.Hour)
	var evtPub dbtype.PublicID
	var evtID uint32
	withTx(t, db, func(tx TX) {
		var err error
		evtPub, evtID, err = ScheduleTask(context.Background(), tx, ScheduleTaskArgs{
			WorkspaceID: fx.wsID,
			TaskID:      fx.taskID,
			CalendarID:  fx.calendarID,
			ActorUserID: fx.userID,
			Role:        RoleDue,
			Title:       "Test task",
			StartAt:     start,
			EndAt:       end,
			Timezone:    "UTC",
		})
		if err != nil {
			t.Fatalf("ScheduleTask: %v", err)
		}
	})

	// tasks.due_on should now match start date.
	var dueOn sql.NullTime
	if err := db.QueryRow(`SELECT due_on FROM tasks WHERE id = ?`, fx.taskID).Scan(&dueOn); err != nil {
		t.Fatalf("read task: %v", err)
	}
	if !dueOn.Valid || !sameDate(dueOn.Time, start) {
		t.Fatalf("tasks.due_on = %v, want %v", dueOn, start)
	}

	// Exactly one linked event.
	var count int
	if err := db.QueryRow(
		`SELECT COUNT(*) FROM calendar_events WHERE task_id = ? AND task_role = 'due' AND enabled = TRUE`,
		fx.taskID,
	).Scan(&count); err != nil {
		t.Fatalf("count events: %v", err)
	}
	if count != 1 {
		t.Fatalf("linked event count = %d, want 1", count)
	}
	if evtPub == (dbtype.PublicID{}) || evtID == 0 {
		t.Fatalf("ScheduleTask returned zero id/public id")
	}

	// events row recorded with item.scheduled kind.
	var kindCount int
	if err := db.QueryRow(
		`SELECT COUNT(*) FROM events WHERE workspace_id = ? AND type = 'item.scheduled'`,
		fx.wsID,
	).Scan(&kindCount); err != nil {
		t.Fatalf("count events row: %v", err)
	}
	if kindCount == 0 {
		t.Fatalf("no item.scheduled event row")
	}
}

func TestRescheduleTaskPropagatesToEvent(t *testing.T) {
	db := startDB(t)
	fx := seed(context.Background(), t, db)
	defer purge(t, db, fx.wsID)

	start := time.Date(2030, 6, 3, 10, 0, 0, 0, time.UTC)
	end := start.Add(time.Hour)
	withTx(t, db, func(tx TX) {
		if _, _, err := ScheduleTask(context.Background(), tx, ScheduleTaskArgs{
			WorkspaceID: fx.wsID, TaskID: fx.taskID, CalendarID: fx.calendarID,
			ActorUserID: fx.userID, Role: RoleDue, Title: "T", StartAt: start, EndAt: end, Timezone: "UTC",
		}); err != nil {
			t.Fatalf("ScheduleTask: %v", err)
		}
	})

	newDate := time.Date(2030, 6, 10, 0, 0, 0, 0, time.UTC)
	withTx(t, db, func(tx TX) {
		if err := RescheduleTask(context.Background(), tx, RescheduleTaskArgs{
			WorkspaceID: fx.wsID,
			TaskID:      fx.taskID,
			ActorUserID: fx.userID,
			SetDueOn:    true,
			DueOn:       newDate,
		}); err != nil {
			t.Fatalf("RescheduleTask: %v", err)
		}
	})

	var evtStart sql.NullTime
	if err := db.QueryRow(
		`SELECT start_at FROM calendar_events WHERE task_id = ? AND task_role = 'due' AND enabled = TRUE`,
		fx.taskID,
	).Scan(&evtStart); err != nil {
		t.Fatalf("read event: %v", err)
	}
	if !evtStart.Valid || !sameDate(evtStart.Time, newDate) {
		t.Fatalf("event start date did not follow task due_on: got %v want date %v",
			evtStart, newDate)
	}
	// Time-of-day portion must be preserved (10:00).
	if evtStart.Time.Hour() != 10 {
		t.Fatalf("event hour not preserved: got %d, want 10", evtStart.Time.Hour())
	}
}

func TestDeleteTaskCascadesEvents(t *testing.T) {
	db := startDB(t)
	fx := seed(context.Background(), t, db)
	defer purge(t, db, fx.wsID)

	start := time.Date(2030, 6, 3, 10, 0, 0, 0, time.UTC)
	withTx(t, db, func(tx TX) {
		if _, _, err := ScheduleTask(context.Background(), tx, ScheduleTaskArgs{
			WorkspaceID: fx.wsID, TaskID: fx.taskID, CalendarID: fx.calendarID,
			ActorUserID: fx.userID, Role: RoleDue, Title: "T", StartAt: start, EndAt: start.Add(time.Hour), Timezone: "UTC",
		}); err != nil {
			t.Fatalf("ScheduleTask: %v", err)
		}
	})

	withTx(t, db, func(tx TX) {
		if err := DeleteTask(context.Background(), tx, fx.wsID, fx.taskID, fx.userID); err != nil {
			t.Fatalf("DeleteTask: %v", err)
		}
	})

	var taskEnabled, evtEnabled bool
	if err := db.QueryRow(`SELECT enabled FROM tasks WHERE id = ?`, fx.taskID).Scan(&taskEnabled); err != nil {
		t.Fatalf("read task: %v", err)
	}
	if err := db.QueryRow(
		`SELECT enabled FROM calendar_events WHERE task_id = ? AND task_role = 'due'`,
		fx.taskID,
	).Scan(&evtEnabled); err != nil {
		t.Fatalf("read event: %v", err)
	}
	if taskEnabled || evtEnabled {
		t.Fatalf("cascade broken: task.enabled=%v event.enabled=%v", taskEnabled, evtEnabled)
	}
}

func TestRenameFromTaskPropagatesToEvent(t *testing.T) {
	db := startDB(t)
	fx := seed(context.Background(), t, db)
	defer purge(t, db, fx.wsID)

	start := time.Date(2030, 6, 3, 10, 0, 0, 0, time.UTC)
	withTx(t, db, func(tx TX) {
		if _, _, err := ScheduleTask(context.Background(), tx, ScheduleTaskArgs{
			WorkspaceID: fx.wsID, TaskID: fx.taskID, CalendarID: fx.calendarID,
			ActorUserID: fx.userID, Role: RoleDue, Title: "Old", StartAt: start, EndAt: start.Add(time.Hour), Timezone: "UTC",
		}); err != nil {
			t.Fatalf("ScheduleTask: %v", err)
		}
	})

	newTitle, err := taskrules.NewTitle("New")
	if err != nil {
		t.Fatalf("NewTitle: %v", err)
	}
	withTx(t, db, func(tx TX) {
		if err := RenameItem(context.Background(), tx, RenameItemArgs{
			WorkspaceID: fx.wsID, ActorUserID: fx.userID, TaskID: fx.taskID, NewTitle: newTitle,
		}); err != nil {
			t.Fatalf("RenameItem: %v", err)
		}
	})

	var taskTitle, evtTitle string
	if err := db.QueryRow(`SELECT title FROM tasks WHERE id = ?`, fx.taskID).Scan(&taskTitle); err != nil {
		t.Fatalf("read task: %v", err)
	}
	if err := db.QueryRow(
		`SELECT title FROM calendar_events WHERE task_id = ? AND task_role = 'due'`,
		fx.taskID,
	).Scan(&evtTitle); err != nil {
		t.Fatalf("read event: %v", err)
	}
	if taskTitle != "New" || evtTitle != "New" {
		t.Fatalf("rename propagation: task=%q event=%q want both 'New'", taskTitle, evtTitle)
	}
}

// sameDate reports whether two times share the same calendar date
// ignoring time-of-day (UTC comparison).
func sameDate(a, b time.Time) bool {
	ay, am, ad := a.Date()
	by, bm, bd := b.Date()
	return ay == by && am == bm && ad == bd
}
