package itemkit

import (
	"context"
	"database/sql"
	"testing"
	"time"

	"github.com/libraz/nodate-flow/packages/go-shared/dbtype"
)

// seedExtraTask inserts an additional tasks row in the same project
// as the primary fixture task, with an optional due_on date.
// Returns the internal id + public id.
func seedExtraTask(ctx context.Context, t *testing.T, db *sql.DB, f fixtures, title string, dueOn time.Time) (uint32, dbtype.PublicID) {
	t.Helper()
	pub := dbtype.New()
	var dueOnArg any
	if !dueOn.IsZero() {
		dueOnArg = dueOn.Format("2006-01-02")
	}
	// task_number must be unique per project — use a time-derived value.
	num := int32(time.Now().UnixNano() % 1_000_000)
	res, err := db.ExecContext(ctx,
		`INSERT INTO tasks (public_id, workspace_id, project_id, task_number, title, visibility, due_on)
		 VALUES (?, ?, ?, ?, ?, 'public', ?)`,
		pub, f.wsID, f.projectID, num, title, dueOnArg,
	)
	if err != nil {
		t.Fatalf("seed extra task: %v", err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		t.Fatalf("seed extra task id: %v", err)
	}
	return uint32(id), pub //#nosec G115 -- LastInsertId in test seed, fits uint32
}

// linkContributesTo inserts a contributes_to link between a task and
// an event in one tx so tests stay terse.
func linkContributesTo(ctx context.Context, t *testing.T, db *sql.DB, f fixtures, taskID, eventID uint32) {
	t.Helper()
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	if _, _, err := LinkTaskToEvent(ctx, tx, LinkTaskToEventArgs{
		WorkspaceID: f.wsID,
		TaskID:      taskID,
		EventID:     eventID,
		Relation:    RelationContributesTo,
		ActorUserID: f.userID,
	}); err != nil {
		_ = tx.Rollback()
		t.Fatalf("link: %v", err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatalf("commit: %v", err)
	}
}

func TestProposeShiftEventAndChildren_NoLinks(t *testing.T) {
	db := startDB(t)
	ctx := context.Background()
	f := seed(ctx, t, db)
	defer purge(t, db, f.wsID)
	evtID, _ := seedEvent(ctx, t, db, f, time.Date(2026, 6, 1, 10, 0, 0, 0, time.UTC))

	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	defer func() { _ = tx.Rollback() }()
	newStart := time.Date(2026, 6, 8, 10, 0, 0, 0, time.UTC)
	p, err := ProposeShiftEventAndChildren(ctx, tx, f.wsID, evtID, newStart)
	if err != nil {
		t.Fatalf("propose: %v", err)
	}
	if len(p.SafeTasks) != 0 || len(p.ConflictTasks) != 0 {
		t.Errorf("expected empty proposal, got safe=%d conflict=%d", len(p.SafeTasks), len(p.ConflictTasks))
	}
	if p.Delta != 7*24*time.Hour {
		t.Errorf("delta = %v, want 7d", p.Delta)
	}
}

func TestProposeShiftEventAndChildren_PartitionsSafeAndConflict(t *testing.T) {
	db := startDB(t)
	ctx := context.Background()
	f := seed(ctx, t, db)
	defer purge(t, db, f.wsID)

	umbrella, _ := seedEvent(ctx, t, db, f, time.Date(2026, 6, 1, 10, 0, 0, 0, time.UTC))
	other, _ := seedEvent(ctx, t, db, f, time.Date(2026, 7, 1, 10, 0, 0, 0, time.UTC))

	// The primary fixture task is safe (linked only to umbrella).
	linkContributesTo(ctx, t, db, f, f.taskID, umbrella)

	// A second task that contributes to BOTH events = conflict.
	conflictTaskID, conflictTaskPub := seedExtraTask(ctx, t, db, f, "Conflict task", time.Time{})
	linkContributesTo(ctx, t, db, f, conflictTaskID, umbrella)
	linkContributesTo(ctx, t, db, f, conflictTaskID, other)

	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	defer func() { _ = tx.Rollback() }()
	newStart := time.Date(2026, 6, 8, 10, 0, 0, 0, time.UTC)
	p, err := ProposeShiftEventAndChildren(ctx, tx, f.wsID, umbrella, newStart)
	if err != nil {
		t.Fatalf("propose: %v", err)
	}
	if len(p.SafeTasks) != 1 {
		t.Fatalf("safe count = %d, want 1", len(p.SafeTasks))
	}
	if p.SafeTasks[0].TaskID != f.taskID {
		t.Errorf("safe task id = %d, want %d", p.SafeTasks[0].TaskID, f.taskID)
	}
	if len(p.ConflictTasks) != 1 {
		t.Fatalf("conflict count = %d, want 1", len(p.ConflictTasks))
	}
	c := p.ConflictTasks[0]
	if c.TaskID != conflictTaskID {
		t.Errorf("conflict task id = %d, want %d", c.TaskID, conflictTaskID)
	}
	if c.TaskPublicID != conflictTaskPub {
		t.Errorf("conflict task public id mismatch")
	}
	if len(c.OtherLinks) != 1 {
		t.Fatalf("conflict task OtherLinks = %d, want 1", len(c.OtherLinks))
	}
	if c.OtherLinks[0].EventID != other {
		t.Errorf("other link event id = %d, want %d", c.OtherLinks[0].EventID, other)
	}
}

func TestProposeShiftEventAndChildren_RejectsUndated(t *testing.T) {
	db := startDB(t)
	ctx := context.Background()
	f := seed(ctx, t, db)
	defer purge(t, db, f.wsID)
	// Create an undated event directly.
	pub := dbtype.New()
	res, err := db.ExecContext(ctx,
		`INSERT INTO calendar_events
		   (public_id, workspace_id, calendar_id, kind, visibility, show_as,
		    title, all_day, start_at, end_at, timezone,
		    owner_user_id, created_by_user_id)
		 VALUES (?, ?, ?, 'event', 'default', 'busy',
		         'Undated', FALSE, NULL, NULL, 'UTC',
		         ?, ?)`,
		pub, f.wsID, f.calendarID, f.userID, f.userID,
	)
	if err != nil {
		t.Fatalf("seed undated event: %v", err)
	}
	id64, _ := res.LastInsertId()
	evtID := uint32(id64) //#nosec G115 -- LastInsertId in test seed, fits uint32

	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	defer func() { _ = tx.Rollback() }()
	_, err = ProposeShiftEventAndChildren(ctx, tx, f.wsID, evtID,
		time.Date(2026, 6, 8, 10, 0, 0, 0, time.UTC))
	if err == nil {
		t.Fatal("expected invariant error for undated umbrella, got nil")
	}
}

func TestApplyShiftEventAndChildren_ShiftsUmbrellaAndSafeTasks(t *testing.T) {
	db := startDB(t)
	ctx := context.Background()
	f := seed(ctx, t, db)
	defer purge(t, db, f.wsID)

	umbrellaStart := time.Date(2026, 6, 1, 10, 0, 0, 0, time.UTC)
	umbrella, _ := seedEvent(ctx, t, db, f, umbrellaStart)

	dueDate := time.Date(2026, 5, 28, 0, 0, 0, 0, time.UTC)
	safeTaskID, _ := seedExtraTask(ctx, t, db, f, "Safe", dueDate)
	linkContributesTo(ctx, t, db, f, safeTaskID, umbrella)

	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	newStart := umbrellaStart.AddDate(0, 0, 7)
	if err := ApplyShiftEventAndChildren(ctx, tx, ApplyShiftEventAndChildrenArgs{
		WorkspaceID:      f.wsID,
		EventID:          umbrella,
		NewStartAt:       newStart,
		ConfirmedTaskIDs: []uint32{safeTaskID},
		ActorUserID:      f.userID,
	}); err != nil {
		_ = tx.Rollback()
		t.Fatalf("apply: %v", err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatalf("commit: %v", err)
	}

	// Umbrella event shifted.
	var gotStart sql.NullTime
	if err := db.QueryRowContext(ctx,
		`SELECT start_at FROM calendar_events WHERE id = ?`, umbrella,
	).Scan(&gotStart); err != nil {
		t.Fatalf("read umbrella: %v", err)
	}
	if !gotStart.Valid || !gotStart.Time.Equal(newStart) {
		t.Errorf("umbrella start = %v, want %v", gotStart, newStart)
	}

	// Safe task due_on shifted by 7 days.
	var gotDue sql.NullTime
	if err := db.QueryRowContext(ctx,
		`SELECT due_on FROM tasks WHERE id = ?`, safeTaskID,
	).Scan(&gotDue); err != nil {
		t.Fatalf("read task: %v", err)
	}
	wantDue := dueDate.AddDate(0, 0, 7)
	if !gotDue.Valid || !gotDue.Time.Equal(wantDue) {
		t.Errorf("safe task due_on = %v, want %v", gotDue, wantDue)
	}
}

func TestApplyShiftEventAndChildren_IgnoresUnconfirmedTasks(t *testing.T) {
	db := startDB(t)
	ctx := context.Background()
	f := seed(ctx, t, db)
	defer purge(t, db, f.wsID)

	umbrellaStart := time.Date(2026, 6, 1, 10, 0, 0, 0, time.UTC)
	umbrella, _ := seedEvent(ctx, t, db, f, umbrellaStart)

	dueDate := time.Date(2026, 5, 28, 0, 0, 0, 0, time.UTC)
	skippedTaskID, _ := seedExtraTask(ctx, t, db, f, "Skipped", dueDate)
	linkContributesTo(ctx, t, db, f, skippedTaskID, umbrella)

	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	newStart := umbrellaStart.AddDate(0, 0, 7)
	if err := ApplyShiftEventAndChildren(ctx, tx, ApplyShiftEventAndChildrenArgs{
		WorkspaceID:      f.wsID,
		EventID:          umbrella,
		NewStartAt:       newStart,
		ConfirmedTaskIDs: nil, // empty = shift umbrella only
		ActorUserID:      f.userID,
	}); err != nil {
		_ = tx.Rollback()
		t.Fatalf("apply: %v", err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatalf("commit: %v", err)
	}

	var gotDue sql.NullTime
	if err := db.QueryRowContext(ctx,
		`SELECT due_on FROM tasks WHERE id = ?`, skippedTaskID,
	).Scan(&gotDue); err != nil {
		t.Fatalf("read task: %v", err)
	}
	if !gotDue.Valid || !gotDue.Time.Equal(dueDate) {
		t.Errorf("unconfirmed task due_on = %v, want unchanged %v", gotDue, dueDate)
	}
}

func TestApplyShiftEventAndChildren_IgnoresStaleTaskIDs(t *testing.T) {
	db := startDB(t)
	ctx := context.Background()
	f := seed(ctx, t, db)
	defer purge(t, db, f.wsID)

	umbrellaStart := time.Date(2026, 6, 1, 10, 0, 0, 0, time.UTC)
	umbrella, _ := seedEvent(ctx, t, db, f, umbrellaStart)

	dueDate := time.Date(2026, 5, 28, 0, 0, 0, 0, time.UTC)
	unrelatedTaskID, _ := seedExtraTask(ctx, t, db, f, "Unrelated", dueDate)
	// Note: NOT linked to umbrella.

	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	newStart := umbrellaStart.AddDate(0, 0, 7)
	if err := ApplyShiftEventAndChildren(ctx, tx, ApplyShiftEventAndChildrenArgs{
		WorkspaceID:      f.wsID,
		EventID:          umbrella,
		NewStartAt:       newStart,
		ConfirmedTaskIDs: []uint32{unrelatedTaskID, 999999},
		ActorUserID:      f.userID,
	}); err != nil {
		_ = tx.Rollback()
		t.Fatalf("apply: %v", err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatalf("commit: %v", err)
	}

	var gotDue sql.NullTime
	if err := db.QueryRowContext(ctx,
		`SELECT due_on FROM tasks WHERE id = ?`, unrelatedTaskID,
	).Scan(&gotDue); err != nil {
		t.Fatalf("read task: %v", err)
	}
	if !gotDue.Valid || !gotDue.Time.Equal(dueDate) {
		t.Errorf("unrelated task due_on = %v, want unchanged %v", gotDue, dueDate)
	}
}

func TestApplyShiftEventAndChildren_TimeOnlyChangeSkipsTasks(t *testing.T) {
	db := startDB(t)
	ctx := context.Background()
	f := seed(ctx, t, db)
	defer purge(t, db, f.wsID)

	umbrellaStart := time.Date(2026, 6, 1, 10, 0, 0, 0, time.UTC)
	umbrella, _ := seedEvent(ctx, t, db, f, umbrellaStart)

	dueDate := time.Date(2026, 5, 28, 0, 0, 0, 0, time.UTC)
	taskID, _ := seedExtraTask(ctx, t, db, f, "Same-day", dueDate)
	linkContributesTo(ctx, t, db, f, taskID, umbrella)

	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	// Shift umbrella by 30 minutes — DATE component unchanged.
	newStart := umbrellaStart.Add(30 * time.Minute)
	if err := ApplyShiftEventAndChildren(ctx, tx, ApplyShiftEventAndChildrenArgs{
		WorkspaceID:      f.wsID,
		EventID:          umbrella,
		NewStartAt:       newStart,
		ConfirmedTaskIDs: []uint32{taskID},
		ActorUserID:      f.userID,
	}); err != nil {
		_ = tx.Rollback()
		t.Fatalf("apply: %v", err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatalf("commit: %v", err)
	}

	// Umbrella moved, but the task's due_on stays put.
	var gotDue sql.NullTime
	if err := db.QueryRowContext(ctx,
		`SELECT due_on FROM tasks WHERE id = ?`, taskID,
	).Scan(&gotDue); err != nil {
		t.Fatalf("read task: %v", err)
	}
	if !gotDue.Valid || !gotDue.Time.Equal(dueDate) {
		t.Errorf("task due_on = %v, want unchanged %v", gotDue, dueDate)
	}
	var gotStart sql.NullTime
	if err := db.QueryRowContext(ctx,
		`SELECT start_at FROM calendar_events WHERE id = ?`, umbrella,
	).Scan(&gotStart); err != nil {
		t.Fatalf("read umbrella: %v", err)
	}
	if !gotStart.Valid || !gotStart.Time.Equal(newStart) {
		t.Errorf("umbrella start = %v, want %v", gotStart, newStart)
	}
}

func TestApplyShiftEventAndChildren_RejectsUndatedUmbrella(t *testing.T) {
	db := startDB(t)
	ctx := context.Background()
	f := seed(ctx, t, db)
	defer purge(t, db, f.wsID)
	pub := dbtype.New()
	res, err := db.ExecContext(ctx,
		`INSERT INTO calendar_events
		   (public_id, workspace_id, calendar_id, kind, visibility, show_as,
		    title, all_day, start_at, end_at, timezone,
		    owner_user_id, created_by_user_id)
		 VALUES (?, ?, ?, 'event', 'default', 'busy',
		         'Undated', FALSE, NULL, NULL, 'UTC',
		         ?, ?)`,
		pub, f.wsID, f.calendarID, f.userID, f.userID,
	)
	if err != nil {
		t.Fatalf("seed undated event: %v", err)
	}
	id64, _ := res.LastInsertId()
	evtID := uint32(id64) //#nosec G115 -- LastInsertId in test seed, fits uint32

	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	defer func() { _ = tx.Rollback() }()
	err = ApplyShiftEventAndChildren(ctx, tx, ApplyShiftEventAndChildrenArgs{
		WorkspaceID: f.wsID,
		EventID:     evtID,
		NewStartAt:  time.Date(2026, 6, 8, 10, 0, 0, 0, time.UTC),
		ActorUserID: f.userID,
	})
	if err == nil {
		t.Fatal("expected invariant error for undated umbrella, got nil")
	}
}
