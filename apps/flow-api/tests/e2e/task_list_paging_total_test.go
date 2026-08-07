package e2e

import (
	"fmt"
	"net/http"
	"net/url"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/libraz/nodate-flow/apps/flow-api/tests/helpers"
)

// taskListPage is the slice of GET /tasks this file asserts on.
type taskListPage struct {
	Total      int64   `json:"total"`
	NextCursor *string `json:"nextCursor"`
	Tasks      []struct {
		ID       string `json:"id"`
		Title    string `json:"title"`
		Priority int32  `json:"priority"`
	} `json:"tasks"`
}

// fetchTaskListPage issues GET /tasks with the given query string.
func fetchTaskListPage(t *testing.T, token, query string) taskListPage {
	t.Helper()
	var page taskListPage
	doJSON(t, http.MethodGet, testServerURL+"/tasks?"+query, token, nil, &page)
	return page
}

// TestTaskListTotalSurvivesPageSplit pins the pagination contract of
// GET /tasks against the two paths that now produce `total`.
//
// The list query no longer carries COUNT(*) OVER(): a full page asks a
// separate COUNT, and a short page derives the total from its own
// offset because it is by definition the last one. Those are two
// different code paths reporting the same number, and only a test that
// walks past the boundary in both directions can tell them apart.
//
// Everything is scoped to this tenant's own project, so the assertions
// hold no matter what else the shared instance contains.
func TestTaskListTotalSurvivesPageSplit(t *testing.T) {
	bootstrap(t)
	t.Parallel()

	const taskCount = 7
	const pageSize = 3

	tt := newTenant(t)
	t.Cleanup(func() { helpers.PurgeWorkspace(t, testDB, tt.WorkspacePublicID) })

	for i := 0; i < taskCount; i++ {
		var created struct {
			ID string `json:"id"`
		}
		doJSON(t, http.MethodPost, testServerURL+"/tasks", tt.AccessToken, map[string]any{
			"projectId": tt.ProjectPublicID,
			"title":     fmt.Sprintf("paging task %d", i),
			"priority":  i % 5,
		}, &created)
		require.NotEmpty(t, created.ID)
	}

	base := "projectId=" + url.QueryEscape(tt.ProjectPublicID)

	// Reference ordering: one request large enough to hold everything.
	whole := fetchTaskListPage(t, tt.AccessToken, base+"&limit=200&offset=0")
	require.Len(t, whole.Tasks, taskCount)
	require.EqualValues(t, taskCount, whole.Total,
		"a single short page must report the true total from its own offset")

	// Full pages: offset 0 and 3 both come back at the limit, so both
	// take the COUNT branch.
	var paged []string
	for offset := 0; offset < taskCount; offset += pageSize {
		page := fetchTaskListPage(t, tt.AccessToken,
			fmt.Sprintf("%s&limit=%d&offset=%d", base, pageSize, offset))
		require.EqualValues(t, taskCount, page.Total,
			"total must stay the full count at offset %d", offset)
		for _, row := range page.Tasks {
			paged = append(paged, row.ID)
		}
	}

	// The last page holds a single row: total there comes from
	// offset+len, not from a COUNT, and it must agree.
	tail := fetchTaskListPage(t, tt.AccessToken,
		fmt.Sprintf("%s&limit=%d&offset=%d", base, pageSize, taskCount-1))
	require.Len(t, tail.Tasks, 1)
	require.EqualValues(t, taskCount, tail.Total)

	// Reading past the end yields nothing, and the response carries no
	// cursor to follow.
	beyond := fetchTaskListPage(t, tt.AccessToken,
		fmt.Sprintf("%s&limit=%d&offset=%d", base, pageSize, taskCount))
	require.Empty(t, beyond.Tasks)
	require.Nil(t, beyond.NextCursor)

	// Paging through must reproduce the single-request order exactly.
	want := make([]string, 0, taskCount)
	for _, row := range whole.Tasks {
		want = append(want, row.ID)
	}
	require.Equal(t, want, paged, "page boundaries must not reorder or drop rows")
}

// TestTaskListPriorityFilterIsServerSide pins `priority` as a real
// query parameter of GET /tasks.
//
// The web client used to fetch page after page and drop non-matching
// rows itself, so a rare priority in a large project cost one request
// per page it had to skip. The filter has to narrow the result set on
// the server for that loop to be removable, and `total` has to count
// the filtered set -- a total describing the unfiltered list is what
// kept the old loop walking.
func TestTaskListPriorityFilterIsServerSide(t *testing.T) {
	bootstrap(t)
	t.Parallel()

	tt := newTenant(t)
	t.Cleanup(func() { helpers.PurgeWorkspace(t, testDB, tt.WorkspacePublicID) })

	// 4 tasks at priority 0, 2 at priority 4, 1 at priority 2.
	priorities := []int32{0, 0, 0, 0, 4, 4, 2}
	for i, p := range priorities {
		var created struct {
			ID string `json:"id"`
		}
		doJSON(t, http.MethodPost, testServerURL+"/tasks", tt.AccessToken, map[string]any{
			"projectId": tt.ProjectPublicID,
			"title":     fmt.Sprintf("priority task %d", i),
			"priority":  p,
		}, &created)
		require.NotEmpty(t, created.ID)
	}

	base := "projectId=" + url.QueryEscape(tt.ProjectPublicID)

	urgent := fetchTaskListPage(t, tt.AccessToken, base+"&priority=4")
	require.Len(t, urgent.Tasks, 2, "only the priority-4 rows may come back")
	require.EqualValues(t, 2, urgent.Total,
		"total must count the filtered set, not the whole project")
	for _, row := range urgent.Tasks {
		require.EqualValues(t, 4, row.Priority)
	}

	// Multiple values OR together, and the wire form that carries them
	// is comma-separated -- the spec marks these parameters
	// explode:false and the server reads only the first occurrence of a
	// repeated one. A client that sends the repeated form silently
	// filters by one value, so both forms are pinned here rather than
	// left to whichever the HTTP client happens to emit.
	commaForm := fetchTaskListPage(t, tt.AccessToken, base+"&priority=4,2")
	require.Len(t, commaForm.Tasks, 3, "comma-separated values must all apply")
	require.EqualValues(t, 3, commaForm.Total)

	repeatForm := fetchTaskListPage(t, tt.AccessToken, base+"&priority=4&priority=2")
	require.Len(t, repeatForm.Tasks, 2,
		"a repeated parameter reaches the server as its first value only; "+
			"clients must send the comma form")

	// A priority nothing carries matches nothing -- and must not fall
	// back to the unfiltered list.
	none := fetchTaskListPage(t, tt.AccessToken, base+"&priority=3")
	require.Empty(t, none.Tasks)

	// Combining priority with another filter narrows further rather
	// than replacing it.
	combined := fetchTaskListPage(t, tt.AccessToken, base+"&priority=4&q=priority+task+5")
	require.Len(t, combined.Tasks, 1)
	require.EqualValues(t, 4, combined.Tasks[0].Priority)

	// Without the filter every row is still there.
	all := fetchTaskListPage(t, tt.AccessToken, base+"&limit=200")
	require.Len(t, all.Tasks, len(priorities))
}
