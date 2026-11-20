package e2e

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/generated"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/notification"
)

type listedNotification struct {
	ResourceID    string `json:"resourceId"`
	ResourceTitle string `json:"resourceTitle"`
}

// TestNotificationResourceTitleFollowsReadability pins that a task
// notification names the task while the recipient can read it, and
// carries no title once the task has become unreadable to them.
func TestNotificationResourceTitleFollowsReadability(t *testing.T) {
	bootstrap(t)
	t.Parallel()

	owner := newTenant(t)
	member := inviteAndJoinWorkspace(t, owner)
	title := "notification-title-" + randomHex(4)

	var task struct {
		ID string `json:"id"`
	}
	doJSON(t, http.MethodPost, testServerURL+"/tasks", owner.AccessToken, map[string]any{
		"projectId": owner.ProjectPublicID, "title": title, "visibility": "public",
	}, &task)
	require.NotEmpty(t, task.ID)
	doJSON(t, http.MethodPost, testServerURL+"/tasks/"+task.ID+"/actors", owner.AccessToken,
		map[string]any{"userId": member.UserPublicID, "role": "assignee"}, nil)

	ctx := context.Background()
	wsInternalID := lookupWorkspaceInternalID(ctx, t, testDB, owner.WorkspacePublicID)
	ownerInternalID := lookupUserInternalID(ctx, t, testDB, owner.UserPublicID)
	var eventID uint64
	require.NoError(t, testDB.QueryRowContext(ctx, `
		SELECT id FROM events
		WHERE workspace_id = ? AND actor_user_id = ? AND type = 'task.actor.added'
		ORDER BY id DESC LIMIT 1`, wsInternalID, ownerInternalID).Scan(&eventID))

	f := notification.NewFanout(testDB, generated.New(testDB))
	f.SetTimeout(5 * time.Second)
	f.Hook()(ctx, wsInternalID, "task.actor.added", eventID)
	require.NoError(t, f.Shutdown(ctxWithTimeout(t, 10*time.Second)))

	list := func() listedNotification {
		t.Helper()
		var out struct {
			Notifications []listedNotification `json:"notifications"`
		}
		doJSON(t, http.MethodGet,
			testServerURL+"/me/notifications?workspaceId="+owner.WorkspacePublicID,
			member.AccessToken, nil, &out)
		for _, n := range out.Notifications {
			if n.ResourceID == task.ID {
				return n
			}
		}
		require.FailNowf(t, "notification missing", "no notification for task %s in %+v", task.ID, out.Notifications)
		return listedNotification{}
	}

	require.Equal(t, title, list().ResourceTitle, "readable task: the title is present")

	// Drop the member's actor row, then make the task private: the
	// notification row stays but the task is no longer readable to them.
	actorID := visibleUserActorID(t, owner.AccessToken, task.ID, member.UserPublicID)
	removeTaskActorForMutationTest(t, owner.AccessToken, task.ID, actorID)
	doJSON(t, http.MethodPatch, testServerURL+"/tasks/"+task.ID, owner.AccessToken,
		map[string]any{"visibility": "private"}, nil)

	require.Empty(t, list().ResourceTitle, "unreadable task: the title must be absent")
}
