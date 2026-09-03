package itemkit

import (
	"context"
	"database/sql"
	"testing"
	"time"

	"github.com/libraz/nodate-flow/packages/go-shared/dbtype"
)

// The projection key used to be UNIQUE (task_id, task_role_key) over a
// de-NULLed copy of task_role, which answered two different questions
// with one index and got both wrong:
//
//   - `scheduled` is documented as a role a task may hold several times,
//     and the key forbade the second one.
//   - a soft-deleted projection keeps its task_id and task_role, so the
//     tombstone stayed in the key and collided with the next projection
//     of the same task.
//
// Both tests below run more than one cycle on purpose. A single
// schedule, or a single schedule-then-unschedule, passes against the old
// key; it is the second one that fails, which is why the shape survived
// as long as it did.

// TestScheduleTaskAllowsSecondTimeBlock covers the first half: a task
// may hold more than one live scheduled projection at a time.
func TestScheduleTaskAllowsSecondTimeBlock(t *testing.T) {
	db := startDB(t)
	fx := seed(context.Background(), t, db)
	defer purge(t, db, fx.wsID)

	first := time.Date(2030, 7, 1, 10, 0, 0, 0, time.UTC)
	second := time.Date(2030, 7, 2, 14, 0, 0, 0, time.UTC)

	var pubs []dbtype.PublicID
	for _, start := range []time.Time{first, second} {
		withTx(t, db, func(tx TX) {
			pub, _, err := ScheduleTask(context.Background(), tx, ScheduleTaskArgs{
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
				t.Fatalf("ScheduleTask(%s): %v", start.Format(time.DateOnly), err)
			}
			pubs = append(pubs, pub)
		})
	}

	if pubs[0] == pubs[1] {
		t.Fatal("the second time block reused the first block's event")
	}

	var live int
	if err := db.QueryRow(
		`SELECT COUNT(*) FROM calendar_events
		  WHERE task_id = ? AND task_role = 'scheduled' AND enabled = TRUE`,
		fx.taskID,
	).Scan(&live); err != nil {
		t.Fatalf("count scheduled events: %v", err)
	}
	if live != 2 {
		t.Fatalf("live scheduled projections = %d, want 2", live)
	}
}

// TestScheduleUnscheduleRescheduleCycles covers the second half: the due
// projection can be created, removed and created again, repeatedly. Two
// full cycles are run because one cycle leaves one tombstone, which the
// old key could still represent — the failure appeared on the second
// unschedule.
func TestScheduleUnscheduleRescheduleCycles(t *testing.T) {
	db := startDB(t)
	fx := seed(context.Background(), t, db)
	defer purge(t, db, fx.wsID)

	base := time.Date(2030, 8, 1, 9, 0, 0, 0, time.UTC)

	for cycle := 0; cycle < 3; cycle++ {
		start := base.AddDate(0, 0, cycle)
		var evtID uint32
		withTx(t, db, func(tx TX) {
			var err error
			_, evtID, err = ScheduleTask(context.Background(), tx, ScheduleTaskArgs{
				WorkspaceID: fx.wsID,
				TaskID:      fx.taskID,
				CalendarID:  fx.calendarID,
				ActorUserID: fx.userID,
				Role:        RoleDue,
				Title:       "Cycled task",
				StartAt:     start,
				EndAt:       start.Add(time.Hour),
				Timezone:    "UTC",
			})
			if err != nil {
				t.Fatalf("cycle %d: ScheduleTask: %v", cycle, err)
			}
		})

		var live int
		if err := db.QueryRow(
			`SELECT COUNT(*) FROM calendar_events
			  WHERE task_id = ? AND task_role = 'due' AND enabled = TRUE`,
			fx.taskID,
		).Scan(&live); err != nil {
			t.Fatalf("cycle %d: count live due: %v", cycle, err)
		}
		if live != 1 {
			t.Fatalf("cycle %d: live due projections = %d, want exactly 1", cycle, live)
		}

		withTx(t, db, func(tx TX) {
			if err := DeleteEvent(context.Background(), tx, fx.wsID, evtID, fx.userID); err != nil {
				t.Fatalf("cycle %d: DeleteEvent: %v", cycle, err)
			}
		})

		// Unscheduling clears the task's own date, so each cycle starts
		// from the same state the first one did.
		var dueOn sql.NullTime
		if err := db.QueryRow(`SELECT due_on FROM tasks WHERE id = ?`, fx.taskID).Scan(&dueOn); err != nil {
			t.Fatalf("cycle %d: read task: %v", cycle, err)
		}
		if dueOn.Valid {
			t.Fatalf("cycle %d: tasks.due_on = %v after unschedule, want NULL", cycle, dueOn.Time)
		}
	}

	// Every cycle left its tombstone. That the rows accumulate rather
	// than collide is the whole point: the projection history survives
	// and the task stays schedulable.
	var tombstones int
	if err := db.QueryRow(
		`SELECT COUNT(*) FROM calendar_events
		  WHERE task_id = ? AND task_role = 'due' AND enabled = FALSE`,
		fx.taskID,
	).Scan(&tombstones); err != nil {
		t.Fatalf("count tombstones: %v", err)
	}
	if tombstones != 3 {
		t.Fatalf("due tombstones = %d, want 3", tombstones)
	}
}

// TestScheduleTaskRefusesSecondLiveDue pins the half of the old key that
// was correct. A due projection mirrors a single task field, so a second
// live one would mean the task has two due dates; ScheduleTask reaches
// the existing row and moves it instead of inserting beside it.
func TestScheduleTaskRefusesSecondLiveDue(t *testing.T) {
	db := startDB(t)
	fx := seed(context.Background(), t, db)
	defer purge(t, db, fx.wsID)

	first := time.Date(2030, 9, 1, 9, 0, 0, 0, time.UTC)
	second := time.Date(2030, 9, 8, 9, 0, 0, 0, time.UTC)

	var firstPub, secondPub dbtype.PublicID
	withTx(t, db, func(tx TX) {
		var err error
		firstPub, _, err = ScheduleTask(context.Background(), tx, ScheduleTaskArgs{
			WorkspaceID: fx.wsID, TaskID: fx.taskID, CalendarID: fx.calendarID,
			ActorUserID: fx.userID, Role: RoleDue, Title: "Due once",
			StartAt: first, EndAt: first.Add(time.Hour), Timezone: "UTC",
		})
		if err != nil {
			t.Fatalf("first ScheduleTask: %v", err)
		}
	})
	withTx(t, db, func(tx TX) {
		var err error
		secondPub, _, err = ScheduleTask(context.Background(), tx, ScheduleTaskArgs{
			WorkspaceID: fx.wsID, TaskID: fx.taskID, CalendarID: fx.calendarID,
			ActorUserID: fx.userID, Role: RoleDue, Title: "Due once",
			StartAt: second, EndAt: second.Add(time.Hour), Timezone: "UTC",
		})
		if err != nil {
			t.Fatalf("second ScheduleTask: %v", err)
		}
	})

	if firstPub != secondPub {
		t.Fatal("scheduling a due date twice created a second event instead of moving the first")
	}

	var live int
	if err := db.QueryRow(
		`SELECT COUNT(*) FROM calendar_events
		  WHERE task_id = ? AND task_role = 'due' AND enabled = TRUE`,
		fx.taskID,
	).Scan(&live); err != nil {
		t.Fatalf("count live due: %v", err)
	}
	if live != 1 {
		t.Fatalf("live due projections = %d, want 1", live)
	}
}
