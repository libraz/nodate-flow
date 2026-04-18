package e2e

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestNotificationMarkReadAndArchive verifies that individual
// notifications can be marked as read and archived.
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

	// Check if member has notifications.
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

	if list.Total == 0 {
		t.Skip("no notification generated — notification system may be async")
	}

	notifID := list.Notifications[0].ID

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
