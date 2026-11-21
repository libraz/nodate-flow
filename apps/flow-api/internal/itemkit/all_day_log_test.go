package itemkit

import (
	"context"
	"database/sql"
	"strings"
	"testing"
	"time"
)

// scheduleAllDayDue projects fx's task onto an all-day due event on 3 June
// 2030 and returns the event's internal id.
func scheduleAllDayDue(ctx context.Context, t *testing.T, fx fixtures, tx TX) uint32 {
	t.Helper()
	day := time.Date(2030, 6, 3, 0, 0, 0, 0, time.UTC)
	_, evtID, err := ScheduleTask(ctx, tx, ScheduleTaskArgs{
		WorkspaceID: fx.wsID,
		TaskID:      fx.taskID,
		CalendarID:  fx.calendarID,
		ActorUserID: fx.userID,
		Role:        RoleDue,
		Title:       "Company holiday",
		StartAt:     day,
		EndAt:       day,
		AllDay:      true,
		Timezone:    "UTC",
	})
	if err != nil {
		t.Fatalf("ScheduleTask: %v", err)
	}
	return evtID
}

// A one-day all-day event spans exactly 24 hours, whatever end the caller
// handed over for the projection.
func TestScheduleTaskStoresAOneDayAllDayEventAsStartPlusOneDay(t *testing.T) {
	db := startDB(t)
	ctx := context.Background()
	fx := seed(ctx, t, db)
	defer purge(t, db, fx.wsID)

	var evtID uint32
	withTx(t, db, func(tx TX) { evtID = scheduleAllDayDue(ctx, t, fx, tx) })

	start, end := eventWindow(ctx, t, db, evtID)
	if want := time.Date(2030, 6, 3, 0, 0, 0, 0, time.UTC); !start.Equal(want) || end.Sub(start) != 24*time.Hour {
		t.Errorf("stored window = %v..%v, want %v + 24h", start, end, want)
	}
}

// Moving the task's due date carries the all-day projection along as whole
// days at UTC midnight, in the event's zone or not.
func TestRescheduleTaskKeepsAnAllDayProjectionOnMidnight(t *testing.T) {
	db := startDB(t)
	ctx := context.Background()
	fx := seed(ctx, t, db)
	defer purge(t, db, fx.wsID)

	var evtID uint32
	withTx(t, db, func(tx TX) {
		day := time.Date(2030, 6, 3, 0, 0, 0, 0, time.UTC)
		_, id, err := ScheduleTask(ctx, tx, ScheduleTaskArgs{
			WorkspaceID: fx.wsID, TaskID: fx.taskID, CalendarID: fx.calendarID,
			ActorUserID: fx.userID, Role: RoleDue, Title: "Holiday",
			StartAt: day, EndAt: day.AddDate(0, 0, 1), AllDay: true,
			Timezone: "America/Los_Angeles",
		})
		if err != nil {
			t.Fatalf("ScheduleTask: %v", err)
		}
		evtID = id
	})

	withTx(t, db, func(tx TX) {
		if err := RescheduleTask(ctx, tx, RescheduleTaskArgs{
			WorkspaceID: fx.wsID, TaskID: fx.taskID, ActorUserID: fx.userID,
			SetDueOn: true, DueOn: time.Date(2030, 6, 8, 0, 0, 0, 0, time.UTC),
		}); err != nil {
			t.Fatalf("RescheduleTask: %v", err)
		}
	})

	start, end := eventWindow(ctx, t, db, evtID)
	if want := time.Date(2030, 6, 8, 0, 0, 0, 0, time.UTC); !start.Equal(want) || end.Sub(start) != 24*time.Hour {
		t.Errorf("stored window = %v..%v, want %v + 24h", start, end, want)
	}
}

// An all-day window whose ends fall on one day names no day at all; the
// move is refused as an invariant rather than reaching the CHECK.
func TestRescheduleEventRefusesAnAllDayWindowThatCollapsesToOneDay(t *testing.T) {
	db := startDB(t)
	ctx := context.Background()
	fx := seed(ctx, t, db)
	defer purge(t, db, fx.wsID)

	var evtID uint32
	withTx(t, db, func(tx TX) { evtID = scheduleAllDayDue(ctx, t, fx, tx) })

	moved := time.Date(2030, 6, 5, 9, 0, 0, 0, time.UTC)
	err := inTxErr(ctx, db, func(tx TX) error {
		return RescheduleEvent(ctx, tx, RescheduleEventArgs{
			WorkspaceID: fx.wsID, EventID: evtID, ActorUserID: fx.userID,
			StartAt: moved, EndAt: moved.Add(time.Hour),
		})
	})
	if err == nil || !strings.Contains(err.Error(), "itemkit invariant") {
		t.Fatalf("error = %v, want an itemkit invariant", err)
	}
}

// calendarIDsOfLog reads the calendar_id of every log row of kind written
// for the event, in order.
func calendarIDsOfLog(ctx context.Context, t *testing.T, db *sql.DB, fx fixtures, kind string, evtID uint32) []*uint32 {
	t.Helper()
	rows, err := db.QueryContext(ctx,
		`SELECT e.calendar_id FROM events e
		   JOIN calendar_events ce ON ce.public_id = e.subject_public_id
		  WHERE e.workspace_id = ? AND e.type = ? AND ce.id = ?
		  ORDER BY e.id`, fx.wsID, kind, evtID)
	if err != nil {
		t.Fatalf("read log: %v", err)
	}
	defer func() { _ = rows.Close() }()
	var out []*uint32
	for rows.Next() {
		var id *uint32
		if err := rows.Scan(&id); err != nil {
			t.Fatalf("scan: %v", err)
		}
		out = append(out, id)
	}
	return out
}

// Every calendar.event.* row itemkit writes about an existing event names
// the event's calendar.
func TestItemkitCalendarLogRowsCarryTheCalendarID(t *testing.T) {
	db := startDB(t)
	ctx := context.Background()
	fx := seed(ctx, t, db)
	defer purge(t, db, fx.wsID)

	var evtID uint32
	withTx(t, db, func(tx TX) { evtID = scheduleAllDayDue(ctx, t, fx, tx) })
	withTx(t, db, func(tx TX) {
		if err := RescheduleTask(ctx, tx, RescheduleTaskArgs{
			WorkspaceID: fx.wsID, TaskID: fx.taskID, ActorUserID: fx.userID,
			SetDueOn: true, DueOn: time.Date(2030, 6, 8, 0, 0, 0, 0, time.UTC),
		}); err != nil {
			t.Fatalf("RescheduleTask: %v", err)
		}
	})

	for _, kind := range []string{"calendar.event.created", "calendar.event.updated"} {
		ids := calendarIDsOfLog(ctx, t, db, fx, kind, evtID)
		if len(ids) == 0 {
			t.Fatalf("no %s row was written for the event", kind)
		}
		for _, id := range ids {
			if id == nil || *id != fx.calendarID {
				t.Errorf("%s calendar_id = %v, want %d", kind, id, fx.calendarID)
			}
		}
	}
}
