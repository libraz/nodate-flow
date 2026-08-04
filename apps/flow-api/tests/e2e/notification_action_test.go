package e2e

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/nodate-flow/nodate-flow/apps/flow-api/internal/db/generated"
	"github.com/nodate-flow/nodate-flow/apps/flow-api/internal/notification"
	"github.com/nodate-flow/nodate-flow/packages/go-shared/email"
)

// TestNotificationMarkReadAndArchive verifies that individual
// notifications can be marked as read and archived.
//
// The test server intentionally does not wire the production fan-out
// hook (that lives in cmd/api/main.go), so we drive the fan-out
// synchronously here — the same pattern as
// TestNotificationDispatchOnAssignee — to make the precondition
// (one assignee notification exists) deterministic. The previous
// implementation skipped on `list.Total == 0`, hiding any actual
// fan-out regression behind a "may be async" justification.
func TestNotificationMarkReadAndArchive(t *testing.T) {
	bootstrap(t)
	t.Parallel()

	owner := newTenant(t)
	member := newTenant(t)

	// Invite member so an action generates a notification.
	var invite struct {
		Token string `json:"token"`
	}
	doJSON(t, http.MethodPost,
		testServerURL+"/workspaces/"+owner.WorkspacePublicID+"/invites",
		owner.AccessToken, map[string]any{"role": "member"}, &invite)
	doJSON(t, http.MethodPost,
		testServerURL+"/invites/"+invite.Token+"/accept",
		member.AccessToken, nil, nil)

	// Create a task assigned to member to trigger a notification.
	var task struct {
		ID string `json:"id"`
	}
	doJSON(t, http.MethodPost, testServerURL+"/tasks", owner.AccessToken,
		map[string]any{
			"projectId": owner.ProjectPublicID,
			"title":     "Notification trigger",
		}, &task)
	doJSON(t, http.MethodPost,
		testServerURL+"/tasks/"+task.ID+"/actors",
		owner.AccessToken, map[string]any{
			"userId": member.UserPublicID,
			"role":   "assignee",
		}, nil)

	// Drive the fan-out synchronously against the task.actor.added
	// event row produced above. Anchor on workspace + actor to avoid
	// collisions with parallel tests that emit the same event type.
	ctx := context.Background()
	wsInternalID := lookupWorkspaceInternalID(ctx, t, testDB, owner.WorkspacePublicID)
	ownerInternalID := lookupUserInternalID(ctx, t, testDB, owner.UserPublicID)

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

	queries := generated.New(testDB)
	f := notification.NewFanout(testDB, queries, email.NoopSender{})
	f.SetTimeout(5 * time.Second)
	hook := f.Hook()
	hook(ctx, wsInternalID, "task.actor.added", actorEventID)
	require.NoError(t, f.Shutdown(ctxWithTimeout(t, 10*time.Second)))

	// Now the assertion is a hard precondition: the fan-out MUST have
	// produced exactly one in-app notification for the member.
	var list struct {
		Total         int64 `json:"total"`
		Notifications []struct {
			ID   string `json:"id"`
			Read bool   `json:"read"`
		} `json:"notifications"`
	}
	doJSON(t, http.MethodGet,
		testServerURL+"/me/notifications",
		member.AccessToken, nil, &list)
	require.Greater(t, list.Total, int64(0),
		"expected at least 1 notification after task.actor.added fan-out")
	require.NotEmpty(t, list.Notifications)

	notifID := list.Notifications[0].ID
	require.NotEmpty(t, notifID)

	// Mark as read.
	var ok struct {
		Ok bool `json:"ok"`
	}
	doJSON(t, http.MethodPost,
		testServerURL+"/notifications/"+notifID+"/read",
		member.AccessToken, nil, &ok)
	require.True(t, ok.Ok)

	// Archive.
	doJSON(t, http.MethodPost,
		testServerURL+"/notifications/"+notifID+"/archive",
		member.AccessToken, nil, &ok)
	require.True(t, ok.Ok)
}
