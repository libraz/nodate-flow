package itemkit

import (
	"context"
	"database/sql"
	"strings"
	"testing"
	"time"

	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/dbretry"
	"github.com/libraz/nodate-flow/packages/go-shared/dbtype"
)

// dueOnString reads tasks.due_on formatted by MySQL, so the assertion
// sees the stored DATE rather than a Go-side reinterpretation of it.
func dueOnString(ctx context.Context, t *testing.T, db *sql.DB, taskID uint32) string {
	t.Helper()
	var due sql.NullString
	if err := db.QueryRowContext(ctx,
		`SELECT DATE_FORMAT(due_on, '%Y-%m-%d') FROM tasks WHERE id = ?`, taskID,
	).Scan(&due); err != nil {
		t.Fatalf("read due_on: %v", err)
	}
	if !due.Valid {
		return ""
	}
	return due.String
}

// TestScheduleTaskDatesTheDeadlineInTheEventTimezone covers the case
// that made the two disagree: a morning meeting in a zone ahead of UTC.
//
// 2030-06-03 08:00 Asia/Tokyo is 2030-06-02 23:00 UTC, so reading the
// stored instant without the zone dates the deadline to the 2nd — a day
// before the meeting that set it.
func TestScheduleTaskDatesTheDeadlineInTheEventTimezone(t *testing.T) {
	db := startDB(t)
	ctx := context.Background()
	fx := seed(ctx, t, db)
	defer purge(t, db, fx.wsID)

	tokyo, err := time.LoadLocation("Asia/Tokyo")
	if err != nil {
		t.Fatalf("load location: %v", err)
	}
	start := time.Date(2030, 6, 3, 8, 0, 0, 0, tokyo)
	if start.UTC().Day() != 2 {
		t.Fatalf("test premise broken: %v is not on the previous UTC day", start.UTC())
	}

	withTx(t, db, func(tx TX) {
		if _, _, err := ScheduleTask(ctx, tx, ScheduleTaskArgs{
			WorkspaceID: fx.wsID, TaskID: fx.taskID, CalendarID: fx.calendarID,
			ActorUserID: fx.userID, Role: RoleDue, Title: "Morning standup",
			StartAt: start, EndAt: start.Add(time.Hour), Timezone: "Asia/Tokyo",
		}); err != nil {
			t.Fatalf("ScheduleTask: %v", err)
		}
	})

	if got := dueOnString(ctx, t, db, fx.taskID); got != "2030-06-03" {
		t.Errorf("tasks.due_on = %q, want 2030-06-03 (the Tokyo date of the event)", got)
	}
}

// TestScheduleTaskDatesTheDeadlineBehindUTCToo covers the mirror case.
// 2030-06-03 20:00 in Los Angeles is 2030-06-04 03:00 UTC, so the UTC
// reading lands a day *later* — a fix that just subtracted a day would
// pass the Tokyo case and fail this one.
func TestScheduleTaskDatesTheDeadlineBehindUTCToo(t *testing.T) {
	db := startDB(t)
	ctx := context.Background()
	fx := seed(ctx, t, db)
	defer purge(t, db, fx.wsID)

	la, err := time.LoadLocation("America/Los_Angeles")
	if err != nil {
		t.Fatalf("load location: %v", err)
	}
	start := time.Date(2030, 6, 3, 20, 0, 0, 0, la)
	if start.UTC().Day() != 4 {
		t.Fatalf("test premise broken: %v is not on the next UTC day", start.UTC())
	}

	withTx(t, db, func(tx TX) {
		if _, _, err := ScheduleTask(ctx, tx, ScheduleTaskArgs{
			WorkspaceID: fx.wsID, TaskID: fx.taskID, CalendarID: fx.calendarID,
			ActorUserID: fx.userID, Role: RoleDue, Title: "Evening review",
			StartAt: start, EndAt: start.Add(time.Hour), Timezone: "America/Los_Angeles",
		}); err != nil {
			t.Fatalf("ScheduleTask: %v", err)
		}
	})

	if got := dueOnString(ctx, t, db, fx.taskID); got != "2030-06-03" {
		t.Errorf("tasks.due_on = %q, want 2030-06-03 (the Los Angeles date of the event)", got)
	}
}

