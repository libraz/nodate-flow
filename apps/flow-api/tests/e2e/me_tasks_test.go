package e2e

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestMyTasksListAcrossWorkspaces verifies that GET /me/tasks
// returns tasks the user is an actor on, across workspaces.
func TestMyTasksListAcrossWorkspaces(t *testing.T) {
	bootstrap(t)
	t.Parallel()

	user := newTenant(t)
	other := newTenant(t)

	// Create a task in user's own workspace and assign self.
	var ownTask struct {
		ID string `json:"id"`
	}
	doJSON(t, http.MethodPost, testServerURL+"/tasks", user.AccessToken,
		map[string]any{"projectId": user.ProjectPublicID, "title": "My own task"}, &ownTask)
	doJSON(t, http.MethodPost,
		testServerURL+"/tasks/"+ownTask.ID+"/actors",
		user.AccessToken, map[string]any{
			"userId": user.UserPublicID,
			"role":   "assignee",
		}, nil)

	// Invite user to other's workspace.
	var invite struct {
		Token string `json:"token"`
	}
	doJSON(t, http.MethodPost,
		testServerURL+"/workspaces/"+other.WorkspacePublicID+"/invites",
		other.AccessToken, map[string]any{"role": "member"}, &invite)
	doJSON(t, http.MethodPost,
		testServerURL+"/invites/"+invite.Token+"/accept",
		user.AccessToken, nil, nil)

	// Create a task in other's workspace and assign our user.
	var otherTask struct {
		ID string `json:"id"`
	}
	doJSON(t, http.MethodPost, testServerURL+"/tasks", other.AccessToken,
		map[string]any{"projectId": other.ProjectPublicID, "title": "Cross-workspace task"}, &otherTask)
	doJSON(t, http.MethodPost,
		testServerURL+"/tasks/"+otherTask.ID+"/actors",
		other.AccessToken, map[string]any{
			"userId": user.UserPublicID,
			"role":   "assignee",
		}, nil)

	// GET /me/tasks should return both.
	var myTasks struct {
		Total int64 `json:"total"`
		Tasks []struct {
			ID            string `json:"id"`
			WorkspaceID   string `json:"workspaceId"`
			WorkspaceName string `json:"workspaceName"`
		} `json:"tasks"`
	}
	doJSON(t, http.MethodGet, testServerURL+"/me/tasks",
		user.AccessToken, nil, &myTasks)
	require.GreaterOrEqual(t, myTasks.Total, int64(2),
		"must see tasks from both workspaces")

	foundOwn := false
	foundOther := false
	for _, task := range myTasks.Tasks {
		if task.ID == ownTask.ID {
			foundOwn = true
		}
		if task.ID == otherTask.ID {
			foundOther = true
			require.NotEmpty(t, task.WorkspaceName,
				"cross-workspace task must include workspace name")
		}
	}
	require.True(t, foundOwn, "must see own workspace task")
	require.True(t, foundOther, "must see other workspace task")
}

// TestMyTasksEmptyWhenNoActorRole verifies that GET /me/tasks
// returns an empty list when the user is not an actor on any task.
func TestMyTasksEmptyWhenNoActorRole(t *testing.T) {
	bootstrap(t)
	t.Parallel()
	tt := newTenant(t)

	// Create a task but don't assign self as actor.
	doJSON(t, http.MethodPost, testServerURL+"/tasks", tt.AccessToken,
		map[string]any{"projectId": tt.ProjectPublicID, "title": "Unassigned task"}, nil)

	var myTasks struct {
		Total int64 `json:"total"`
		Tasks []struct {
			ID string `json:"id"`
		} `json:"tasks"`
	}
	doJSON(t, http.MethodGet, testServerURL+"/me/tasks",
		tt.AccessToken, nil, &myTasks)
	require.Equal(t, int64(0), myTasks.Total,
		"tasks where user is not actor must not appear")
}
