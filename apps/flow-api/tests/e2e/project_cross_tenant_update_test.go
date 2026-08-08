package e2e

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestCrossTenantProjectUpdateIsolation verifies that UpdateProjectFull
// includes workspace_id in its WHERE clause so that a project owned by
// workspace A cannot be updated when the request routes through
// workspace B.
func TestCrossTenantProjectUpdateIsolation(t *testing.T) {
	bootstrap(t)
	t.Parallel()

	ownerA := newTenant(t)
	ownerB := newTenant(t)

	// ownerA creates a project in workspace A.
	var project struct {
		ID   string `json:"id"`
		Slug string `json:"slug"`
		Name string `json:"name"`
	}
	doJSON(t, http.MethodPost,
		testServerURL+"/workspaces/"+ownerA.WorkspacePublicID+"/projects",
		ownerA.AccessToken, map[string]any{
			"slug": "cross-tenant-prj-" + randomHex(4),
			"name": "Original Name",
		}, &project)
	require.NotEmpty(t, project.ID, "project create must return an id")

	// Attempt to update ownerA's project as ownerB. The project gate
	// resolves the id and then refuses on membership, so the answer is
	// 403 WS.PROJECT.ACCESS_DENIED — pinned here so a regression that
	// turns the resolve step into a 500 cannot pass as a refusal.
	status, body := doJSONStatus(t, http.MethodPatch,
		testServerURL+"/projects/"+project.ID,
		ownerB.AccessToken, map[string]any{"name": "Hacked Name"})
	requireDenied(t, status, body, http.StatusForbidden, "WS.PROJECT.ACCESS_DENIED",
		"an outsider updating a project in another workspace")
	require.NotContains(t, string(body), "Original Name",
		"a refusal must not carry the project it refused")

	// Verify the project name is unchanged by fetching it as ownerA.
	var fetched struct {
		Name string `json:"name"`
	}
	doJSON(t, http.MethodGet, testServerURL+"/projects/"+project.ID,
		ownerA.AccessToken, nil, &fetched)
	require.Equal(t, "Original Name", fetched.Name,
		"project name must remain unchanged after cross-tenant update attempt")

	// ownerA can update their own project successfully.
	var updated struct {
		Name string `json:"name"`
	}
	doJSON(t, http.MethodPatch, testServerURL+"/projects/"+project.ID,
		ownerA.AccessToken, map[string]any{"name": "Updated Name"}, &updated)
	require.Equal(t, "Updated Name", updated.Name,
		"owner must be able to update their own project")
}