// TestRescheduleEventKeepsTheDeadlineOnItsLocalDay moves a Tokyo event
// across a UTC midnight without leaving its own day. The deadline must
// not move.
func TestRescheduleEventKeepsTheDeadlineOnItsLocalDay(t *testing.T) {
	db := startDB(t)
	ctx := context.Background()
	fx := seed(ctx, t, db)
	defer purge(t, db, fx.wsID)

	tokyo, err := time.LoadLocation("Asia/Tokyo")
	if err != nil {
		t.Fatalf("load location: %v", err)
	}
	start := time.Date(2030, 6, 3, 8, 0, 0, 0, tokyo)
	var eventID uint32
	withTx(t, db, func(tx TX) {
		_, id, err := ScheduleTask(ctx, tx, ScheduleTaskArgs{
			WorkspaceID: fx.wsID, TaskID: fx.taskID, CalendarID: fx.calendarID,
			ActorUserID: fx.userID, Role: RoleDue, Title: "Standup",
			StartAt: start, EndAt: start.Add(time.Hour), Timezone: "Asia/Tokyo",
		})
		if err != nil {
			t.Fatalf("ScheduleTask: %v", err)
		}
		eventID = id
	})

	// Assert the starting state too. Without this the test passes for the
	// wrong reason under a UTC derivation: the initial write lands on the
	// 2nd, the move to 20:00 JST crosses into the 3rd in UTC, and the
	// final assertion sees the right date arrived at by two errors.
	if got := dueOnString(ctx, t, db, fx.taskID); got != "2030-06-03" {
		t.Fatalf("tasks.due_on = %q before the move, want 2030-06-03", got)
	}

	// 08:00 → 20:00 the same Tokyo day, which crosses a UTC midnight.
	moved := time.Date(2030, 6, 3, 20, 0, 0, 0, tokyo)
	if moved.UTC().Day() == start.UTC().Day() {
		t.Fatal("test premise broken: the move does not cross a UTC midnight")
	}
	withTx(t, db, func(tx TX) {
		if err := RescheduleEvent(ctx, tx, RescheduleEventArgs{
			WorkspaceID: fx.wsID, EventID: eventID, ActorUserID: fx.userID,
			StartAt: moved, EndAt: moved.Add(time.Hour),
		}); err != nil {
			t.Fatalf("RescheduleEvent: %v", err)
		}
	})

	if got := dueOnString(ctx, t, db, fx.taskID); got != "2030-06-03" {
		t.Errorf("tasks.due_on = %q, want 2030-06-03 — the event never left its Tokyo day", got)
	}
}

// TestRescheduleTaskMovesTheEventToTheRequestedLocalDay covers the
// other direction: the user edits the task's deadline, and the linked
// event has to land on that day in its own zone, keeping its local
// time-of-day.
//
// Composing the new instant from the event's stored UTC clock kept the
// wrong hour — a Tokyo 08:00 meeting reads 23:00 in UTC, so the event
// landed at 23:00 on the requested day, which is 08:00 the day after in
// Tokyo. The pair then disagrees forever, and the drift reconciler
// settles that disagreement in the event's favour: the deadline the user
// typed gets quietly replaced by one a day later.
func TestRescheduleTaskMovesTheEventToTheRequestedLocalDay(t *testing.T) {
	db := startDB(t)
	ctx := context.Background()
	fx := seed(ctx, t, db)
	defer purge(t, db, fx.wsID)

	tokyo, err := time.LoadLocation("Asia/Tokyo")
	if err != nil {
		t.Fatalf("load location: %v", err)
	}
	start := time.Date(2030, 6, 3, 8, 0, 0, 0, tokyo)
	withTx(t, db, func(tx TX) {
		if _, _, err := ScheduleTask(ctx, tx, ScheduleTaskArgs{
			WorkspaceID: fx.wsID, TaskID: fx.taskID, CalendarID: fx.calendarID,
			ActorUserID: fx.userID, Role: RoleDue, Title: "Standup",
			StartAt: start, EndAt: start.Add(time.Hour), Timezone: "Asia/Tokyo",
		}); err != nil {
			t.Fatalf("ScheduleTask: %v", err)
		}
	})

	newDue := time.Date(2030, 6, 10, 0, 0, 0, 0, time.UTC)
	withTx(t, db, func(tx TX) {
		if err := RescheduleTask(ctx, tx, RescheduleTaskArgs{
			WorkspaceID: fx.wsID, TaskID: fx.taskID, ActorUserID: fx.userID,
			SetDueOn: true, DueOn: newDue,
		}); err != nil {
			t.Fatalf("RescheduleTask: %v", err)
		}
	})

	var evtStart sql.NullTime
	if err := db.QueryRowContext(ctx,
		`SELECT start_at FROM calendar_events WHERE task_id = ? AND task_role = 'due' AND enabled = TRUE`,
		fx.taskID,
	).Scan(&evtStart); err != nil {
		t.Fatalf("read event: %v", err)
	}
	if !evtStart.Valid {
		t.Fatal("linked event lost its start")
	}
	local := evtStart.Time.In(tokyo)
	if got := local.Format("2006-01-02 15:04"); got != "2030-06-10 08:00" {
		t.Errorf("event start in Tokyo = %s, want 2030-06-10 08:00", got)
	}

	// The invariant the reconciler enforces: the task's deadline and the
	// event's local date must name the same day. If they do not, the
	// background loop rewrites one of them and the user's edit is lost.
	if got := dueOnString(ctx, t, db, fx.taskID); got != local.Format("2006-01-02") {
		t.Errorf("tasks.due_on = %q but the event falls on %s in its own zone",
			got, local.Format("2006-01-02"))
	}
}

