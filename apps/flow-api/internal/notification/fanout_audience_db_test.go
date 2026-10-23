package notification

import (
	"context"
	"database/sql"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/generated"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/types"
	"github.com/libraz/nodate-flow/apps/flow-api/tests/helpers"
	"github.com/libraz/nodate-flow/packages/go-shared/testhelpers"
)

// addMember seeds one more enabled workspace member and returns its id.
func addMember(t *testing.T, db *sql.DB, wsID uint32, label string) uint32 {
	t.Helper()
	ctx := context.Background()
	pub := uuid.Must(uuid.NewV7())
	res, err := db.ExecContext(ctx,
		`INSERT INTO users (public_id, email, display_name, locale) VALUES (?, ?, ?, 'en')`,
		pub[:], "fanout-"+label+"-"+uuid.NewString()[:8]+"@example.test", "Fanout "+label)
	require.NoError(t, err)
	id, err := res.LastInsertId()
	require.NoError(t, err)
	userID := uint32(id) //#nosec G115 -- LastInsertId in test seed, fits uint32
	memberPub := uuid.Must(uuid.NewV7())
	_, err = db.ExecContext(ctx,
		`INSERT INTO workspace_members (public_id, workspace_id, user_id, role) VALUES (?, ?, ?, 'member')`,
		memberPub[:], wsID, userID)
	require.NoError(t, err)
	return userID
}

// appendTaskEvent writes an events row on the fixture task and returns its id.
func appendTaskEvent(t *testing.T, db *sql.DB, fx *fanoutFixture, eventType, payload string) uint64 {
	t.Helper()
	pub := uuid.Must(uuid.NewV7())
	res, err := db.ExecContext(context.Background(),
		`INSERT INTO events (public_id, workspace_id, type, actor_user_id, task_id, payload_json, occurred_at)
		 VALUES (?, ?, ?, ?, ?, ?, NOW(3))`,
		pub[:], fx.wsID, eventType, fx.actorUserID, fx.taskInternalID, payload)
	require.NoError(t, err)
	id, err := res.LastInsertId()
	require.NoError(t, err)
	return uint64(id) //#nosec G115 -- AUTO_INCREMENT LastInsertId is non-negative
}

// rowsForEvent counts the notification rows one event wrote per recipient.
func rowsForEvent(t *testing.T, db *sql.DB, wsID uint32, eventID uint64) map[uint32]int {
	t.Helper()
	rows, err := db.QueryContext(context.Background(),
		`SELECT recipient_user_id, COUNT(*) FROM notifications
		  WHERE workspace_id = ? AND source_event_id = ? GROUP BY recipient_user_id`,
		wsID, eventID)
	require.NoError(t, err)
	defer rows.Close()
	out := map[uint32]int{}
	for rows.Next() {
		var id uint32
		var n int
		require.NoError(t, rows.Scan(&id, &n))
		out[id] = n
	}
	require.NoError(t, rows.Err())
	return out
}

// TestFanout_ActorKindsReachOnlyTheNamedUser drives the addressed kinds
// against the real tables with three members who can all read the task:
// the add reaches only the added user, and the removal reaches the
// removed user even though their actor row is already disabled.
func TestFanout_ActorKindsReachOnlyTheNamedUser(t *testing.T) {
	testhelpers.SkipUnlessIntegration(t)
	inst := helpers.StartShared(t)
	db := inst.DB
	fx := seedFanoutFixture(t, db)
	third := addMember(t, db, fx.wsID, "third")

	actorPub := types.New()
	_, err := db.ExecContext(context.Background(),
		`INSERT INTO task_actors (public_id, workspace_id, task_id, user_id, role) VALUES (?, ?, ?, ?, 'assignee')`,
		actorPub, fx.wsID, fx.taskInternalID, third)
	require.NoError(t, err)

	f := NewFanout(db, generated.New(db))
	payload := `{"actorId":"` + actorPub.String() + `"}`

	added := appendTaskEvent(t, db, fx, "task.actor.added", payload)
	f.fanout(context.Background(), fx.wsID, "task.actor.added", added)
	require.Equal(t, map[uint32]int{third: 1}, rowsForEvent(t, db, fx.wsID, added),
		"an add must reach the added user and nobody else")

	_, err = db.ExecContext(context.Background(),
		`UPDATE task_actors SET enabled = FALSE WHERE public_id = ?`, actorPub)
	require.NoError(t, err)
	removed := appendTaskEvent(t, db, fx, "task.actor.removed", payload)
	f.fanout(context.Background(), fx.wsID, "task.actor.removed", removed)
	require.Equal(t, map[uint32]int{third: 1}, rowsForEvent(t, db, fx.wsID, removed),
		"a removal must reach the removed user and nobody else")
}

