package e2e

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestDuplicateDependencyIsAConflict holds a repeated add of the same edge
// to a structured 409, the answer a double-click or client retry gets, and
// to leaving exactly one edge behind.
func TestDuplicateDependencyIsAConflict(t *testing.T) {
	bootstrap(t)
	t.Parallel()
	tt := newTenant(t)

	a := createTask(t, tt.AccessToken, tt.ProjectPublicID, "Dup dep A")
	b := createTask(t, tt.AccessToken, tt.ProjectPublicID, "Dup dep B")
	url := testServerURL + "/tasks/" + a + "/dependencies"
	body := map[string]any{"toTaskId": b, "kind": "blocks"}

	doJSON(t, http.MethodPost, url, tt.AccessToken, body, nil)

	status, raw := doJSONStatus(t, http.MethodPost, url, tt.AccessToken, body)
	require.Equal(t, http.StatusConflict, status, "repeated edge must be a conflict: %s", string(raw))
	require.Equal(t, "WS.TASK.DEPENDENCY_ALREADY_EXISTS", problemType(t, raw))

	var edges int
	require.NoError(t, testDB.QueryRow(`
		SELECT COUNT(*) FROM task_dependencies d
		JOIN tasks f ON f.id = d.from_task_id
		JOIN tasks o ON o.id = d.to_task_id
		WHERE f.public_id = UUID_TO_BIN(?, 0) AND o.public_id = UUID_TO_BIN(?, 0)
		  AND d.kind = 'blocks' AND d.active IS NOT NULL`, a, b,
	).Scan(&edges))
	require.Equal(t, 1, edges, "the refused add must not leave a second edge")
}
