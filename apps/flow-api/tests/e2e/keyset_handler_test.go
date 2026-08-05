package e2e

import (
	"net/http"
	"net/url"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/libraz/nodate-flow/apps/flow-api/tests/helpers"
)

// keysetTaskListItem is a minimal projection of the GET /tasks list
// response used by the keyset handler test. Only the fields we assert
// on are present so the local DTO does not drift with shape changes
// to TaskListItem.
type keysetTaskListItem struct {
	ID    string `json:"id"`
	Title string `json:"title"`
}

// keysetTaskListResponse mirrors the relevant slice of ListTasksBody.
// `nextCursor` is *string in the wire body so a JSON null deserialises
// to a nil pointer here, which is how we detect the terminal page.
type keysetTaskListResponse struct {
	Total      int64                `json:"total"`
	Tasks      []keysetTaskListItem `json:"tasks"`
	NextCursor *string              `json:"nextCursor"`
}

// TestKeysetHandlerListTasksWorkspace exercises the cursor query
// parameter on GET /tasks at the HTTP layer (not the SQL layer like
// TestKeysetPaginationListTasksForWorkspace does). The contract under
// test is the additive boundary established in Phase 2 of the keyset
// rollout: callers that pass `cursor=` get the keyset path and a
// `nextCursor` in the response; callers that omit it keep getting
// the OFFSET path. The two paths must enumerate the same set of rows
// when run over identical input.
//
// The test seeds 30 tasks via POST /tasks and then:
//
//  1. Pages forward with cursor + limit=10 until nextCursor goes nil,
//     asserting no duplicates, no skips, and the page count.
//  2. Pulls the same workspace via plain offset/limit and asserts the
//     full set of public ids matches the keyset traversal.
//
// Single tenant, parallel-safe via createTestTenant + PurgeWorkspace.
// Gated by NF_TEST_INTEGRATION=1 like every other test in the package.
func TestKeysetHandlerListTasksWorkspace(t *testing.T) {
	bootstrap(t)
	t.Parallel()

	const totalRows = 30
	const pageLimit = 10
	const expectedPages = totalRows / pageLimit

	tt := newTenant(t)
	t.Cleanup(func() { helpers.PurgeWorkspace(t, testDB, tt.WorkspacePublicID) })

	// Seed 30 tasks via the public API. Identical title + priority so
	// the only thing distinguishing rows is their server-assigned
	// (created_at, public_id) tuple — exactly the cursor key.
	for i := 0; i < totalRows; i++ {
		var created struct {
			ID string `json:"id"`
		}
		doJSON(t, http.MethodPost, testServerURL+"/tasks", tt.AccessToken, map[string]any{
			"projectId": tt.ProjectPublicID,
			"title":     "keyset handler task",
			"priority":  0,
		}, &created)
		require.NotEmpty(t, created.ID)
	}

	listURL := func(cursor string) string {
		v := url.Values{}
		v.Set("workspaceId", tt.WorkspacePublicID)
		v.Set("limit", "10")
		if cursor != "" {
			v.Set("cursor", cursor)
		}
		return testServerURL + "/tasks?" + v.Encode()
	}

	// 1. Walk the keyset path.
	seen := make(map[string]struct{}, totalRows)
	cursor := ""
	pages := 0
	for {
		var resp keysetTaskListResponse
		doJSON(t, http.MethodGet, listURL(cursor), tt.AccessToken, nil, &resp)
		pages++

		require.LessOrEqualf(t, len(resp.Tasks), pageLimit,
			"page %d returned %d rows (limit was %d)", pages, len(resp.Tasks), pageLimit)

		for _, row := range resp.Tasks {
			_, dup := seen[row.ID]
			require.Falsef(t, dup, "duplicate id %s on page %d", row.ID, pages)
			seen[row.ID] = struct{}{}
		}

		if resp.NextCursor == nil || *resp.NextCursor == "" {
			break
		}
		cursor = *resp.NextCursor

		// Defensive cap: if a regression returned the same page
		// forever the test would otherwise hang the whole package.
		require.LessOrEqualf(t, pages, expectedPages+2,
			"paged %d times, expected at most %d", pages, expectedPages+2)
	}

	require.Equalf(t, expectedPages, pages,
		"expected exactly %d pages (totalRows=%d, limit=%d), got %d",
		expectedPages, totalRows, pageLimit, pages)
	require.Lenf(t, seen, totalRows,
		"keyset traversal saw %d unique ids, expected %d", len(seen), totalRows)

	// 2. Walk the OFFSET path over the same data and assert the union
	//    of ids matches the keyset traversal exactly. limit=200 so a
	//    single round-trip covers the seeded 30.
	offsetURL := testServerURL + "/tasks?" + url.Values{
		"workspaceId": []string{tt.WorkspacePublicID},
		"limit":       []string{"200"},
	}.Encode()
	var offsetResp keysetTaskListResponse
	doJSON(t, http.MethodGet, offsetURL, tt.AccessToken, nil, &offsetResp)
	require.Equalf(t, int64(totalRows), offsetResp.Total,
		"offset path total = %d, expected %d", offsetResp.Total, totalRows)
	require.Lenf(t, offsetResp.Tasks, totalRows,
		"offset path returned %d rows, expected %d", len(offsetResp.Tasks), totalRows)
	require.Nil(t, offsetResp.NextCursor,
		"offset path must not emit nextCursor (cursor was not requested)")

	offsetSet := make(map[string]struct{}, totalRows)
	for _, r := range offsetResp.Tasks {
		offsetSet[r.ID] = struct{}{}
	}
	for id := range seen {
		_, ok := offsetSet[id]
		require.Truef(t, ok, "id %s appeared in keyset path but not in offset path", id)
	}
	for id := range offsetSet {
		_, ok := seen[id]
		require.Truef(t, ok, "id %s appeared in offset path but not in keyset path", id)
	}
}

// TestKeysetHandlerInvalidCursor verifies that a malformed cursor
// surfaces as a 4xx (specifically WS.VALIDATION.QUERY_FIELD_INVALID via
// apierror) rather than leaking a 500 / database error. The handler
// path is List → DecodeCursor → httpErr; this test guards against a
// future refactor that accidentally swallows the decode error.
func TestKeysetHandlerInvalidCursor(t *testing.T) {
	bootstrap(t)
	t.Parallel()

	tt := newTenant(t)
	t.Cleanup(func() { helpers.PurgeWorkspace(t, testDB, tt.WorkspacePublicID) })

	listURL := testServerURL + "/tasks?" + url.Values{
		"workspaceId": []string{tt.WorkspacePublicID},
		"limit":       []string{"10"},
		"cursor":      []string{"@@@not-a-real-cursor@@@"},
	}.Encode()

	status, _ := doJSONStatus(t, http.MethodGet, listURL, tt.AccessToken, nil)
	require.GreaterOrEqualf(t, status, 400, "invalid cursor must be 4xx, got %d", status)
	require.Lessf(t, status, 500, "invalid cursor must NOT bubble as 5xx, got %d", status)
}
