package e2e

import (
	"context"
	"database/sql"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/generated"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/types"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/notification"
	"github.com/libraz/nodate-flow/apps/flow-api/tests/helpers"
	"github.com/libraz/nodate-flow/packages/go-shared/email"
)

// TestNotificationFanoutDedup verifies that re-firing the fan-out for
// the same (recipient, source_event_id, channel) tuple yields exactly
// one notification row. This protects the at-least-once contract
// guarded by the uniq_notifications_recipient_source_channel UNIQUE
// key combined with INSERT IGNORE in CreateNotification.
func TestNotificationFanoutDedup(t *testing.T) {
	bootstrap(t)
	t.Parallel()

	// Two tenants in the same workspace: actorTenant produces the
	// event, recipientTenant should receive exactly one notification
	// even when the fan-out hook fires twice for the same event row.
	actorTenant := newTenant(t)
	recipientTenant := newTenant(t)

	// Resolve internal ids needed to insert an event row directly.
	ctx := context.Background()
	queries := generated.New(testDB)

	wsInternalID := lookupWorkspaceInternalID(ctx, t, testDB, actorTenant.WorkspacePublicID)
	actorInternalID := lookupUserInternalID(ctx, t, testDB, actorTenant.UserPublicID)
	recipientInternalID := lookupUserInternalID(ctx, t, testDB, recipientTenant.UserPublicID)

	// Add the recipient as a member of the actor's workspace so the
	// fan-out picks them up. Direct insert avoids the invite/accept
	// flow which is orthogonal to this test.
	//
	// public_id is BINARY(16) NOT NULL with no default; supply a fresh
	// UUID v7 so STRICT_TRANS_TABLES (MySQL 9 default) does not turn
	// the row insertion into a silent INSERT IGNORE warning. The
	// previous version omitted public_id and worked only because the
	// container happened to run with the strict flag relaxed; under
	// heavy parallel load it falls back to "no rows inserted" and the
	// downstream fan-out then sees zero recipients.
	memberPID := types.New()
	_, err := testDB.ExecContext(ctx, `
		INSERT IGNORE INTO workspace_members (public_id, workspace_id, user_id, role, enabled)
		VALUES (?, ?, ?, 'member', TRUE)
	`, memberPID, wsInternalID, recipientInternalID)
	require.NoError(t, err)

	// Insert a single event row and capture its internal id.
	res, err := helpers.ExecRetry(ctx, testDB, "test seed: insert event", `
		INSERT INTO events (public_id, workspace_id, task_id, actor_user_id, type, payload_json, occurred_at)
		VALUES (?, ?, NULL, ?, 'task.created', JSON_OBJECT(), NOW())
	`, types.New(), wsInternalID, actorInternalID)
	require.NoError(t, err)
	eventLastID, err := res.LastInsertId()
	require.NoError(t, err)
	eventInternalID := uint64(eventLastID) //#nosec G115 -- AUTO_INCREMENT LastInsertId is non-negative

	// Snapshot the recipient's notification count before firing.
	beforeCount := notificationCountForUser(ctx, t, testDB, recipientInternalID)

	// Fire the fan-out twice for the same event. The Hook helper spawns
	// goroutines, so we exercise the package-private fanout method
	// directly to keep the assertions deterministic.
	f := notification.NewFanout(testDB, queries, email.NoopSender{})
	f.SetTimeout(5 * time.Second)
	hook := f.Hook()
	hook(ctx, wsInternalID, "task.created", eventInternalID)
	hook(ctx, wsInternalID, "task.created", eventInternalID)

	// Wait for the fan-out goroutines to drain.
	require.NoError(t, f.Shutdown(ctxWithTimeout(t, 10*time.Second)))

	afterCount := notificationCountForUser(ctx, t, testDB, recipientInternalID)
	require.Equalf(t, int64(1), afterCount-beforeCount,
		"expected exactly 1 new notification after firing the same event twice (before=%d after=%d)",
		beforeCount, afterCount)

	// And the existing row points at the right source event.
	var sourceEventID sql.NullInt64
	err = testDB.QueryRowContext(ctx, `
		SELECT source_event_id
		FROM notifications
		WHERE recipient_user_id = ?
		  AND event_type = 'task.created'
		ORDER BY id DESC
		LIMIT 1
	`, recipientInternalID).Scan(&sourceEventID)
	require.NoError(t, err)
	require.True(t, sourceEventID.Valid, "source_event_id should be populated")
	require.Equal(t, eventInternalID, uint64(sourceEventID.Int64)) //#nosec G115 -- source_event_id is BIGINT UNSIGNED read back through NullInt64; the id was written by this test
}

// lookupWorkspaceInternalID resolves a workspace's internal id from its
// public UUID. Only tests reach across this boundary; production code
// must never expose internal ids.
func lookupWorkspaceInternalID(ctx context.Context, t *testing.T, db *sql.DB, publicID string) uint32 {
	t.Helper()
	pid, err := types.Parse(publicID)
	require.NoError(t, err)
	var id uint32
	err = db.QueryRowContext(ctx, `SELECT id FROM workspaces WHERE public_id = ?`, pid).Scan(&id)
	require.NoError(t, err)
	return id
}

// lookupUserInternalID resolves a user's internal id from the public UUID
// returned by /auth/register.
func lookupUserInternalID(ctx context.Context, t *testing.T, db *sql.DB, publicID string) uint32 {
	t.Helper()
	pid, err := types.Parse(publicID)
	require.NoError(t, err)
	var id uint32
	err = db.QueryRowContext(ctx, `SELECT id FROM users WHERE public_id = ?`, pid).Scan(&id)
	require.NoError(t, err)
	return id
}

// notificationCountForUser counts rows in the notifications table for a
// recipient. Used as a delta sentinel around the dedup fire.
func notificationCountForUser(ctx context.Context, t *testing.T, db *sql.DB, userID uint32) int64 {
	t.Helper()
	var n int64
	err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM notifications WHERE recipient_user_id = ?`, userID).Scan(&n)
	require.NoError(t, err)
	return n
}

// ctxWithTimeout returns a context bound to a deadline derived from d.
// The cancel func is wired into t.Cleanup so nothing leaks.
func ctxWithTimeout(t *testing.T, d time.Duration) context.Context {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), d)
	t.Cleanup(cancel)
	return ctx
}