// TestFanout_DeleteNotifiesEachReaderOnce replays a delete the way
// itemkit commits it — task disabled, then item.deleted and its legacy
// task.disabled twin — and requires one row per reader across both.
func TestFanout_DeleteNotifiesEachReaderOnce(t *testing.T) {
	testhelpers.SkipUnlessIntegration(t)
	inst := helpers.StartShared(t)
	db := inst.DB
	fx := seedFanoutFixture(t, db)

	_, err := db.ExecContext(context.Background(),
		`UPDATE tasks SET enabled = FALSE WHERE id = ?`, fx.taskInternalID)
	require.NoError(t, err)

	f := NewFanout(db, generated.New(db))
	itemDeleted := appendTaskEvent(t, db, fx, "item.deleted", `{}`)
	taskDisabled := appendTaskEvent(t, db, fx, "task.disabled", `{}`)
	f.fanout(context.Background(), fx.wsID, "item.deleted", itemDeleted)
	f.fanout(context.Background(), fx.wsID, "task.disabled", taskDisabled)

	total := 0
	for _, n := range rowsForEvent(t, db, fx.wsID, itemDeleted) {
		total += n
	}
	for _, n := range rowsForEvent(t, db, fx.wsID, taskDisabled) {
		total += n
	}
	require.Equal(t, 1, total, "the one reader besides the actor must get exactly one delete notification")
	require.Equal(t, map[uint32]int{fx.recipientUserID: 1}, rowsForEvent(t, db, fx.wsID, itemDeleted))
}

// TestNotificationReadsFollowMembership holds the list and unread-count
// queries to current membership: a member who left stops seeing that
// workspace's rows on every path.
func TestNotificationReadsFollowMembership(t *testing.T) {
	testhelpers.SkipUnlessIntegration(t)
	inst := helpers.StartShared(t)
	db := inst.DB
	fx := seedFanoutFixture(t, db)
	ctx := context.Background()
	q := generated.New(db)

	f := NewFanout(db, q)
	f.fanout(ctx, fx.wsID, "task.created", fx.eventInternalID)

	count := func() int64 {
		n, err := q.CountUnreadNotifications(ctx, fx.recipientUserID)
		require.NoError(t, err)
		return n
	}
	list := func() int {
		rows, err := q.ListNotificationsForUser(ctx, generated.ListNotificationsForUserParams{
			RecipientUserID: fx.recipientUserID, Limit: 50, Offset: 0,
		})
		require.NoError(t, err)
		return len(rows)
	}
	require.EqualValues(t, 1, count(), "fixture: the member has one unread notification")
	require.Equal(t, 1, list())

	_, err := db.ExecContext(ctx,
		`UPDATE workspace_members SET enabled = FALSE WHERE workspace_id = ? AND user_id = ?`,
		fx.wsID, fx.recipientUserID)
	require.NoError(t, err)

	require.EqualValues(t, 0, count(), "a former member's badge must not count that workspace's rows")
	require.Equal(t, 0, list(), "a former member's list must not show that workspace's rows")

	wsCount, err := q.CountUnreadNotificationsForWorkspace(ctx, generated.CountUnreadNotificationsForWorkspaceParams{
		WorkspaceID: fx.wsID, RecipientUserID: fx.recipientUserID,
	})
	require.NoError(t, err)
	require.EqualValues(t, 0, wsCount)
}
