package itemkit

import (
	"context"
	"database/sql"
	"strings"
	"testing"
	"time"
)

// The preconditions a projection write passes are stated once, in
// calendarrules and taskrules, so a browser and an agent writing the same
// row are refused for the same reason and store the same value. These
// tests hold itemkit to them from the write side, because that is where
// both transports meet: a rule reached through one handler and not the
// other is exactly the state the shared packages exist to end.

// eventWindow reads a calendar_events row's stored window.
func eventWindow(ctx context.Context, t *testing.T, db *sql.DB, eventID uint32) (time.Time, time.Time) {
	t.Helper()
	var start, end sql.NullTime
	if err := db.QueryRowContext(ctx,
		`SELECT start_at, end_at FROM calendar_events WHERE id = ?`, eventID).
		Scan(&start, &end); err != nil {
		t.Fatalf("read event window: %v", err)
	}
	if !start.Valid || !end.Valid {
		t.Fatalf("event %d has no window", eventID)
	}
	return start.Time.UTC(), end.Time.UTC()
}

// setStartedOn gives the seeded task a start date for the ordering rule to
// hold a projected due date against.
func setStartedOn(ctx context.Context, t *testing.T, db *sql.DB, taskID uint32, date string) {
	t.Helper()
	if _, err := db.ExecContext(ctx,
		`UPDATE tasks SET started_on = ? WHERE id = ?`, date, taskID); err != nil {
		t.Fatalf("set started_on: %v", err)
	}
}

func TestScheduleTaskPinsAnAllDayProjectionToUTCMidnight(t *testing.T) {
	db := startDB(t)
	ctx := context.Background()
	fx := seed(ctx, t, db)
	defer purge(t, db, fx.wsID)

	// The instant a client hands over for an all-day event is whatever its
	// own clock produced. Stored verbatim, the same day lands on a
	// different square for every reader that takes the row's UTC date.
	start := time.Date(2030, 6, 3, 13, 30, 0, 0, time.UTC)
	var evtID uint32
	withTx(t, db, func(tx TX) {
		var err error
		_, evtID, err = ScheduleTask(ctx, tx, ScheduleTaskArgs{
			WorkspaceID: fx.wsID,
			TaskID:      fx.taskID,
			CalendarID:  fx.calendarID,
			ActorUserID: fx.userID,
			Role:        RoleDue,
			Title:       "Company holiday",
			StartAt:     start,
			EndAt:       start.Add(4 * time.Hour),
			AllDay:      true,
			Timezone:    "UTC",
		})
		if err != nil {
			t.Fatalf("ScheduleTask: %v", err)
		}
	})

	midnight := time.Date(2030, 6, 3, 0, 0, 0, 0, time.UTC)
	gotStart, gotEnd := eventWindow(ctx, t, db, evtID)
	if !gotStart.Equal(midnight) || !gotEnd.Equal(midnight) {
		t.Errorf("stored window = %v..%v, want both pinned to %v", gotStart, gotEnd, midnight)
	}
	if got := dueOnString(ctx, t, db, fx.taskID); got != "2030-06-03" {
		t.Errorf("tasks.due_on = %q, want 2030-06-03 — the task keeps the date the row now stands for", got)
	}
}

func TestScheduleTaskGivesAnAllDayProjectionInATimedZoneTheSameDateAsItsRow(t *testing.T) {
	db := startDB(t)
	ctx := context.Background()
	fx := seed(ctx, t, db)
	defer purge(t, db, fx.wsID)

	// An all-day row's stored instant is midnight UTC on the author's
	// date, so its date is the UTC one. Reading it in the event's own zone
	// would answer the day before west of Greenwich and leave the task and
	// the event it projects on different days.
	// 02:00 UTC is still the previous evening in New York, so a date read
	// in the event's own zone answers a different day from the one the
	// pinned row stands for.
	start := time.Date(2030, 6, 3, 2, 0, 0, 0, time.UTC)
	withTx(t, db, func(tx TX) {
		if _, _, err := ScheduleTask(ctx, tx, ScheduleTaskArgs{
			WorkspaceID: fx.wsID,
			TaskID:      fx.taskID,
			CalendarID:  fx.calendarID,
			ActorUserID: fx.userID,
			Role:        RoleDue,
			Title:       "All-hands day",
			StartAt:     start,
			EndAt:       start.Add(time.Hour),
			AllDay:      true,
			Timezone:    "America/New_York",
		}); err != nil {
			t.Fatalf("ScheduleTask: %v", err)
		}
	})

	if got := dueOnString(ctx, t, db, fx.taskID); got != "2030-06-03" {
		t.Errorf("tasks.due_on = %q, want 2030-06-03 — the same day the pinned row stands for", got)
	}
}

