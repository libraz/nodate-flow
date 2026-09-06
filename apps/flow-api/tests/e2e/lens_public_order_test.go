package e2e

import (
	"encoding/json"
	"fmt"
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/libraz/nodate-flow/apps/flow-api/tests/helpers"
)

// A share page shows a capped, unpaginated slice of its author's list,
// so the order it applies is not presentation: it decides which rows the
// link holder is shown at all. A page ordered differently than the
// author's own list publishes a different set, and the rows it drops are
// ones the author never saw omitted.
//
// Both cases below assert the whole published sequence rather than
// filtering it down to the rows they created: the tenant is fresh and
// the lens is scoped to its project, so anything else on the page is
// itself the finding.

// orderedLensTask is one fixture row: the priority that would order it
// one way, and the manual weight that has to order it another.
type orderedLensTask struct {
	title      string
	priority   int32
	sortWeight int32
}

// createOrderedTasks creates every fixture row as a public task and
// applies the manual ordering in a single reorder call, which is the
// same endpoint a drag-and-drop drives.
func createOrderedTasks(t *testing.T, tt *helpers.TestTenant, specs []orderedLensTask) {
	t.Helper()
	items := make([]map[string]any, 0, len(specs))
	for _, spec := range specs {
		var out struct {
			ID string `json:"id"`
		}
		doJSON(t, http.MethodPost, testServerURL+"/tasks", tt.AccessToken, map[string]any{
			"projectId":  tt.ProjectPublicID,
			"title":      spec.title,
			"visibility": "public",
			"priority":   spec.priority,
		}, &out)
		require.NotEmpty(t, out.ID, "fixture: task create returned no id")
		items = append(items, map[string]any{"id": out.ID, "sortWeight": spec.sortWeight})
	}
	require.Len(t, items, len(specs), "fixture: every task must have been created")
	doJSON(t, http.MethodPost, testServerURL+"/tasks/reorder", tt.AccessToken, map[string]any{
		"projectId": tt.ProjectPublicID,
		"items":     items,
	}, nil)
}

// sharedTaskTitles fetches a share page without a bearer and returns the
// titles in the order the page published them.
func sharedTaskTitles(t *testing.T, token string) []string {
	t.Helper()
	var pub struct {
		Tasks []struct {
			Title string `json:"title"`
		} `json:"tasks"`
	}
	doJSON(t, http.MethodGet, testServerURL+"/public/lenses/"+token, "", nil, &pub)
	out := make([]string, 0, len(pub.Tasks))
	for _, task := range pub.Tasks {
		out = append(out, task.Title)
	}
	return out
}

// authorTaskTitles reads the author's own task list for the tenant's
// project and returns the titles in the order it published them. limit
// is the page size, which the schema caps at the same 200 rows a share
// page carries.
func authorTaskTitles(t *testing.T, tt *helpers.TestTenant, limit int) []string {
	t.Helper()
	var list struct {
		Tasks []struct {
			Title string `json:"title"`
		} `json:"tasks"`
	}
	doJSON(t, http.MethodGet,
		fmt.Sprintf("%s/tasks?projectId=%s&limit=%d", testServerURL, tt.ProjectPublicID, limit),
		tt.AccessToken, nil, &list)
	out := make([]string, 0, len(list.Tasks))
	for _, task := range list.Tasks {
		out = append(out, task.Title)
	}
	return out
}

// TestPublicLensPublishesTheOrderItsAuthorArranged pins the share page's
// order against the author's list on a set small enough to fit under the
// cap, where the disagreement is still only about sequence.
func TestPublicLensPublishesTheOrderItsAuthorArranged(t *testing.T) {
	bootstrap(t)
	t.Parallel()

	tt := newTenant(t)
	suffix := randomHex(4)

	// Priority and manual weight disagree on every pair, so a page
	// ordered by priority cannot reproduce this sequence by accident.
	specs := []orderedLensTask{
		{title: "arranged first " + suffix, priority: 0, sortWeight: 10},
		{title: "arranged second " + suffix, priority: 4, sortWeight: 20},
		{title: "arranged third " + suffix, priority: 1, sortWeight: 30},
		{title: "arranged fourth " + suffix, priority: 3, sortWeight: 40},
	}
	createOrderedTasks(t, tt, specs)

	_, token := publishLens(t, tt, json.RawMessage(`{"status":{"values":["open"]}}`))

	want := make([]string, 0, len(specs))
	for _, spec := range specs {
		want = append(want, spec.title)
	}

	require.Equal(t, want, sharedTaskTitles(t, token),
		"the share page must publish the order its author arranged")
	require.Equal(t, want, authorTaskTitles(t, tt, 200),
		"the author's own list must be the order the share page was compared against")
}

// TestPublicLensCapPublishesTheAuthorsLeadingRows is the case the order
// turns into a correctness question. The lens matches more rows than a
// share page carries and there is no next page, so whichever rows the
// order puts first are the entire published set — and the author has no
// way to see which of theirs were left out.
func TestPublicLensCapPublishesTheAuthorsLeadingRows(t *testing.T) {
	bootstrap(t)
	t.Parallel()

	tt := newTenant(t)
	suffix := randomHex(4)

	// One page's worth of rows plus a tail that must fall off it. The
	// rows the author put first carry the lowest priority and the ones
	// that must be dropped carry the highest, so an order led by
	// priority fills the page with exactly the rows that do not belong
	// on it.
	const (
		pageRows  = 200
		dropped   = 10
		totalRows = pageRows + dropped
	)
	specs := make([]orderedLensTask, 0, totalRows)
	for i := 0; i < totalRows; i++ {
		priority := int32(4)
		if i < dropped {
			priority = 0
		}
		specs = append(specs, orderedLensTask{
			title:      fmt.Sprintf("capped %03d %s", i, suffix),
			priority:   priority,
			sortWeight: int32(i),
		})
	}
	createOrderedTasks(t, tt, specs)

	_, token := publishLens(t, tt, json.RawMessage(`{"status":{"values":["open"]}}`))

	leading := authorTaskTitles(t, tt, pageRows)
	require.Len(t, leading, pageRows,
		"fixture: the author's first page must be full for the cap to bite")
	require.Equal(t, specs[0].title, leading[0],
		"fixture: the author's page must lead with the row they put first")

	shared := sharedTaskTitles(t, token)
	require.Len(t, shared, pageRows, "the share page publishes one page and no more")
	require.Equal(t, leading, shared,
		"a capped share page must publish the author's leading rows, not a different selection of them")
}
