package e2e

import (
	"context"
	"database/sql"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/nodate-flow/nodate-flow/apps/flow-api/internal/db/generated"
	"github.com/nodate-flow/nodate-flow/apps/flow-api/internal/notification"
	"github.com/nodate-flow/nodate-flow/packages/go-shared/email"
)

// TestNotificationDispatchOnAssignee verifies the fan-out contract for
// task.actor.added: when an owner posts a task and then assigns a
// workspace member as an actor, the recipient receives exactly one
// in-app notification row whose source_event_id and recipient_user_id
// match the underlying event row.
//
// The test server does not wire the production fan-out hook (that
// happens in cmd/api/main.go). To keep this e2e self-contained we
// follow the dedup test convention and invoke notification.Fanout's
// Hook function directly with the event ids surfaced by the API
// path — the same contract the production hook upholds, just driven
// synchronously.
func TestNotificationDispatchOnAssignee(t *testing.T) {
	bootstrap(t)
	t.Parallel()

	owner := newTenant(t)
	member := newTenant(t)

	// Promote member to a real workspace member via the invite flow.
	var invite struct {
		Token string `json:"token"`
	}
	doJSON(t, http.MethodPost,
		testServerURL+"/workspaces/"+owner.WorkspacePublicID+"/invites",
		owner.AccessToken, map[string]any{"role": "member"}, &invite)
	require.NotEmpty(t, invite.Token)
	doJSON(t, http.MethodPost,
		testServerURL+"/invites/"+invite.Token+"/accept",
		member.AccessToken, nil, nil)

	// Owner creates a task. This appends a task.created event.
	var task struct {
		ID string `json:"id"`
	}
	doJSON(t, http.MethodPost, testServerURL+"/tasks", owner.AccessToken,
		map[string]any{
			"projectId": owner.ProjectPublicID,
			"title":     "Notification dispatch target",
		}, &task)
	require.NotEmpty(t, task.ID)

	// Owner adds member as the assignee. This appends a task.actor.added
	// event row that the fan-out should consume.
	doJSON(t, http.MethodPost,
		testServerURL+"/tasks/"+task.ID+"/actors",
		owner.AccessToken, map[string]any{
			"userId": member.UserPublicID,
			"role":   "assignee",
		}, nil)

	ctx := context.Background()
	wsInternalID := lookupWorkspaceInternalID(ctx, t, testDB, owner.WorkspacePublicID)
	ownerInternalID := lookupUserInternalID(ctx, t, testDB, owner.UserPublicID)
	memberInternalID := lookupUserInternalID(ctx, t, testDB, member.UserPublicID)

	// Find the task.actor.added event row id this test produced. Anchor
	// on workspace + actor to avoid collisions with parallel tests.
	var actorEventID uint64
	err := testDB.QueryRowContext(ctx, `
		SELECT id
		FROM events
		WHERE workspace_id = ?
		  AND actor_user_id = ?
		  AND type = 'task.actor.added'
		ORDER BY id DESC
		LIMIT 1
	`, wsInternalID, ownerInternalID).Scan(&actorEventID)
	require.NoError(t, err, "expected a task.actor.added event row")
	require.NotZero(t, actorEventID)

	// Snapshot the recipient's pre-fanout count so the assertion is a
	// delta, not an absolute total (other parallel tests may also be
	// touching the table).
	beforeCount := notificationCountForUser(ctx, t, testDB, memberInternalID)

	// Drive the fan-out directly. The Hook helper spawns a goroutine,
	// so we must Shutdown to drain before asserting.
	queries := generated.New(testDB)
	f := notification.NewFanout(testDB, queries, email.NoopSender{})
	f.SetTimeout(5 * time.Second)
	hook := f.Hook()
	hook(ctx, wsInternalID, "task.actor.added", actorEventID)
	require.NoError(t, f.Shutdown(ctxWithTimeout(t, 10*time.Second)))

	afterCount := notificationCountForUser(ctx, t, testDB, memberInternalID)
	require.Equalf(t, int64(1), afterCount-beforeCount,
		"expected exactly 1 new notification for the assignee (before=%d after=%d)",
		beforeCount, afterCount)

	// Verify the new row's shape: it points at the right event, recipient,
	// channel, and event type. source_event_id is read as NullInt64 so
	// the assertion survives the in-flight INT→BIGINT migration.
	var (
		gotRecipientID  uint32
		gotSourceEvent  sql.NullInt64
		gotEventType    string
		gotChannel      string
		gotResourceType string
	)
	err = testDB.QueryRowContext(ctx, `
		SELECT recipient_user_id, source_event_id, event_type, channel, resource_type
		FROM notifications
		WHERE recipient_user_id = ?
		  AND event_type = 'task.actor.added'
		ORDER BY id DESC
		LIMIT 1
	`, memberInternalID).Scan(&gotRecipientID, &gotSourceEvent, &gotEventType, &gotChannel, &gotResourceType)
	require.NoError(t, err, "the dispatched notification row must be readable")
	require.Equal(t, memberInternalID, gotRecipientID)
	require.True(t, gotSourceEvent.Valid, "source_event_id must be populated by fan-out")
	require.Equal(t, actorEventID, uint64(gotSourceEvent.Int64))
	require.Equal(t, "task.actor.added", gotEventType)
	require.Equal(t, "in_app", gotChannel)
	require.Equal(t, "task", gotResourceType)
}
