package e2e

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestOwnerCanDisableWorkspace verifies that the workspace owner
// can soft-disable their workspace and all sub-resources become
// inaccessible.
func TestOwnerCanDisableWorkspace(t *testing.T) {
	bootstrap(t)
	t.Parallel()
	tt := newTenant(t)

	// Disable the workspace.
	var ok struct {
		Ok bool `json:"ok"`
	}
	doJSON(t, http.MethodDelete,
		testServerURL+"/workspaces/"+tt.WorkspacePublicID,
		tt.AccessToken, nil, &ok)
	require.True(t, ok.Ok)

	// Workspace is no longer accessible.
	status, _ := doJSONStatus(t, http.MethodGet,
		testServerURL+"/workspaces/"+tt.WorkspacePublicID,
		tt.AccessToken, nil)
	require.GreaterOrEqual(t, status, 400,
		"disabled workspace must not be accessible")

	// Tasks in the workspace are inaccessible.
	status, _ = doJSONStatus(t, http.MethodGet,
		testServerURL+"/tasks?workspaceId="+tt.WorkspacePublicID,
		tt.AccessToken, nil)
	require.GreaterOrEqual(t, status, 400,
		"tasks in disabled workspace must not be accessible")

	// Projects in the workspace are inaccessible.
	status, _ = doJSONStatus(t, http.MethodGet,
		testServerURL+"/workspaces/"+tt.WorkspacePublicID+"/projects",
		tt.AccessToken, nil)
	require.GreaterOrEqual(t, status, 400,
		"projects in disabled workspace must not be accessible")
}
