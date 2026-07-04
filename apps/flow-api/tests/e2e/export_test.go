package e2e

import (
	"context"
	"net/http"
	"strings"
	"testing"
	"time"

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

// TestExportTasksExcludesDisabledProjectTasks verifies that tasks whose
// parent project is disabled (enabled=FALSE) are excluded from the
// workspace export, even when the tasks themselves still claim
// enabled=TRUE. Regression for the audit fix that added
// `AND p.enabled = TRUE` to the projects INNER JOIN in
// ExportTasksForWorkspace.
//
// We bypass the project-disable handler (which cascades enabled=FALSE
// onto child tasks) by issuing a direct UPDATE so the test can simulate
// the inconsistent state the JOIN guard now defends against.
func TestExportTasksExcludesDisabledProjectTasks(t *testing.T) {
	bootstrap(t)
	t.Parallel()
	tt := newTenant(t)

	wsURL := testServerURL + "/workspaces/" + tt.WorkspacePublicID

	// Create a second project alongside the tenant default project.
	var disabledProj struct {
		ID string `json:"id"`
	}
	doJSON(t, http.MethodPost, wsURL+"/projects", tt.AccessToken,
		map[string]any{
			"slug": "exp-disabled-" + randomHex(4),
			"name": "Project To Disable",
		}, &disabledProj)
	require.NotEmpty(t, disabledProj.ID)

	// Create one task in each project.
	var visibleTask, hiddenTask struct {
		ID string `json:"id"`
	}
	doJSON(t, http.MethodPost, testServerURL+"/tasks", tt.AccessToken,
		map[string]any{
			"projectId": tt.ProjectPublicID,
			"title":     "Visible task in enabled project",
		}, &visibleTask)
	doJSON(t, http.MethodPost, testServerURL+"/tasks", tt.AccessToken,
		map[string]any{
			"projectId": disabledProj.ID,
			"title":     "Hidden task in disabled project",
		}, &hiddenTask)
	require.NotEmpty(t, visibleTask.ID)
	require.NotEmpty(t, hiddenTask.ID)

	// Disable the second project directly via SQL — bypassing the
	// handler's child-task cascade so the task row stays enabled. This
	// reproduces the leak scenario the JOIN guard now blocks.
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_, err := testDB.ExecContext(ctx,
		`UPDATE projects SET enabled = FALSE
		 WHERE public_id = UUID_TO_BIN(?, 0)`, disabledProj.ID)
	require.NoError(t, err, "soft-disable of project must succeed")

	// Export and assert only the visible task is returned.
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

	titles := make([]string, 0, len(exported.Tasks))
	for _, x := range exported.Tasks {
		titles = append(titles, x.Title)
	}
	require.Contains(t, titles, "Visible task in enabled project")
	require.NotContains(t, titles, "Hidden task in disabled project",
		"tasks under disabled projects must not appear in workspace export")
}

func TestExportTasksAppliesTaskVisibilityFilter(t *testing.T) {
	bootstrap(t)
	t.Parallel()
	owner := newTenant(t)
	member := seedWorkspaceMemberWithoutProjectRole(t, owner)

	needle := "export visibility " + randomHex(6)
	publicTitle := needle + " public"
	projectTitle := needle + " project"
	privateTitle := needle + " private"

	var publicTask, projectTask, privateTask struct {
		ID string `json:"id"`
	}
	doJSON(t, http.MethodPost, testServerURL+"/tasks", owner.AccessToken,
		map[string]any{
			"projectId":  owner.ProjectPublicID,
			"title":      publicTitle,
			"visibility": "public",
		}, &publicTask)
	doJSON(t, http.MethodPost, testServerURL+"/tasks", owner.AccessToken,
		map[string]any{
			"projectId":  owner.ProjectPublicID,
			"title":      projectTitle,
			"visibility": "project",
		}, &projectTask)
	doJSON(t, http.MethodPost, testServerURL+"/tasks", owner.AccessToken,
		map[string]any{
			"projectId":  owner.ProjectPublicID,
			"title":      privateTitle,
			"visibility": "private",
		}, &privateTask)
	require.NotEmpty(t, publicTask.ID)
	require.NotEmpty(t, projectTask.ID)
	require.NotEmpty(t, privateTask.ID)

	wsURL := testServerURL + "/workspaces/" + owner.WorkspacePublicID
	var exported struct {
		Tasks []struct {
			ID    string `json:"id"`
			Title string `json:"title"`
		} `json:"tasks"`
	}
	doJSON(t, http.MethodGet, wsURL+"/export/tasks?format=json&limit=100",
		member.AccessToken, nil, &exported)

	jsonTitles := make([]string, 0, len(exported.Tasks))
	for _, task := range exported.Tasks {
		jsonTitles = append(jsonTitles, task.Title)
	}
	require.Contains(t, jsonTitles, publicTitle,
		"workspace member should export public tasks")
	require.NotContains(t, jsonTitles, projectTitle,
		"workspace member without project membership must not export project-visibility tasks")
	require.NotContains(t, jsonTitles, privateTitle,
		"workspace member without actor/creator access must not export private tasks")

	status, body := doJSONStatus(t, http.MethodGet, wsURL+"/export/tasks.csv?limit=100",
		member.AccessToken, nil)
	require.Equal(t, http.StatusOK, status)
	csv := string(body)
	require.Contains(t, csv, publicTitle)
	require.NotContains(t, csv, projectTitle)
	require.NotContains(t, csv, privateTitle)
}

func TestMCPExportTasksAppliesTaskVisibilityFilter(t *testing.T) {
	bootstrap(t)
	t.Parallel()
	owner := newTenant(t)
	member := seedWorkspaceMemberWithoutProjectRole(t, owner)
	tok := mintMCPToken(t, member.AccessToken, owner.WorkspacePublicID,
		"export-visibility", []string{"read:workspace"})

	needle := "mcp export visibility " + randomHex(6)
	publicTitle := needle + " public"
	projectTitle := needle + " project"
	privateTitle := needle + " private"

	var publicTask, projectTask, privateTask struct {
		ID string `json:"id"`
	}
	doJSON(t, http.MethodPost, testServerURL+"/tasks", owner.AccessToken,
		map[string]any{
			"projectId":  owner.ProjectPublicID,
			"title":      publicTitle,
			"visibility": "public",
		}, &publicTask)
	doJSON(t, http.MethodPost, testServerURL+"/tasks", owner.AccessToken,
		map[string]any{
			"projectId":  owner.ProjectPublicID,
			"title":      projectTitle,
			"visibility": "project",
		}, &projectTask)
	doJSON(t, http.MethodPost, testServerURL+"/tasks", owner.AccessToken,
		map[string]any{
			"projectId":  owner.ProjectPublicID,
			"title":      privateTitle,
			"visibility": "private",
		}, &privateTask)
	require.NotEmpty(t, publicTask.ID)
	require.NotEmpty(t, projectTask.ID)
	require.NotEmpty(t, privateTask.ID)

	body := mcpCall(t, tok, "tools/call", map[string]any{
		"name": "export_tasks",
		"arguments": map[string]any{
			"limit": 100,
		},
	})
	result := mcpToolTextJSON[struct {
		Tasks []struct {
			ID    string `json:"id"`
			Title string `json:"title"`
		} `json:"tasks"`
	}](t, body)

	titles := make([]string, 0, len(result.Tasks))
	for _, task := range result.Tasks {
		titles = append(titles, task.Title)
	}
	require.Contains(t, titles, publicTitle,
		"workspace member should export public tasks over MCP")
	require.NotContains(t, titles, projectTitle,
		"MCP export must hide project-visibility tasks without project membership")
	require.NotContains(t, titles, privateTitle,
		"MCP export must hide private tasks without actor/creator access")
}