// TestScheduleTaskRefusesAnUnknownTimezone asserts the write fails
// rather than falling back to UTC. A fallback here writes a
// plausible-looking date that is silently wrong, which is the whole
// failure being fixed.
func TestScheduleTaskRefusesAnUnknownTimezone(t *testing.T) {
	db := startDB(t)
	ctx := context.Background()
	fx := seed(ctx, t, db)
	defer purge(t, db, fx.wsID)

	start := time.Date(2030, 6, 3, 8, 0, 0, 0, time.UTC)
	// The closure returns the invariant error, so InTx rolls the
	// attempt back — which is the second half of what this test checks.
	err := dbretry.InTx(ctx, db, "itemkit.test", nil, func(ctx context.Context, tx *dbretry.Tx) error {
		_, _, serr := ScheduleTask(ctx, tx, ScheduleTaskArgs{
			WorkspaceID: fx.wsID, TaskID: fx.taskID, CalendarID: fx.calendarID,
			ActorUserID: fx.userID, Role: RoleDue, Title: "Broken zone",
			StartAt: start, EndAt: start.Add(time.Hour), Timezone: "Mars/Olympus",
		})
		return serr
	})
	if err == nil {
		t.Fatal("ScheduleTask accepted an unknown timezone")
	}
	if !strings.Contains(err.Error(), "event_timezone_valid") {
		t.Errorf("error = %v, want the event_timezone_valid invariant", err)
	}

	if got := dueOnString(ctx, t, db, fx.taskID); got != "" {
		t.Errorf("tasks.due_on = %q, want it left unset after a rejected write", got)
	}
}

// TestApplyShiftCountsDaysInTheEventTimezone moves an umbrella event
// within its own Tokyo day. Linked tasks have DATE precision, so they
// must not move — counting the delta in UTC drags them a day forward,
// because the move crosses a UTC midnight.
func TestApplyShiftCountsDaysInTheEventTimezone(t *testing.T) {
	db := startDB(t)
	ctx := context.Background()
	f := seed(ctx, t, db)
	defer purge(t, db, f.wsID)

	tokyo, err := time.LoadLocation("Asia/Tokyo")
	if err != nil {
		t.Fatalf("load location: %v", err)
	}
	umbrellaStart := time.Date(2030, 6, 3, 8, 0, 0, 0, tokyo)
	umbrella := seedTokyoEvent(ctx, t, db, f, umbrellaStart)

	dueDate := time.Date(2030, 5, 28, 0, 0, 0, 0, time.UTC)
	taskID, _ := seedExtraTask(ctx, t, db, f, "Same Tokyo day", dueDate)
	linkContributesTo(ctx, t, db, f, taskID, umbrella)

	newStart := time.Date(2030, 6, 3, 20, 0, 0, 0, tokyo)
	if newStart.UTC().Day() == umbrellaStart.UTC().Day() {
		t.Fatal("test premise broken: the move does not cross a UTC midnight")
	}

	withTx(t, db, func(tx TX) {
		if err := ApplyShiftEventAndChildren(ctx, tx, ApplyShiftEventAndChildrenArgs{
			WorkspaceID:      f.wsID,
			EventID:          umbrella,
			NewStartAt:       newStart,
			ConfirmedTaskIDs: []uint32{taskID},
			ActorUserID:      f.userID,
		}); err != nil {
			t.Fatalf("apply: %v", err)
		}
	})

	if got := dueOnString(ctx, t, db, taskID); got != "2030-05-28" {
		t.Errorf("tasks.due_on = %q, want 2030-05-28 — the umbrella stayed on its Tokyo day", got)
	}
}

// seedTokyoEvent is seedEvent with a real timezone on the row. The
// shared helper hard-codes 'UTC', which cannot express the case under
// test here.
func seedTokyoEvent(ctx context.Context, t *testing.T, db *sql.DB, f fixtures, startAt time.Time) uint32 {
	t.Helper()
	pub := dbtype.New()
	res, err := db.ExecContext(ctx,
		`INSERT INTO calendar_events
		   (public_id, workspace_id, calendar_id, kind, visibility, show_as,
		    title, all_day, start_at, end_at, timezone,
		    owner_user_id, created_by_user_id)
		 VALUES (?, ?, ?, 'event', 'default', 'busy',
		         'Tokyo umbrella', FALSE, ?, ?, 'Asia/Tokyo',
		         ?, ?)`,
		pub, f.wsID, f.calendarID, startAt, startAt.Add(time.Hour), f.userID, f.userID,
	)
	if err != nil {
		t.Fatalf("seed tokyo event: %v", err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		t.Fatalf("seed tokyo event id: %v", err)
	}
	return uint32(id) //#nosec G115 -- LastInsertId in test seed, fits uint32
}
