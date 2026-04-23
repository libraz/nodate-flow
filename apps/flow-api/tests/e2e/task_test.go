package e2e

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestTaskLifecycle exercises create, list, get, patch, constraint,
// dependency, actor and disable on a single task, and verifies the
// events table gets rows along the way.
func TestTaskLifecycle(t *testing.T) {
	bootstrap(t)
	t.Parallel()

	tt := newTenant(t)

	// Create the primary task.
	var task struct {
		ID           string `json:"id"`
		ProjectID    string `json:"projectId"`
		Title        string `json:"title"`
		DerivedState string `json:"derivedState"`
	}
	doJSON(t, http.MethodPost, testServerURL+"/tasks", tt.AccessToken, map[string]any{
		"projectId": tt.ProjectPublicID,
		"title":     "Initial task",
		"priority":  2,
	}, &task)
	require.NotEmpty(t, task.ID)
	require.Equal(t, tt.ProjectPublicID, task.ProjectID)
	require.Equal(t, "Initial task", task.Title)

	// List tasks for the project.
	var list struct {
		Total int64 `json:"total"`
		Tasks []struct {
			ID    string `json:"id"`
			Title string `json:"title"`
		} `json:"tasks"`
	}
	doJSON(t, http.MethodGet,
		testServerURL+"/tasks?projectId="+tt.ProjectPublicID,
		tt.AccessToken, nil, &list)
	require.GreaterOrEqual(t, list.Total, int64(1))

	// Get the task back by id.
	var got struct {
		ID    string `json:"id"`
		Title string `json:"title"`
	}
	doJSON(t, http.MethodGet, testServerURL+"/tasks/"+task.ID, tt.AccessToken, nil, &got)
	require.Equal(t, task.ID, got.ID)

	// Patch title.
	doJSON(t, http.MethodPatch, testServerURL+"/tasks/"+task.ID,
		tt.AccessToken, map[string]any{"title": "Updated task"}, &got)
	require.Equal(t, "Updated task", got.Title)

	// Add a constraint.
	var constraint struct {
		ID   string `json:"id"`
		Kind string `json:"kind"`
	}
	doJSON(t, http.MethodPost, testServerURL+"/tasks/"+task.ID+"/constraints",
		tt.AccessToken, map[string]any{
			"kind":       "deadline",
			"expression": "due_on<=2026-12-31",
		}, &constraint)
	require.NotEmpty(t, constraint.ID)
	require.Equal(t, "deadline", constraint.Kind)

	// Create a second task to depend on.
	var other struct {
		ID string `json:"id"`
	}
	doJSON(t, http.MethodPost, testServerURL+"/tasks", tt.AccessToken, map[string]any{
		"projectId": tt.ProjectPublicID,
		"title":     "Dependency target",
	}, &other)
	require.NotEmpty(t, other.ID)

	// Add a dependency edge.
	var dep struct {
		ID       string `json:"id"`
		Kind     string `json:"kind"`
		ToTaskID string `json:"toTaskId"`
	}
	doJSON(t, http.MethodPost, testServerURL+"/tasks/"+task.ID+"/dependencies",
		tt.AccessToken, map[string]any{
			"toTaskId": other.ID,
			"kind":     "blocks",
		}, &dep)
	require.Equal(t, other.ID, dep.ToTaskID)
	require.Equal(t, "blocks", dep.Kind)

	// Add the tenant user as an actor under a non-assignee role. The
	// creator is already auto-attached as the default `assignee` by
	// POST /tasks, so re-adding the same (task, user, assignee) tuple
	// would hit the UNIQUE key uniq_task_actors_task_id_user_id_role.
	var actor struct {
		ID     string `json:"id"`
		UserID string `json:"userId"`
		Role   string `json:"role"`
	}
	doJSON(t, http.MethodPost, testServerURL+"/tasks/"+task.ID+"/actors",
		tt.AccessToken, map[string]any{
			"userId": tt.UserPublicID,
			"role":   "reviewer",
		}, &actor)
	require.Equal(t, tt.UserPublicID, actor.UserID)
	require.Equal(t, "reviewer", actor.Role)

	// Disable the task.
	status, _ := doJSONStatus(t, http.MethodDelete, testServerURL+"/tasks/"+task.ID, tt.AccessToken, nil)
	require.Equal(t, http.StatusOK, status)

	// Verify events were emitted for this workspace. We run a bounded
	// query directly against the container DB because there is no
	// workspace-events listing API other than timeline, which is
	// exercised separately.
	var events int
	err := testDB.QueryRow(
		`SELECT COUNT(*) FROM events
		 WHERE workspace_id = (SELECT id FROM workspaces WHERE public_id = UUID_TO_BIN(?, 0))`,
		tt.WorkspacePublicID).Scan(&events)
	require.NoError(t, err)
	require.Greater(t, events, 0, "task lifecycle must emit events")
}
