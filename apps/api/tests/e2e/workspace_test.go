package e2e

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestWorkspaceCRUD exercises create, list, get, patch and member ops.
func TestWorkspaceCRUD(t *testing.T) {
	bootstrap(t)
	t.Parallel()

	tt := newTenant(t)

	// List returns at least the tenant's workspace.
	var list struct {
		Total      int64 `json:"total"`
		Workspaces []struct {
			ID   string `json:"id"`
			Slug string `json:"slug"`
			Role string `json:"role"`
		} `json:"workspaces"`
	}
	doJSON(t, http.MethodGet, testServerURL+"/workspaces", tt.AccessToken, nil, &list)
	require.GreaterOrEqual(t, list.Total, int64(1))
	found := false
	for _, w := range list.Workspaces {
		if w.ID == tt.WorkspacePublicID {
			found = true
			require.Equal(t, "owner", w.Role, "tenant must be owner of its workspace")
		}
	}
	require.True(t, found, "workspace list must include tenant workspace")

	// Get returns the workspace by id.
	var got struct {
		ID   string `json:"id"`
		Slug string `json:"slug"`
		Name string `json:"name"`
	}
	doJSON(t, http.MethodGet, testServerURL+"/workspaces/"+tt.WorkspacePublicID, tt.AccessToken, nil, &got)
	require.Equal(t, tt.WorkspacePublicID, got.ID)
	require.Equal(t, tt.WorkspaceSlug, got.Slug)

	// Patch renames the workspace.
	var patched struct {
		Name string `json:"name"`
	}
	doJSON(t, http.MethodPatch, testServerURL+"/workspaces/"+tt.WorkspacePublicID, tt.AccessToken, map[string]any{
		"name": "Renamed Workspace",
	}, &patched)
	require.Equal(t, "Renamed Workspace", patched.Name)
}

// TestWorkspaceMemberLifecycle verifies invite, role change, and remove.
func TestWorkspaceMemberLifecycle(t *testing.T) {
	bootstrap(t)
	t.Parallel()

	host := newTenant(t)
	guest := newTenant(t)

	// Invite the guest user by email.
	var invited struct {
		UserID string `json:"userId"`
		Role   string `json:"role"`
	}
	doJSON(t, http.MethodPost, testServerURL+"/workspaces/"+host.WorkspacePublicID+"/members",
		host.AccessToken, map[string]any{
			"email": guest.Email,
			"role":  "member",
		}, &invited)
	require.Equal(t, guest.UserPublicID, invited.UserID)
	require.Equal(t, "member", invited.Role)

	// Promote the guest to admin.
	var promoted struct {
		Role string `json:"role"`
	}
	doJSON(t, http.MethodPatch,
		testServerURL+"/workspaces/"+host.WorkspacePublicID+"/members/"+guest.UserPublicID,
		host.AccessToken, map[string]any{"role": "admin"}, &promoted)
	require.Equal(t, "admin", promoted.Role)

	// Remove the guest.
	status, _ := doJSONStatus(t, http.MethodDelete,
		testServerURL+"/workspaces/"+host.WorkspacePublicID+"/members/"+guest.UserPublicID,
		host.AccessToken, nil)
	require.Equal(t, http.StatusOK, status)
}
