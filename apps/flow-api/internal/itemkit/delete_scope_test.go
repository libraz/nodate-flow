package itemkit

import (
	"context"
	"database/sql"
	"testing"
	"time"
)

// eventEnabled reads one calendar_events row's enabled flag by internal
// id and nothing else. The scoping assertions below have to be answered
// by the row itself rather than by a workspace predicate in the reading
// query, which would hide a write that crossed a tenant.
func eventEnabled(t *testing.T, db *sql.DB, eventID uint32) bool {
	t.Helper()
	var enabled bool
	if err := db.QueryRow(`SELECT enabled FROM calendar_events WHERE id = ?`, eventID).Scan(&enabled); err != nil {
		t.Fatalf("read calendar_events id=%d: %v", eventID, err)
	}
	return enabled
}

// taskEnabled reads one tasks row's enabled flag by internal id, on the
// same reasoning as eventEnabled.
func taskEnabled(t *testing.T, db *sql.DB, taskID uint32) bool {
	t.Helper()
	var enabled bool
	if err := db.QueryRow(`SELECT enabled FROM tasks WHERE id = ?`, taskID).Scan(&enabled); err != nil {
		t.Fatalf("read tasks id=%d: %v", taskID, err)
	}
	return enabled
}

// taskHasDueOn reports whether a task still carries a deadline.
func taskHasDueOn(t *testing.T, db *sql.DB, taskID uint32) bool {
	t.Helper()
	var dueOn sql.NullTime
	if err := db.QueryRow(`SELECT due_on FROM tasks WHERE id = ?`, taskID).Scan(&dueOn); err != nil {
		t.Fatalf("read tasks id=%d due_on: %v", taskID, err)
	}
	return dueOn.Valid
}

// scheduleTimeBlock adds one `scheduled` projection to the fixture's task
// and returns its internal id. A task may hold several of these, which is
// what a role-scoped withdrawal has to reach all of.
func scheduleTimeBlock(t *testing.T, db *sql.DB, fx fixtures, start time.Time) uint32 {
	t.Helper()
	var evtID uint32
	withTx(t, db, func(tx TX) {
		var err error
		_, evtID, err = ScheduleTask(context.Background(), tx, ScheduleTaskArgs{
			WorkspaceID: fx.wsID,
			TaskID:      fx.taskID,
			CalendarID:  fx.calendarID,
			ActorUserID: fx.userID,
			Role:        RoleScheduled,
			Title:       "Focus block",
			StartAt:     start,
			EndAt:       start.Add(time.Hour),
			Timezone:    "UTC",
		})
		if err != nil {
			t.Fatalf("ScheduleTask(scheduled, %s): %v", start.Format(time.DateOnly), err)
		}
	})
	return evtID
}

