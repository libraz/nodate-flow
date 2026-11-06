package itemkit

import (
	"context"
	"database/sql"
	"testing"

	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/dbretry"
	"github.com/libraz/nodate-flow/packages/go-shared/eventbus"
)

// countEventUnlinks counts the item.unscheduled rows that name one
// calendar event, which is what the single-row withdrawal appends.
func countEventUnlinks(t *testing.T, db *sql.DB, workspaceID, eventID uint32) int {
	t.Helper()
	var n int
	if err := db.QueryRow(`SELECT COUNT(*) FROM events e
JOIN calendar_events ce ON ce.id = ?
WHERE e.workspace_id = ?
  AND e.type = ?
  AND JSON_UNQUOTE(JSON_EXTRACT(e.payload_json, '$.eventPublicId')) = BIN_TO_UUID(ce.public_id, 0)`,
		eventID, workspaceID, string(eventbus.ItemUnscheduled)).Scan(&n); err != nil {
		t.Fatalf("count unlink events for calendar_events id=%d: %v", eventID, err)
	}
	return n
}

// TestUnscheduleTaskDueAppendsNoUnlinkForAlreadyWithdrawnEvent pins the
// lost race: the due link is read live from the transaction's snapshot,
// but another writer has already disabled it, so the disable affects no
// row and the unschedule may append no unlink event of its own for it.
func TestUnscheduleTaskDueAppendsNoUnlinkForAlreadyWithdrawnEvent(t *testing.T) {
	db := startDB(t)
	ctx := context.Background()
	fx, dueEvtID := scheduleFixture(t, db)
	var afterWithdrawal int

	err := dbretry.InTx(ctx, db, "itemkit.test", nil, func(ctx context.Context, tx *dbretry.Tx) error {
		// Fix the consistent-read snapshot while the event is still live.
		var enabled bool
		if err := tx.QueryRowContext(ctx, `SELECT enabled FROM calendar_events WHERE id = ?`, dueEvtID).Scan(&enabled); err != nil {
			return err
		}
		if !enabled {
			t.Fatal("fixture due event must start enabled")
		}
		withTx(t, db, func(other TX) {
			if err := DeleteEvent(ctx, other, fx.wsID, dueEvtID, fx.userID); err != nil {
				t.Fatalf("concurrent DeleteEvent: %v", err)
			}
		})
		afterWithdrawal = countEventUnlinks(t, db, fx.wsID, dueEvtID)
		return UnscheduleTask(ctx, tx, UnscheduleTaskArgs{
			WorkspaceID: fx.wsID,
			TaskID:      fx.taskID,
			ActorUserID: fx.userID,
			Role:        RoleDue,
		})
	})
	if err != nil {
		t.Fatalf("UnscheduleTask(due): %v", err)
	}
	if n := countEventUnlinks(t, db, fx.wsID, dueEvtID); n != afterWithdrawal {
		t.Fatalf("unlink events for an event another writer withdrew = %d, want the withdrawing writer's %d", n, afterWithdrawal)
	}
}
