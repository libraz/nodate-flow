package e2e

import (
	"net/http"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestTaskSearchByTitle verifies that the q= query parameter
// performs a case-insensitive substring search on task titles.
func TestTaskSearchByTitle(t *testing.T) {
	bootstrap(t)
	t.Parallel()
	tt := newTenant(t)

	tag := randomHex(6)
	var target struct {
		ID string `json:"id"`
	}
	doJSON(t, http.MethodPost, testServerURL+"/tasks", tt.AccessToken,
		map[string]any{"projectId": tt.ProjectPublicID, "title": "Searchable " + tag}, &target)

	var other struct {
		ID string `json:"id"`
	}
	doJSON(t, http.MethodPost, testServerURL+"/tasks", tt.AccessToken,
		map[string]any{"projectId": tt.ProjectPublicID, "title": "Unrelated task"}, &other)

	// Search by the unique tag.
	var result struct {
		Tasks []struct {
			ID string `json:"id"`
		} `json:"tasks"`
	}
	doJSON(t, http.MethodGet,
		testServerURL+"/tasks?projectId="+tt.ProjectPublicID+"&q="+tag,
		tt.AccessToken, nil, &result)

	require.Len(t, result.Tasks, 1)
	require.Equal(t, target.ID, result.Tasks[0].ID)
}

// TestTaskSearchCaseInsensitive verifies that search is
// case-insensitive.
func TestTaskSearchCaseInsensitive(t *testing.T) {
	bootstrap(t)
	t.Parallel()
	tt := newTenant(t)

	tag := "CamelCase" + randomHex(4)
	doJSON(t, http.MethodPost, testServerURL+"/tasks", tt.AccessToken,
		map[string]any{"projectId": tt.ProjectPublicID, "title": tag + " task"}, nil)

	// Search with lowercase version.
	var result struct {
		Total int64 `json:"total"`
	}
	doJSON(t, http.MethodGet,
		testServerURL+"/tasks?projectId="+tt.ProjectPublicID+"&q="+
			strings.ToLower(tag),
		tt.AccessToken, nil, &result)
	require.GreaterOrEqual(t, result.Total, int64(1),
		"search must be case-insensitive")
}

// TestTaskSearchNoMatch verifies that a search with no matches
// returns an empty list, not an error.
func TestTaskSearchNoMatch(t *testing.T) {
	bootstrap(t)
	t.Parallel()
	tt := newTenant(t)

	var result struct {
		Total int64 `json:"total"`
		Tasks []struct {
			ID string `json:"id"`
		} `json:"tasks"`
	}
	doJSON(t, http.MethodGet,
		testServerURL+"/tasks?projectId="+tt.ProjectPublicID+"&q=nonexistent"+randomHex(8),
		tt.AccessToken, nil, &result)
	require.Equal(t, int64(0), result.Total)
	require.Empty(t, result.Tasks)
}