// TestDeleteEventDisablesOnlyTheNamedWorkspacesEvent pins both halves of
// the delete: the event named by the caller loses its enabled flag, and
// nothing outside the caller's workspace moves.
//
// DeleteEvent writes through DisableCalendarEvent, keyed on (public_id,
// calendar_id, workspace_id) and confined to enabled rows. An inline
// statement keyed on the internal id alone passes the first assertion and
// says nothing about the rest, which is why the neighbouring tenant is
// seeded and re-read rather than assumed untouched.
func TestDeleteEventDisablesOnlyTheNamedWorkspacesEvent(t *testing.T) {
	db := startDB(t)
	ctx := context.Background()

	fx, evtID := scheduleFixture(t, db)
	neighbour, neighbourEvtID := scheduleFixture(t, db)

	if fx.wsID == neighbour.wsID {
		t.Fatal("the two fixtures landed in one workspace; the scoping assertions would prove nothing")
	}

	withTx(t, db, func(tx TX) {
		if err := DeleteEvent(ctx, tx, fx.wsID, evtID, fx.userID); err != nil {
			t.Fatalf("DeleteEvent: %v", err)
		}
	})

	if eventEnabled(t, db, evtID) {
		t.Fatalf("calendar_events id=%d is still enabled after DeleteEvent", evtID)
	}
	if !eventEnabled(t, db, neighbourEvtID) {
		t.Fatalf("DeleteEvent in ws %d disabled ws %d's event id=%d",
			fx.wsID, neighbour.wsID, neighbourEvtID)
	}

	// The same call again is the state the caller already asked for: the
	// event reads as gone, so there is nothing to report and nothing to
	// write.
	withTx(t, db, func(tx TX) {
		if err := DeleteEvent(ctx, tx, fx.wsID, evtID, fx.userID); err != nil {
			t.Fatalf("second DeleteEvent on the same event: %v", err)
		}
	})
	if eventEnabled(t, db, evtID) {
		t.Fatalf("calendar_events id=%d came back enabled", evtID)
	}

	// Internal ids run across the whole table, so one workspace can name a
	// number that exists in another. Under this workspace it must resolve
	// to nothing rather than to that workspace's row.
	withTx(t, db, func(tx TX) {
		if err := DeleteEvent(ctx, tx, fx.wsID, neighbourEvtID, fx.userID); err != nil {
			t.Fatalf("DeleteEvent naming another workspace's event id: %v", err)
		}
	})
	if !eventEnabled(t, db, neighbourEvtID) {
		t.Fatalf("event id=%d in ws %d was disabled by a delete issued under ws %d",
			neighbourEvtID, neighbour.wsID, fx.wsID)
	}
}

// TestUnscheduleTaskWithdrawsOnlyThisWorkspacesTimeBlocks pins the
// role-scoped withdrawal. Unscheduling the `scheduled` role takes every
// time block the task holds, leaves the same task's due projection alone,
// and does not reach another tenant's blocks.
//
// The due projection is the assertion that matters most here: it mirrors
// tasks.due_on, so a statement that withdrew it without clearing that
// column would leave the task claiming a deadline nothing stands for.
func TestUnscheduleTaskWithdrawsOnlyThisWorkspacesTimeBlocks(t *testing.T) {
	db := startDB(t)
	ctx := context.Background()

	fx, dueEvtID := scheduleFixture(t, db)
	neighbour, neighbourDueEvtID := scheduleFixture(t, db)
	if fx.wsID == neighbour.wsID {
		t.Fatal("the two fixtures landed in one workspace; the scoping assertions would prove nothing")
	}

	firstBlock := scheduleTimeBlock(t, db, fx, time.Date(2030, 11, 4, 9, 0, 0, 0, time.UTC))
	secondBlock := scheduleTimeBlock(t, db, fx, time.Date(2030, 11, 5, 9, 0, 0, 0, time.UTC))
	neighbourBlock := scheduleTimeBlock(t, db, neighbour, time.Date(2030, 11, 4, 9, 0, 0, 0, time.UTC))

	withTx(t, db, func(tx TX) {
		if err := UnscheduleTask(ctx, tx, UnscheduleTaskArgs{
			WorkspaceID: fx.wsID,
			TaskID:      fx.taskID,
			ActorUserID: fx.userID,
			Role:        RoleScheduled,
		}); err != nil {
			t.Fatalf("UnscheduleTask(scheduled): %v", err)
		}
	})

	if eventEnabled(t, db, firstBlock) || eventEnabled(t, db, secondBlock) {
		t.Fatalf("time blocks id=%d,%d survived the withdrawal of the scheduled role",
			firstBlock, secondBlock)
	}
	if !eventEnabled(t, db, dueEvtID) {
		t.Fatalf("withdrawing the scheduled role disabled the due projection id=%d", dueEvtID)
	}
	if !taskHasDueOn(t, db, fx.taskID) {
		t.Fatal("withdrawing the scheduled role cleared tasks.due_on")
	}
	if !eventEnabled(t, db, neighbourBlock) {
		t.Fatalf("unscheduling in ws %d withdrew ws %d's time block id=%d",
			fx.wsID, neighbour.wsID, neighbourBlock)
	}
	if !eventEnabled(t, db, neighbourDueEvtID) {
		t.Fatalf("unscheduling in ws %d disabled ws %d's due projection id=%d",
			fx.wsID, neighbour.wsID, neighbourDueEvtID)
	}
}

