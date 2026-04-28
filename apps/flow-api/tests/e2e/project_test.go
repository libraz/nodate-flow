package e2e

import (
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// TestProjectCRUD exercises create, list, get, patch, slug collision and
// disable for the /workspaces/{wsId}/projects and /projects/{prjId}
// routes.
func TestProjectCRUD(t *testing.T) {
	bootstrap(t)
	t.Parallel()

	tt := newTenant(t)

	// Default project exists from CreateTestTenant; list returns it.
	var list struct {
		Total    int64 `json:"total"`
		Projects []struct {
			ID   string `json:"id"`
			Slug string `json:"slug"`
		} `json:"projects"`
	}
	doJSON(t, http.MethodGet, testServerURL+"/workspaces/"+tt.WorkspacePublicID+"/projects",
		tt.AccessToken, nil, &list)
	require.GreaterOrEqual(t, list.Total, int64(1))

	// Get returns the default project by its global id.
	var got struct {
		ID   string `json:"id"`
		Slug string `json:"slug"`
		Name string `json:"name"`
	}
	doJSON(t, http.MethodGet, testServerURL+"/projects/"+tt.ProjectPublicID,
		tt.AccessToken, nil, &got)
	require.Equal(t, tt.ProjectPublicID, got.ID)
	require.Equal(t, tt.ProjectSlug, got.Slug)

	// Patch the project name.
	var patched struct {
		Name string `json:"name"`
	}
	doJSON(t, http.MethodPatch, testServerURL+"/projects/"+tt.ProjectPublicID,
		tt.AccessToken, map[string]any{"name": "Renamed Project"}, &patched)
	require.Equal(t, "Renamed Project", patched.Name)

	// Slug collision: creating a second project with the same slug must fail.
	status, body := doJSONStatus(t, http.MethodPost,
		testServerURL+"/workspaces/"+tt.WorkspacePublicID+"/projects",
		tt.AccessToken, map[string]any{
			"slug": tt.ProjectSlug,
			"name": "Collides",
		})
	require.GreaterOrEqual(t, status, 400, "slug collision must fail")
	require.True(t,
		strings.Contains(string(body), "SLUG_ALREADY_TAKEN") || strings.Contains(string(body), "slug"),
		"collision body must mention slug, got=%s", string(body))

	// Disable (soft delete).
	status, _ = doJSONStatus(t, http.MethodDelete, testServerURL+"/projects/"+tt.ProjectPublicID,
		tt.AccessToken, nil)
	require.Equal(t, http.StatusOK, status)
}

// TestProjectDisableCascadesToChildTasks verifies the transactional
// cascade implemented in projects.Disable: every enabled task that lived
// under the project is flipped to enabled = FALSE in the same write,
// and the project itself is also disabled. The view layer already
// AND-filters projects.enabled, but bypassing the view (raw SELECT on
// tasks) would otherwise still see live rows under a disabled project,
// hence the direct-SQL assertions here.
func TestProjectDisableCascadesToChildTasks(t *testing.T) {
	bootstrap(t)
	t.Parallel()

	tt := newTenant(t)

	// Create two enabled tasks in the default project.
	var t1, t2 struct {
		ID string `json:"id"`
	}
	doJSON(t, http.MethodPost, testServerURL+"/tasks", tt.AccessToken, map[string]any{
		"projectId": tt.ProjectPublicID,
		"title":     "Cascade target one",
	}, &t1)
	require.NotEmpty(t, t1.ID)
	doJSON(t, http.MethodPost, testServerURL+"/tasks", tt.AccessToken, map[string]any{
		"projectId": tt.ProjectPublicID,
		"title":     "Cascade target two",
	}, &t2)
	require.NotEmpty(t, t2.ID)

	// Capture the tasks' updated_at BEFORE the cascade so we can verify
	// the column is bumped by the UPDATE issued in DisableProjectChildTasks.
	// MySQL UNIX_TIMESTAMP returns a fractional value for fractional-second
	// columns, so we scan into float64 to keep the comparison stable.
	var beforeT1, beforeT2 float64
	require.NoError(t, testDB.QueryRow(
		`SELECT UNIX_TIMESTAMP(updated_at) FROM tasks WHERE public_id = UUID_TO_BIN(?, 0)`,
		t1.ID).Scan(&beforeT1))
	require.NoError(t, testDB.QueryRow(
		`SELECT UNIX_TIMESTAMP(updated_at) FROM tasks WHERE public_id = UUID_TO_BIN(?, 0)`,
		t2.ID).Scan(&beforeT2))

	// Wait until MySQL's clock has advanced past the captured baseline
	// by at least one second so the CURRENT_TIMESTAMP(3) stamp the
	// cascade writes is observably greater. UNIX_TIMESTAMP without a
	// fractional argument returns whole seconds so we need a strict
	// integer-second advance, not just a small float bump. Polling
	// avoids paying a fixed 1.1s sleep on hosts whose clock has already
	// rolled over.
	require.Eventually(t, func() bool {
		var nowSec float64
		if err := testDB.QueryRow(`SELECT UNIX_TIMESTAMP(NOW(3))`).Scan(&nowSec); err != nil {
			return false
		}
		return nowSec >= beforeT1+1.0 && nowSec >= beforeT2+1.0
	}, 5*time.Second, 50*time.Millisecond,
		"MySQL NOW() must advance at least one second past the captured baseline")

	// Disable the project.
	status, _ := doJSONStatus(t, http.MethodDelete,
		testServerURL+"/projects/"+tt.ProjectPublicID, tt.AccessToken, nil)
	require.Equal(t, http.StatusOK, status)

	// Project itself must be disabled.
	var projEnabled bool
	require.NoError(t, testDB.QueryRow(
		`SELECT enabled FROM projects WHERE public_id = UUID_TO_BIN(?, 0)`,
		tt.ProjectPublicID).Scan(&projEnabled))
	require.False(t, projEnabled, "project must be disabled by DELETE")

	// Both child tasks must be disabled.
	for _, taskID := range []string{t1.ID, t2.ID} {
		var enabled bool
		require.NoError(t, testDB.QueryRow(
			`SELECT enabled FROM tasks WHERE public_id = UUID_TO_BIN(?, 0)`,
			taskID).Scan(&enabled))
		require.Falsef(t, enabled, "task %s must be cascaded to enabled = FALSE", taskID)
	}

	// updated_at on each task should reflect the cascade.
	var afterT1, afterT2 float64
	require.NoError(t, testDB.QueryRow(
		`SELECT UNIX_TIMESTAMP(updated_at) FROM tasks WHERE public_id = UUID_TO_BIN(?, 0)`,
		t1.ID).Scan(&afterT1))
	require.NoError(t, testDB.QueryRow(
		`SELECT UNIX_TIMESTAMP(updated_at) FROM tasks WHERE public_id = UUID_TO_BIN(?, 0)`,
		t2.ID).Scan(&afterT2))
	require.Greater(t, afterT1, beforeT1, "task1 updated_at must advance")
	require.Greater(t, afterT2, beforeT2, "task2 updated_at must advance")
}