func TestScheduleTaskRefusesAProjectedDueDateBeforeTheTaskStart(t *testing.T) {
	db := startDB(t)
	ctx := context.Background()
	fx := seed(ctx, t, db)
	defer purge(t, db, fx.wsID)

	setStartedOn(ctx, t, db, fx.taskID, "2030-06-10")

	// Nothing in the database refuses a due date earlier than the start
	// date, and the caller moved an event rather than a task, so no
	// field-level check upstream is looking at this pair at all.
	start := time.Date(2030, 6, 3, 10, 0, 0, 0, time.UTC)
	err := inTxErr(ctx, db, func(tx TX) error {
		_, _, scheduleErr := ScheduleTask(ctx, tx, ScheduleTaskArgs{
			WorkspaceID: fx.wsID,
			TaskID:      fx.taskID,
			CalendarID:  fx.calendarID,
			ActorUserID: fx.userID,
			Role:        RoleDue,
			Title:       "Kickoff",
			StartAt:     start,
			EndAt:       start.Add(time.Hour),
			Timezone:    "UTC",
		})
		return scheduleErr
	})
	if err == nil {
		t.Fatal("ScheduleTask stored a due date earlier than the task's start date")
	}
	if !strings.Contains(err.Error(), "itemkit invariant") {
		t.Errorf("error = %v, want an itemkit invariant the transports translate to a 422", err)
	}
	if got := dueOnString(ctx, t, db, fx.taskID); got != "" {
		t.Errorf("tasks.due_on = %q, want unset — the refusal rolled the transaction back", got)
	}
}

func TestScheduleTaskAcceptsAProjectedDueDateOnTheTaskStart(t *testing.T) {
	db := startDB(t)
	ctx := context.Background()
	fx := seed(ctx, t, db)
	defer purge(t, db, fx.wsID)

	// A task started and due the same day is ordinary, so the rule's
	// boundary has to be inclusive.
	setStartedOn(ctx, t, db, fx.taskID, "2030-06-03")

	start := time.Date(2030, 6, 3, 10, 0, 0, 0, time.UTC)
	withTx(t, db, func(tx TX) {
		if _, _, err := ScheduleTask(ctx, tx, ScheduleTaskArgs{
			WorkspaceID: fx.wsID,
			TaskID:      fx.taskID,
			CalendarID:  fx.calendarID,
			ActorUserID: fx.userID,
			Role:        RoleDue,
			Title:       "Kickoff",
			StartAt:     start,
			EndAt:       start.Add(time.Hour),
			Timezone:    "UTC",
		}); err != nil {
			t.Fatalf("ScheduleTask: %v", err)
		}
	})
	if got := dueOnString(ctx, t, db, fx.taskID); got != "2030-06-03" {
		t.Errorf("tasks.due_on = %q, want 2030-06-03", got)
	}
}

func TestRescheduleEventRefusesAMoveThatDragsTheDueDateBeforeTheTaskStart(t *testing.T) {
	db := startDB(t)
	ctx := context.Background()
	fx := seed(ctx, t, db)
	defer purge(t, db, fx.wsID)

	setStartedOn(ctx, t, db, fx.taskID, "2030-06-10")

	start := time.Date(2030, 6, 12, 10, 0, 0, 0, time.UTC)
	var evtID uint32
	withTx(t, db, func(tx TX) {
		var err error
		_, evtID, err = ScheduleTask(ctx, tx, ScheduleTaskArgs{
			WorkspaceID: fx.wsID,
			TaskID:      fx.taskID,
			CalendarID:  fx.calendarID,
			ActorUserID: fx.userID,
			Role:        RoleDue,
			Title:       "Review",
			StartAt:     start,
			EndAt:       start.Add(time.Hour),
			Timezone:    "UTC",
		})
		if err != nil {
			t.Fatalf("ScheduleTask: %v", err)
		}
	})

	// Moving the event is the way a projected task's date is inverted one
	// field at a time: the task's own start never enters the request.
	moved := time.Date(2030, 6, 2, 10, 0, 0, 0, time.UTC)
	err := inTxErr(ctx, db, func(tx TX) error {
		return RescheduleEvent(ctx, tx, RescheduleEventArgs{
			WorkspaceID: fx.wsID,
			EventID:     evtID,
			ActorUserID: fx.userID,
			StartAt:     moved,
			EndAt:       moved.Add(time.Hour),
		})
	})
	if err == nil {
		t.Fatal("RescheduleEvent stored a due date earlier than the task's start date")
	}
	if !strings.Contains(err.Error(), "itemkit invariant") {
		t.Errorf("error = %v, want an itemkit invariant the transports translate to a 422", err)
	}
	if got := dueOnString(ctx, t, db, fx.taskID); got != "2030-06-12" {
		t.Errorf("tasks.due_on = %q, want the pre-move 2030-06-12 — the refusal rolled the transaction back", got)
	}
}