// TestUnscheduleTaskDueWithdrawsOnlyThisWorkspacesEvent covers the shared
// single-row withdrawal. UnscheduleTask's due branch reaches it through
// unlinkEventRow, which resolves the event by task link and must then
// disable that row and no other tenant's.
func TestUnscheduleTaskDueWithdrawsOnlyThisWorkspacesEvent(t *testing.T) {
	db := startDB(t)
	ctx := context.Background()

	fx, dueEvtID := scheduleFixture(t, db)
	neighbour, neighbourDueEvtID := scheduleFixture(t, db)
	if fx.wsID == neighbour.wsID {
		t.Fatal("the two fixtures landed in one workspace; the scoping assertions would prove nothing")
	}

	withTx(t, db, func(tx TX) {
		if err := UnscheduleTask(ctx, tx, UnscheduleTaskArgs{
			WorkspaceID: fx.wsID,
			TaskID:      fx.taskID,
			ActorUserID: fx.userID,
			Role:        RoleDue,
		}); err != nil {
			t.Fatalf("UnscheduleTask(due): %v", err)
		}
	})

	if eventEnabled(t, db, dueEvtID) {
		t.Fatalf("due projection id=%d is still enabled after UnscheduleTask", dueEvtID)
	}
	if taskHasDueOn(t, db, fx.taskID) {
		t.Fatal("the event was withdrawn but tasks.due_on still carries a deadline")
	}
	if !eventEnabled(t, db, neighbourDueEvtID) {
		t.Fatalf("unscheduling in ws %d disabled ws %d's due projection id=%d",
			fx.wsID, neighbour.wsID, neighbourDueEvtID)
	}
	if !taskHasDueOn(t, db, neighbour.taskID) {
		t.Fatalf("unscheduling in ws %d cleared ws %d's tasks.due_on", fx.wsID, neighbour.wsID)
	}
}

// TestDeleteTaskWithdrawsOnlyThisWorkspacesEvents pins the cascade. A
// deleted task takes its projections in every role with it, and stops at
// the tenant boundary.
func TestDeleteTaskWithdrawsOnlyThisWorkspacesEvents(t *testing.T) {
	db := startDB(t)
	ctx := context.Background()

	fx, dueEvtID := scheduleFixture(t, db)
	neighbour, neighbourDueEvtID := scheduleFixture(t, db)
	if fx.wsID == neighbour.wsID {
		t.Fatal("the two fixtures landed in one workspace; the scoping assertions would prove nothing")
	}

	block := scheduleTimeBlock(t, db, fx, time.Date(2030, 12, 2, 9, 0, 0, 0, time.UTC))
	neighbourBlock := scheduleTimeBlock(t, db, neighbour, time.Date(2030, 12, 2, 9, 0, 0, 0, time.UTC))

	withTx(t, db, func(tx TX) {
		if err := DeleteTask(ctx, tx, fx.wsID, fx.taskID, fx.userID); err != nil {
			t.Fatalf("DeleteTask: %v", err)
		}
	})

	if taskEnabled(t, db, fx.taskID) {
		t.Fatalf("task id=%d is still enabled after DeleteTask", fx.taskID)
	}
	if eventEnabled(t, db, dueEvtID) || eventEnabled(t, db, block) {
		t.Fatalf("DeleteTask left projections enabled: due id=%d block id=%d", dueEvtID, block)
	}
	if !taskEnabled(t, db, neighbour.taskID) {
		t.Fatalf("deleting a task in ws %d disabled ws %d's task id=%d",
			fx.wsID, neighbour.wsID, neighbour.taskID)
	}
	if !eventEnabled(t, db, neighbourDueEvtID) || !eventEnabled(t, db, neighbourBlock) {
		t.Fatalf("deleting a task in ws %d withdrew ws %d's events: due id=%d block id=%d",
			fx.wsID, neighbour.wsID, neighbourDueEvtID, neighbourBlock)
	}
}
