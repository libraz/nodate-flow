package e2e

import (
	"net/http"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestExportTasksJSON verifies that a member can export workspace tasks
// as JSON and the output contains the expected tasks.
func TestExportTasksJSON(t *testing.T) {
	bootstrap(t)
	t.Parallel()
	tt := newTenant(t)

	wsURL := testServerURL + "/workspaces/" + tt.WorkspacePublicID

	// Create a few tasks to export.
	for _, title := range []string{"Task Alpha", "Task Beta"} {
		doJSON(t, http.MethodPost, testServerURL+"/tasks",
			tt.AccessToken, map[string]any{
				"projectId": tt.ProjectPublicID,
				"title":     title,
			}, nil)
	}

	// Export as JSON.
	var exported struct {
		Format string `json:"format"`
		Count  int    `json:"count"`
		Tasks  []struct {
			ID    string `json:"id"`
			Title string `json:"title"`
		} `json:"tasks"`
	}
	doJSON(t, http.MethodGet, wsURL+"/export/tasks?format=json",
		tt.AccessToken, nil, &exported)
	require.Equal(t, "json", exported.Format)
	require.GreaterOrEqual(t, exported.Count, 2, "at least the two created tasks")
	require.GreaterOrEqual(t, len(exported.Tasks), 2)
}

// TestExportTasksCSV verifies that a member can export workspace tasks
// as CSV and receives valid comma-separated output.
func TestExportTasksCSV(t *testing.T) {
	bootstrap(t)
	t.Parallel()
	tt := newTenant(t)

	wsURL := testServerURL + "/workspaces/" + tt.WorkspacePublicID

	// Create a task so the export is non-empty.
	doJSON(t, http.MethodPost, testServerURL+"/tasks",
		tt.AccessToken, map[string]any{
			"projectId": tt.ProjectPublicID,
			"title":     "CSV Test Task",
		}, nil)

	// Export as CSV (raw response — may not be JSON).
	status, body := doJSONStatus(t, http.MethodGet, wsURL+"/export/tasks.csv",
		tt.AccessToken, nil)
	require.Equal(t, http.StatusOK, status)
	require.True(t, len(body) > 0, "CSV body must not be empty")

	// CSV should contain a header row and the task title.
	csv := string(body)
	require.True(t, strings.Contains(csv, "title") || strings.Contains(csv, "Title"),
		"CSV must contain a header row")
	require.Contains(t, csv, "CSV Test Task")
}
