package e2e

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestTimelineAggregation exercises both task-scoped and workspace-
// scoped timeline endpoints after a task, comment and constraint have
// been created.
func TestTimelineAggregation(t *testing.T) {
	bootstrap(t)
	t.Parallel()

	tt := newTenant(t)

	// Create a task.
	var task struct {
		ID string `json:"id"`
	}
	doJSON(t, http.MethodPost, testServerURL+"/tasks", tt.AccessToken, map[string]any{
		"projectId": tt.ProjectPublicID,
		"title":     "Timeline task",
	}, &task)
	require.NotEmpty(t, task.ID)

	// Add a comment.
	doJSON(t, http.MethodPost, testServerURL+"/tasks/"+task.ID+"/comments",
		tt.AccessToken, map[string]any{"body": "first"}, nil)

	// Add a constraint.
	doJSON(t, http.MethodPost, testServerURL+"/tasks/"+task.ID+"/constraints",
		tt.AccessToken, map[string]any{
			"kind":       "approval",
			"expression": `{"op":"approval.granted","arg":"lead"}`,
		}, nil)

	// Task-scoped timeline must include all three events.
	var taskTL struct {
		Total  int64 `json:"total"`
		Events []struct {
			ID   string `json:"id"`
			Type string `json:"type"`
		} `json:"events"`
	}
	doJSON(t, http.MethodGet, testServerURL+"/tasks/"+task.ID+"/timeline",
		tt.AccessToken, nil, &taskTL)
	require.GreaterOrEqual(t, taskTL.Total, int64(3),
		"task timeline must include create, comment and constraint events")

	// Workspace-scoped timeline must also see entries.
	var wsTL struct {
		Total  int64 `json:"total"`
		Events []struct {
			Type string `json:"type"`
		} `json:"events"`
	}
	doJSON(t, http.MethodGet, testServerURL+"/workspaces/"+tt.WorkspacePublicID+"/timeline",
		tt.AccessToken, nil, &wsTL)
	require.GreaterOrEqual(t, wsTL.Total, int64(3), "workspace timeline must include entries")
}

// TestTimelineTotalIsIndependentOfPage holds that the reported total
// counts the whole filtered scope, not the page: a one-row page, a page
// past the end and an unpaged read all report the same total.
func TestTimelineTotalIsIndependentOfPage(t *testing.T) {
	bootstrap(t)
	t.Parallel()

	tt := newTenant(t)

	var task struct {
		ID string `json:"id"`
	}
	doJSON(t, http.MethodPost, testServerURL+"/tasks", tt.AccessToken, map[string]any{
		"projectId": tt.ProjectPublicID,
		"title":     "Paged timeline task",
	}, &task)
	require.NotEmpty(t, task.ID)
	doJSON(t, http.MethodPost, testServerURL+"/tasks/"+task.ID+"/comments",
		tt.AccessToken, map[string]any{"body": "first"}, nil)

	type page struct {
		Total  int64 `json:"total"`
		Events []struct {
			ID string `json:"id"`
		} `json:"events"`
	}
	get := func(query string) page {
		var p page
		doJSON(t, http.MethodGet,
			testServerURL+"/workspaces/"+tt.WorkspacePublicID+"/timeline"+query,
			tt.AccessToken, nil, &p)
		return p
	}

	all := get("")
	require.GreaterOrEqual(t, all.Total, int64(2))

	first := get("?limit=1")
	require.Len(t, first.Events, 1)
	require.Equal(t, all.Total, first.Total)

	past := get("?limit=1&offset=1000")
	require.Empty(t, past.Events)
	require.Equal(t, all.Total, past.Total, "a page past the end still reports the scope total")
}
