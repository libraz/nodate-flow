package e2e

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestInboxTotalCountsEveryItemNotThePage holds the inbox total to the
// whole list on both feeds, including a page past the last item, where a
// total read off the first row of the page had nothing to read.
func TestInboxTotalCountsEveryItemNotThePage(t *testing.T) {
	bootstrap(t)
	t.Parallel()

	tt := newTenant(t)
	const items = 3
	for i := 0; i < items; i++ {
		doJSON(t, http.MethodPost, testServerURL+"/signals", tt.AccessToken, map[string]any{
			"workspaceId": tt.WorkspacePublicID,
			"source":      "manual",
			"kind":        "manual",
		}, nil)
	}

	type page struct {
		Total int64 `json:"total"`
		Items []struct {
			ID string `json:"id"`
		} `json:"items"`
	}
	var first page
	doJSON(t, http.MethodGet, testServerURL+"/inbox?workspaceId="+tt.WorkspacePublicID+"&limit=1",
		tt.AccessToken, nil, &first)
	require.Len(t, first.Items, 1)
	require.GreaterOrEqual(t, first.Total, int64(items), "the total counts the list, not the page")

	var past page
	doJSON(t, http.MethodGet, testServerURL+"/inbox?workspaceId="+tt.WorkspacePublicID+"&limit=1&offset=1000",
		tt.AccessToken, nil, &past)
	require.Empty(t, past.Items)
	require.Equal(t, first.Total, past.Total, "a page past the end still reports the list's total")

	var mine page
	doJSON(t, http.MethodGet, testServerURL+"/inbox?limit=1&offset=1000", tt.AccessToken, nil, &mine)
	require.GreaterOrEqual(t, mine.Total, int64(items), "the cross-workspace feed reports its total too")
}
