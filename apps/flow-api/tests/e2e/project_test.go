package e2e

import (
	"net/http"
	"strings"
	"testing"

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
