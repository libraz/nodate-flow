package itemkit

import (
	"context"
	"database/sql"
	"testing"

	"github.com/libraz/nodate-flow/packages/go-shared/eventbus"
)

// requireNoUserActor asserts every event of kind on the task is recorded
// without a human actor.
func requireNoUserActor(t *testing.T, db *sql.DB, workspaceID, taskID uint32, kind eventbus.Kind) {
	t.Helper()
	rows, err := db.Query(`SELECT actor_user_id FROM events WHERE workspace_id = ? AND task_id = ? AND type = ?`,
		workspaceID, taskID, string(kind))
	if err != nil {
		t.Fatalf("read %s events: %v", kind, err)
	}
	defer rows.Close()
	n := 0
	for rows.Next() {
		var actor sql.NullInt64
		if err := rows.Scan(&actor); err != nil {
			t.Fatalf("scan %s event: %v", kind, err)
		}
		if actor.Valid {
			t.Fatalf("%s event recorded user actor %d for a call with no actor", kind, actor.Int64)
		}
		n++
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate %s events: %v", kind, err)
	}
	if n == 0 {
		t.Fatalf("no %s event recorded", kind)
	}
}

// TestDeleteTaskWithoutActorRecordsNoUserActor records a delete made with no
// acting user as actorless rather than as user 0.
func TestDeleteTaskWithoutActorRecordsNoUserActor(t *testing.T) {
	db := startDB(t)
	fx, _ := scheduleFixture(t, db)

	withTx(t, db, func(tx TX) {
		deleted, err := DeleteTask(context.Background(), tx, fx.wsID, fx.taskID, 0)
		if err != nil {
			t.Fatalf("DeleteTask: %v", err)
		}
		if !deleted {
			t.Fatal("DeleteTask reported nothing deleted")
		}
	})
	requireNoUserActor(t, db, fx.wsID, fx.taskID, eventbus.ItemDeleted)
}

// TestDeleteEventWithoutActorRecordsNoUserActor is the same rule for a
// projection event delete.
func TestDeleteEventWithoutActorRecordsNoUserActor(t *testing.T) {
	db := startDB(t)
	fx, dueEvtID := scheduleFixture(t, db)

	withTx(t, db, func(tx TX) {
		if err := DeleteEvent(context.Background(), tx, fx.wsID, dueEvtID, 0); err != nil {
			t.Fatalf("DeleteEvent: %v", err)
		}
	})
	requireNoUserActor(t, db, fx.wsID, fx.taskID, eventbus.ItemUnscheduled)
}
