package itemkit

import (
	"context"
	"database/sql"
	"strings"
	"testing"
	"time"
)

// scheduleFixture seeds a workspace and returns it together with the
// internal id of a task-projected calendar event, so each guard case can
// start from a row the guard is supposed to protect.
func scheduleFixture(t *testing.T, db *sql.DB) (fixtures, uint32) {
	t.Helper()
	fx := seed(context.Background(), t, db)
	t.Cleanup(func() { purge(t, db, fx.wsID) })

	start := time.Date(2030, 7, 1, 10, 0, 0, 0, time.UTC)
	var evtID uint32
	withTx(t, db, func(tx *sql.Tx) {
		var err error
		_, evtID, err = ScheduleTask(context.Background(), tx, ScheduleTaskArgs{
			WorkspaceID: fx.wsID,
			TaskID:      fx.taskID,
			CalendarID:  fx.calendarID,
			ActorUserID: fx.userID,
			Role:        RoleDue,
			Title:       "Guarded task",
			StartAt:     start,
			EndAt:       start.Add(time.Hour),
			Timezone:    "UTC",
		})
		if err != nil {
			t.Fatalf("ScheduleTask: %v", err)
		}
	})
	return fx, evtID
}

// requireGuardRejects asserts the statement fails with the trigger's
// SQLSTATE 45000 rather than with some unrelated error, so a schema
// mistake cannot be mistaken for the guard doing its job.
func requireGuardRejects(t *testing.T, db *sql.DB, what, query string, args ...any) {
	t.Helper()
	_, err := db.ExecContext(context.Background(), query, args...)
	if err == nil {
		t.Fatalf("%s: expected the projection guard to reject the write, got nil error", what)
	}
	if !strings.Contains(err.Error(), "Error 1644 (45000)") {
		t.Fatalf("%s: expected SQLSTATE 45000 from the projection guard, got: %v", what, err)
	}
}

// TestProjectionGuardRejectsUnguardedWrites is the reason the trigger
// exists: a writer that is not the projection engine must be refused at
// the database, not merely discouraged in documentation. Each case here
// corresponds to a column that mirrors a task field.
func TestProjectionGuardRejectsUnguardedWrites(t *testing.T) {
	db := startDB(t)
	_, evtID := scheduleFixture(t, db)

	t.Run("title", func(t *testing.T) {
		requireGuardRejects(t, db, "direct title update",
			`UPDATE calendar_events SET title = 'renamed behind itemkit' WHERE id = ?`, evtID)
	})

	t.Run("start_at", func(t *testing.T) {
		requireGuardRejects(t, db, "direct start_at update",
			`UPDATE calendar_events SET start_at = '2031-01-01 09:00:00' WHERE id = ?`, evtID)
	})

	t.Run("soft delete", func(t *testing.T) {
		requireGuardRejects(t, db, "direct soft delete",
			`UPDATE calendar_events SET enabled = FALSE WHERE id = ?`, evtID)
	})

	t.Run("hard delete", func(t *testing.T) {
		requireGuardRejects(t, db, "direct hard delete",
			`DELETE FROM calendar_events WHERE id = ?`, evtID)
	})

	t.Run("unlink", func(t *testing.T) {
		requireGuardRejects(t, db, "clearing the projection link",
			`UPDATE calendar_events SET task_id = NULL, task_role = NULL WHERE id = ?`, evtID)
	})
}

// TestProjectionGuardAllowsUnmirroredColumns pins the other half of the
// boundary. Columns with no task-side counterpart stay editable on a
// projected row, so the calendar UI can still offer them.
func TestProjectionGuardAllowsUnmirroredColumns(t *testing.T) {
	db := startDB(t)
	_, evtID := scheduleFixture(t, db)

	if _, err := db.ExecContext(context.Background(),
		`UPDATE calendar_events SET memo = 'notes', location = 'Room 1', show_as = 'tentative' WHERE id = ?`,
		evtID,
	); err != nil {
		t.Fatalf("expected unmirrored columns to stay writable on a projected event: %v", err)
	}
}

// TestProjectionGuardShapeInvariantsAreUnconditional covers the two
// invariants the table comment documents but MySQL cannot express as
// CHECK constraints, because task_id is used in a foreign key
// referential action. They hold even for the projection engine.
func TestProjectionGuardShapeInvariantsAreUnconditional(t *testing.T) {
	db := startDB(t)
	_, evtID := scheduleFixture(t, db)

	ctx := context.Background()
	conn, err := db.Conn(ctx)
	if err != nil {
		t.Fatalf("acquire conn: %v", err)
	}
	defer func() { _ = conn.Close() }()
	if _, err := conn.ExecContext(ctx, "SET @nf_item_projection_engine = 1"); err != nil {
		t.Fatalf("arm projection guard: %v", err)
	}
	defer func() { _, _ = conn.ExecContext(ctx, "SET @nf_item_projection_engine = NULL") }()

	if _, err := conn.ExecContext(ctx,
		`UPDATE calendar_events SET task_role = NULL WHERE id = ?`, evtID,
	); err == nil || !strings.Contains(err.Error(), "Error 1644 (45000)") {
		t.Fatalf("expected task_id/task_role to be rejected when only one is cleared, got: %v", err)
	}

	if _, err := conn.ExecContext(ctx,
		`UPDATE calendar_events SET recurrence_rule = '{"freq":"WEEKLY"}' WHERE id = ?`, evtID,
	); err == nil || !strings.Contains(err.Error(), "Error 1644 (45000)") {
		t.Fatalf("expected a recurrence rule on a task-projected event to be rejected, got: %v", err)
	}
}

// TestProjectionGuardDisarmsAfterItemkitReturns is the pooling case. The
// session variable outlives the transaction it was set in, so an entry
// point that forgot to clear it would hand the next checkout of that
// connection a database with the guard down.
func TestProjectionGuardDisarmsAfterItemkitReturns(t *testing.T) {
	db := startDB(t)
	fx := seed(context.Background(), t, db)
	t.Cleanup(func() { purge(t, db, fx.wsID) })

	ctx := context.Background()
	conn, err := db.Conn(ctx)
	if err != nil {
		t.Fatalf("acquire conn: %v", err)
	}
	defer func() { _ = conn.Close() }()

	tx, err := conn.BeginTx(ctx, nil)
	if err != nil {
		t.Fatalf("begin tx: %v", err)
	}
	start := time.Date(2030, 8, 1, 10, 0, 0, 0, time.UTC)
	if _, _, err := ScheduleTask(ctx, tx, ScheduleTaskArgs{
		WorkspaceID: fx.wsID,
		TaskID:      fx.taskID,
		CalendarID:  fx.calendarID,
		ActorUserID: fx.userID,
		Role:        RoleDue,
		Title:       "Guarded task",
		StartAt:     start,
		EndAt:       start.Add(time.Hour),
		Timezone:    "UTC",
	}); err != nil {
		t.Fatalf("ScheduleTask: %v", err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatalf("commit: %v", err)
	}

	var armed sql.NullInt64
	if err := conn.QueryRowContext(ctx, "SELECT @nf_item_projection_engine").Scan(&armed); err != nil {
		t.Fatalf("read session variable: %v", err)
	}
	if armed.Valid {
		t.Fatalf("itemkit left the projection guard armed on the connection (= %d); "+
			"the next request to reuse it would bypass the trigger", armed.Int64)
	}
}
