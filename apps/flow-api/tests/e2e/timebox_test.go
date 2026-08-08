package e2e

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestTimeboxLifecycle verifies the full sprint lifecycle: create a
// timebox, transition its status, add/remove tasks, and view progress.
func TestTimeboxLifecycle(t *testing.T) {
	bootstrap(t)
	t.Parallel()
	tt := newTenant(t)

	wsURL := testServerURL + "/workspaces/" + tt.WorkspacePublicID
	tbBase := wsURL + "/timeboxes"

	// --- Create a timebox (sprint) ---
	var created struct {
		ID                 string `json:"id"`
		Name               string `json:"name"`
		StartsOn           string `json:"startsOn"`
		EndsOn             string `json:"endsOn"`
		Status             string `json:"status"`
		ProjectID          string `json:"projectId"`
		ProjectName        string `json:"projectName"`
		CreatorID          string `json:"creatorId"`
		CreatorDisplayName string `json:"creatorDisplayName"`
		CreatedAt          int64  `json:"createdAt"`
	}
	doJSON(t, http.MethodPost, tbBase, tt.AccessToken, map[string]any{
		"name":      "Sprint 1",
		"startsOn":  "2025-05-01",
		"endsOn":    "2025-05-14",
		"projectId": tt.ProjectPublicID,
	}, &created)
	require.NotEmpty(t, created.ID)
	require.Equal(t, "Sprint 1", created.Name)
	require.Equal(t, "2025-05-01", created.StartsOn)
	require.Equal(t, "planned", created.Status, "new timebox defaults to planned")

	// The create response is the only one a client has before it can
	// render the new row, so it has to carry the same fields Get does.
	// It used to be hand-built from the request body, which left the
	// creator and the project name blank on that one path.
	require.NotEmpty(t, created.CreatorID, "create response must identify the creator")
	require.NotEmpty(t, created.CreatorDisplayName, "create response must name the creator")
	require.Equal(t, tt.ProjectPublicID, created.ProjectID)
	require.NotEmpty(t, created.ProjectName, "create response must carry the project name")
	require.NotZero(t, created.CreatedAt)

	var fetched struct {
		CreatorID          string `json:"creatorId"`
		CreatorDisplayName string `json:"creatorDisplayName"`
		ProjectName        string `json:"projectName"`
	}
	doJSON(t, http.MethodGet, tbBase+"/"+created.ID, tt.AccessToken, nil, &fetched)
	require.Equal(t, fetched.CreatorID, created.CreatorID)
	require.Equal(t, fetched.CreatorDisplayName, created.CreatorDisplayName)
	require.Equal(t, fetched.ProjectName, created.ProjectName)

	// --- List includes the timebox ---
	var list struct {
		Total     int64 `json:"total"`
		Timeboxes []struct {
			ID string `json:"id"`
		} `json:"timeboxes"`
	}
	doJSON(t, http.MethodGet, tbBase, tt.AccessToken, nil, &list)
	require.GreaterOrEqual(t, list.Total, int64(1))

	// --- Update name and dates ---
	var patched struct {
		Name   string `json:"name"`
		EndsOn string `json:"endsOn"`
	}
	doJSON(t, http.MethodPatch, tbBase+"/"+created.ID, tt.AccessToken,
		map[string]any{"name": "Sprint 1 (extended)", "endsOn": "2025-05-21"}, &patched)
	require.Equal(t, "Sprint 1 (extended)", patched.Name)
	require.Equal(t, "2025-05-21", patched.EndsOn)

	// --- Transition: planned → active ---
	doJSON(t, http.MethodPost, tbBase+"/"+created.ID+"/status", tt.AccessToken,
		map[string]any{"status": "active"}, nil)

	var active struct {
		Status string `json:"status"`
	}
	doJSON(t, http.MethodGet, tbBase+"/"+created.ID, tt.AccessToken, nil, &active)
	require.Equal(t, "active", active.Status)

	// --- Create a task and add it to the timebox ---
	var task struct {
		ID string `json:"id"`
	}
	doJSON(t, http.MethodPost, testServerURL+"/tasks",
		tt.AccessToken, map[string]any{
			"projectId": tt.ProjectPublicID,
			"title":     "Implement feature X",
		}, &task)
	require.NotEmpty(t, task.ID)

	doJSON(t, http.MethodPost, tbBase+"/"+created.ID+"/tasks", tt.AccessToken,
		map[string]any{"taskId": task.ID}, nil)

	// --- List tasks in timebox shows the task ---
	var tbTasks struct {
		Total          int64 `json:"total"`
		TotalTasks     int64 `json:"totalTasks"`
		CompletedTasks int64 `json:"completedTasks"`
		Tasks          []struct {
			ID    string `json:"id"`
			Title string `json:"title"`
		} `json:"tasks"`
	}
	doJSON(t, http.MethodGet, tbBase+"/"+created.ID+"/tasks",
		tt.AccessToken, nil, &tbTasks)
	require.Equal(t, int64(1), tbTasks.TotalTasks)
	require.Equal(t, int64(0), tbTasks.CompletedTasks)

	// --- Remove task from timebox ---
	status, _ := doJSONStatus(t, http.MethodDelete,
		tbBase+"/"+created.ID+"/tasks/"+task.ID, tt.AccessToken, nil)
	require.Equal(t, http.StatusOK, status)

	// Task list should now be empty.
	var tbTasksAfter struct {
		TotalTasks int64 `json:"totalTasks"`
	}
	doJSON(t, http.MethodGet, tbBase+"/"+created.ID+"/tasks",
		tt.AccessToken, nil, &tbTasksAfter)
	require.Equal(t, int64(0), tbTasksAfter.TotalTasks)

	// --- Transition: active → completed ---
	doJSON(t, http.MethodPost, tbBase+"/"+created.ID+"/status", tt.AccessToken,
		map[string]any{"status": "completed"}, nil)

	var completed struct {
		Status string `json:"status"`
	}
	doJSON(t, http.MethodGet, tbBase+"/"+created.ID, tt.AccessToken, nil, &completed)
	require.Equal(t, "completed", completed.Status)

	// --- Delete (soft) ---
	status, _ = doJSONStatus(t, http.MethodDelete, tbBase+"/"+created.ID,
		tt.AccessToken, nil)
	require.Equal(t, http.StatusOK, status)
}
