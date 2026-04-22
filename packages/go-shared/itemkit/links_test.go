package itemkit

import (
	"context"
	"database/sql"
	"errors"
	"testing"
	"time"

	"github.com/nodate-flow/nodate-flow/packages/go-shared/dbtype"
)

// seedEvent inserts a standalone calendar_event (no task link) and
// returns its internal id + public id. Mirror of what reschedule /
// schedule tests use but without requiring task projection.
func seedEvent(t *testing.T, ctx context.Context, db *sql.DB, f fixtures, startAt time.Time) (uint32, dbtype.PublicID) {
	t.Helper()
	pub := dbtype.New()
	endAt := startAt.Add(time.Hour)
	res, err := db.ExecContext(ctx,
		`INSERT INTO calendar_events
		   (public_id, workspace_id, calendar_id, kind, visibility, show_as,
		    title, all_day, start_at, end_at, timezone,
		    owner_user_id, created_by_user_id)
		 VALUES (?, ?, ?, 'event', 'default', 'busy',
		         'Linked event', FALSE, ?, ?, 'UTC',
		         ?, ?)`,
		pub, f.wsID, f.calendarID, startAt, endAt, f.userID, f.userID,
	)
	if err != nil {
		t.Fatalf("seed event: %v", err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		t.Fatalf("seed event id: %v", err)
	}
	return uint32(id), pub
}

func TestLinkTaskToEvent_CreatesAndAppendsEvent(t *testing.T) {
	db := startDB(t)
	ctx := context.Background()
	f := seed(t, ctx, db)
	defer purge(t, db, f.wsID)
	evtID, _ := seedEvent(t, ctx, db, f, time.Date(2026, 5, 1, 10, 0, 0, 0, time.UTC))

	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	defer tx.Rollback()
	pub, linkID, err := LinkTaskToEvent(ctx, tx, LinkTaskToEventArgs{
		WorkspaceID: f.wsID,
		TaskID:      f.taskID,
		EventID:     evtID,
		Relation:    RelationContributesTo,
		ActorUserID: f.userID,
	})
	if err != nil {
		t.Fatalf("LinkTaskToEvent: %v", err)
	}
	if linkID == 0 || pub == (dbtype.PublicID{}) {
		t.Errorf("expected non-zero ID + public_id, got id=%d pub=%v", linkID, pub)
	}
	if err := tx.Commit(); err != nil {
		t.Fatalf("commit: %v", err)
	}

	var relation string
	var enabled bool
	row := db.QueryRowContext(ctx,
		`SELECT relation, enabled FROM task_event_links WHERE id = ?`, linkID)
	if err := row.Scan(&relation, &enabled); err != nil {
		t.Fatalf("verify link row: %v", err)
	}
	if relation != "contributes_to" || !enabled {
		t.Errorf("unexpected row: relation=%s enabled=%v", relation, enabled)
	}
	// Events log should have an item.milestone.link.added row.
	var count int
	if err := db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM events WHERE workspace_id = ? AND type = 'item.milestone.link.added'`,
		f.wsID).Scan(&count); err != nil {
		t.Fatalf("count events: %v", err)
	}
	if count != 1 {
		t.Errorf("expected 1 link-added event, got %d", count)
	}
}

func TestLinkTaskToEvent_IsIdempotent(t *testing.T) {
	db := startDB(t)
	ctx := context.Background()
	f := seed(t, ctx, db)
	defer purge(t, db, f.wsID)
	evtID, _ := seedEvent(t, ctx, db, f, time.Date(2026, 5, 2, 9, 0, 0, 0, time.UTC))

	var firstPub dbtype.PublicID
	for i := 0; i < 2; i++ {
		tx, err := db.BeginTx(ctx, nil)
		if err != nil {
			t.Fatalf("begin: %v", err)
		}
		pub, _, err := LinkTaskToEvent(ctx, tx, LinkTaskToEventArgs{
			WorkspaceID: f.wsID,
			TaskID:      f.taskID,
			EventID:     evtID,
			Relation:    RelationContributesTo,
			ActorUserID: f.userID,
		})
		if err != nil {
			tx.Rollback()
			t.Fatalf("iter %d: %v", i, err)
		}
		if err := tx.Commit(); err != nil {
			t.Fatalf("commit iter %d: %v", i, err)
		}
		if i == 0 {
			firstPub = pub
		} else if pub != firstPub {
			t.Errorf("second call returned new link: want %v, got %v", firstPub, pub)
		}
	}
	var rowCount int
	if err := db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM task_event_links
		 WHERE task_id = ? AND event_id = ? AND relation = 'contributes_to' AND enabled = TRUE`,
		f.taskID, evtID).Scan(&rowCount); err != nil {
		t.Fatalf("count rows: %v", err)
	}
	if rowCount != 1 {
		t.Errorf("expected 1 link row, got %d", rowCount)
	}
	var evtCount int
	if err := db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM events WHERE workspace_id = ? AND type = 'item.milestone.link.added'`,
		f.wsID).Scan(&evtCount); err != nil {
		t.Fatalf("count events: %v", err)
	}
	if evtCount != 1 {
		t.Errorf("expected 1 link-added event after idempotent retry, got %d", evtCount)
	}
}

func TestLinkTaskToEvent_RejectsUnknownRelation(t *testing.T) {
	db := startDB(t)
	ctx := context.Background()
	f := seed(t, ctx, db)
	defer purge(t, db, f.wsID)
	evtID, _ := seedEvent(t, ctx, db, f, time.Date(2026, 5, 3, 9, 0, 0, 0, time.UTC))

	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	defer tx.Rollback()
	_, _, err = LinkTaskToEvent(ctx, tx, LinkTaskToEventArgs{
		WorkspaceID: f.wsID,
		TaskID:      f.taskID,
		EventID:     evtID,
		Relation:    "unknown",
		ActorUserID: f.userID,
	})
	if err == nil {
		t.Fatal("expected invariant error, got nil")
	}
}

func TestLinkTaskToEvent_RejectsMissingTask(t *testing.T) {
	db := startDB(t)
	ctx := context.Background()
	f := seed(t, ctx, db)
	defer purge(t, db, f.wsID)
	evtID, _ := seedEvent(t, ctx, db, f, time.Date(2026, 5, 4, 9, 0, 0, 0, time.UTC))

	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	defer tx.Rollback()
	_, _, err = LinkTaskToEvent(ctx, tx, LinkTaskToEventArgs{
		WorkspaceID: f.wsID,
		TaskID:      999999,
		EventID:     evtID,
		Relation:    RelationContributesTo,
		ActorUserID: f.userID,
	})
	if err == nil {
		t.Fatal("expected not-found error, got nil")
	}
}

func TestUnlinkTaskFromEvent_SoftDisables(t *testing.T) {
	db := startDB(t)
	ctx := context.Background()
	f := seed(t, ctx, db)
	defer purge(t, db, f.wsID)
	evtID, _ := seedEvent(t, ctx, db, f, time.Date(2026, 5, 5, 9, 0, 0, 0, time.UTC))

	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	pub, _, err := LinkTaskToEvent(ctx, tx, LinkTaskToEventArgs{
		WorkspaceID: f.wsID,
		TaskID:      f.taskID,
		EventID:     evtID,
		Relation:    RelationContributesTo,
		ActorUserID: f.userID,
	})
	if err != nil {
		tx.Rollback()
		t.Fatalf("link: %v", err)
	}
	if err := UnlinkTaskFromEvent(ctx, tx, UnlinkTaskFromEventArgs{
		WorkspaceID: f.wsID,
		LinkID:      pub,
		ActorUserID: f.userID,
	}); err != nil {
		tx.Rollback()
		t.Fatalf("unlink: %v", err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatalf("commit: %v", err)
	}
	var enabled bool
	if err := db.QueryRowContext(ctx,
		`SELECT enabled FROM task_event_links WHERE public_id = ?`, pub).Scan(&enabled); err != nil {
		t.Fatalf("scan: %v", err)
	}
	if enabled {
		t.Error("link should be soft-disabled")
	}
	// item.milestone.link.removed must be appended in addition to link.added.
	var addCount, removeCount int
	if err := db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM events WHERE workspace_id = ? AND type = 'item.milestone.link.added'`,
		f.wsID).Scan(&addCount); err != nil {
		t.Fatalf("count add: %v", err)
	}
	if err := db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM events WHERE workspace_id = ? AND type = 'item.milestone.link.removed'`,
		f.wsID).Scan(&removeCount); err != nil {
		t.Fatalf("count remove: %v", err)
	}
	if addCount != 1 || removeCount != 1 {
		t.Errorf("expected 1 added + 1 removed, got %d + %d", addCount, removeCount)
	}
}

func TestUnlinkTaskFromEvent_ReturnsNotFoundForAlreadyDisabled(t *testing.T) {
	db := startDB(t)
	ctx := context.Background()
	f := seed(t, ctx, db)
	defer purge(t, db, f.wsID)

	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	defer tx.Rollback()
	// Bogus link id — not present.
	err = UnlinkTaskFromEvent(ctx, tx, UnlinkTaskFromEventArgs{
		WorkspaceID: f.wsID,
		LinkID:      dbtype.New(),
		ActorUserID: f.userID,
	})
	if !errors.Is(err, sql.ErrNoRows) {
		t.Errorf("want sql.ErrNoRows, got %v", err)
	}
}

func TestLinkTaskToEvent_DifferentRelationsCoexist(t *testing.T) {
	db := startDB(t)
	ctx := context.Background()
	f := seed(t, ctx, db)
	defer purge(t, db, f.wsID)
	evtID, _ := seedEvent(t, ctx, db, f, time.Date(2026, 5, 6, 9, 0, 0, 0, time.UTC))

	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	defer tx.Rollback()
	for _, rel := range []Relation{
		RelationContributesTo, RelationBlocks, RelationDependsOn, RelationPrepFor,
	} {
		if _, _, err := LinkTaskToEvent(ctx, tx, LinkTaskToEventArgs{
			WorkspaceID: f.wsID,
			TaskID:      f.taskID,
			EventID:     evtID,
			Relation:    rel,
			ActorUserID: f.userID,
		}); err != nil {
			t.Fatalf("LinkTaskToEvent(%s): %v", rel, err)
		}
	}
	if err := tx.Commit(); err != nil {
		t.Fatalf("commit: %v", err)
	}
	var count int
	if err := db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM task_event_links
		 WHERE task_id = ? AND event_id = ? AND enabled = TRUE`,
		f.taskID, evtID).Scan(&count); err != nil {
		t.Fatalf("count: %v", err)
	}
	if count != 4 {
		t.Errorf("expected 4 relation rows, got %d", count)
	}
}