func TestRescheduleEventKeepsAnAllDayRowOnMidnight(t *testing.T) {
	db := startDB(t)
	ctx := context.Background()
	fx := seed(ctx, t, db)
	defer purge(t, db, fx.wsID)

	start := time.Date(2030, 6, 3, 0, 0, 0, 0, time.UTC)
	var evtID uint32
	withTx(t, db, func(tx TX) {
		var err error
		_, evtID, err = ScheduleTask(ctx, tx, ScheduleTaskArgs{
			WorkspaceID: fx.wsID,
			TaskID:      fx.taskID,
			CalendarID:  fx.calendarID,
			ActorUserID: fx.userID,
			Role:        RoleDue,
			Title:       "Company holiday",
			StartAt:     start,
			EndAt:       start,
			AllDay:      true,
			Timezone:    "UTC",
		})
		if err != nil {
			t.Fatalf("ScheduleTask: %v", err)
		}
	})

	// The move carries an instant because that is the shape of the call;
	// the row's own all-day flag is what says the instant stands for a
	// date, so the row stays canonical whichever transport moved it.
	moved := time.Date(2030, 6, 5, 16, 45, 0, 0, time.UTC)
	withTx(t, db, func(tx TX) {
		if err := RescheduleEvent(ctx, tx, RescheduleEventArgs{
			WorkspaceID: fx.wsID,
			EventID:     evtID,
			ActorUserID: fx.userID,
			StartAt:     moved,
			EndAt:       moved.Add(2 * time.Hour),
		}); err != nil {
			t.Fatalf("RescheduleEvent: %v", err)
		}
	})

	midnight := time.Date(2030, 6, 5, 0, 0, 0, 0, time.UTC)
	gotStart, gotEnd := eventWindow(ctx, t, db, evtID)
	if !gotStart.Equal(midnight) || !gotEnd.Equal(midnight) {
		t.Errorf("stored window = %v..%v, want both pinned to %v", gotStart, gotEnd, midnight)
	}
	if got := dueOnString(ctx, t, db, fx.taskID); got != "2030-06-05" {
		t.Errorf("tasks.due_on = %q, want 2030-06-05", got)
	}
}

func TestScheduleTaskRefusesAnInvertedWindow(t *testing.T) {
	db := startDB(t)
	ctx := context.Background()
	fx := seed(ctx, t, db)
	defer purge(t, db, fx.wsID)

	// The pair reaches chk_calendar_events_chronology either way; what the
	// shared rule buys is that the refusal is attributable rather than a
	// driver error the caller cannot act on.
	start := time.Date(2030, 6, 3, 10, 0, 0, 0, time.UTC)
	err := inTxErr(ctx, db, func(tx TX) error {
		_, _, scheduleErr := ScheduleTask(ctx, tx, ScheduleTaskArgs{
			WorkspaceID: fx.wsID,
			TaskID:      fx.taskID,
			CalendarID:  fx.calendarID,
			ActorUserID: fx.userID,
			Role:        RoleDue,
			Title:       "Backwards",
			StartAt:     start,
			EndAt:       start.Add(-time.Hour),
			Timezone:    "UTC",
		})
		return scheduleErr
	})
	if err == nil {
		t.Fatal("ScheduleTask accepted an end before its start")
	}
	if !strings.Contains(err.Error(), "chronology") {
		t.Errorf("error = %v, want the chronology invariant", err)
	}
}
