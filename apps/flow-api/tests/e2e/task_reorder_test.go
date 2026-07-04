package e2e

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestTaskReorder verifies that POST /tasks/reorder updates the
// sort weights of tasks and the new order is reflected in list.
func TestTaskReorder(t *testing.T) {
	bootstrap(t)
	t.Parallel()
	tt := newTenant(t)

	// Create 3 tasks.
	ids := make([]string, 3)
	for i := 0; i < 3; i++ {
		var task struct {
			ID string `json:"id"`
		}
		doJSON(t, http.MethodPost, testServerURL+"/tasks", tt.AccessToken,
			map[string]any{
				"projectId": tt.ProjectPublicID,
				"title":     "Reorder " + randomHex(4),
			}, &task)
		ids[i] = task.ID
	}

	// Reverse the order via reorder.
	items := []map[string]any{
		{"id": ids[2], "sortWeight": 10},
		{"id": ids[1], "sortWeight": 20},
		{"id": ids[0], "sortWeight": 30},
	}
	var ok struct {
		Ok bool `json:"ok"`
	}
	doJSON(t, http.MethodPost, testServerURL+"/tasks/reorder", tt.AccessToken,
		map[string]any{
			"projectId": tt.ProjectPublicID,
			"items":     items,
		}, &ok)
	require.True(t, ok.Ok)

	// List tasks — verify new order (sorted by sort_weight ASC).
	var list struct {
		Tasks []struct {
			ID         string `json:"id"`
			SortWeight int32  `json:"sortWeight"`
		} `json:"tasks"`
	}
	doJSON(t, http.MethodGet,
		testServerURL+"/tasks?projectId="+tt.ProjectPublicID,
		tt.AccessToken, nil, &list)

	// Find our tasks and verify weights.
	weights := map[string]int32{}
	for _, task := range list.Tasks {
		weights[task.ID] = task.SortWeight
	}
	require.Equal(t, int32(10), weights[ids[2]])
	require.Equal(t, int32(20), weights[ids[1]])
	require.Equal(t, int32(30), weights[ids[0]])

	var updatedByMatches int
	err := testDB.QueryRow(
		`SELECT COUNT(*) FROM tasks
		 WHERE public_id IN (UUID_TO_BIN(?, 0), UUID_TO_BIN(?, 0), UUID_TO_BIN(?, 0))
		   AND updated_by_user_id = (SELECT id FROM users WHERE public_id = UUID_TO_BIN(?, 0))`,
		ids[0], ids[1], ids[2], tt.UserPublicID).Scan(&updatedByMatches)
	require.NoError(t, err)
	require.Equal(t, 3, updatedByMatches,
		"POST /tasks/reorder must record actor in updated_by_user_id for every reordered task")
}
