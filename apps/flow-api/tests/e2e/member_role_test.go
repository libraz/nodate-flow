package e2e

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestAdminCanChangeMemberRole verifies that a workspace admin can
// change a member's role (e.g., member → admin) and the change is
// reflected in subsequent requests.
func TestAdminCanChangeMemberRole(t *testing.T) {
	bootstrap(t)
	t.Parallel()

	owner := newTenant(t)
	member := newTenant(t)

	// Invite as member.
	var invite struct {
		Token string `json:"token"`
	}
	doJSON(t, http.MethodPost,
		testServerURL+"/workspaces/"+owner.WorkspacePublicID+"/invites",
		owner.AccessToken, map[string]any{"role": "member"}, &invite)
	doJSON(t, http.MethodPost,
		testServerURL+"/invites/"+invite.Token+"/accept",
		member.AccessToken, nil, nil)

	// Verify initial role is "member".
	var members struct {
		Members []struct {
			UserID string `json:"userId"`
			Role   string `json:"role"`
		} `json:"members"`
	}
	doJSON(t, http.MethodGet,
		testServerURL+"/workspaces/"+owner.WorkspacePublicID+"/members",
		owner.AccessToken, nil, &members)

	var initialRole string
	for _, m := range members.Members {
		if m.UserID == member.UserPublicID {
			initialRole = m.Role
		}
	}
	require.Equal(t, "member", initialRole)

	// Owner promotes member to admin.
	var updated struct {
		Role string `json:"role"`
	}
	doJSON(t, http.MethodPatch,
		testServerURL+"/workspaces/"+owner.WorkspacePublicID+"/members/"+member.UserPublicID,
		owner.AccessToken, map[string]any{"role": "admin"}, &updated)
	require.Equal(t, "admin", updated.Role)

	// Verify the promoted user can now perform admin actions (e.g., create invites).
	status, _ := doJSONStatus(t, http.MethodPost,
		testServerURL+"/workspaces/"+owner.WorkspacePublicID+"/invites",
		member.AccessToken, map[string]any{"role": "member"})
	require.Less(t, status, 300,
		"promoted admin must be able to create invites")
}

// TestAdminCanRemoveMember verifies that an admin can remove a member
// and the member can no longer access the workspace.
func TestAdminCanRemoveMember(t *testing.T) {
	bootstrap(t)
	t.Parallel()

	owner := newTenant(t)
	member := newTenant(t)

	var invite struct {
		Token string `json:"token"`
	}
	doJSON(t, http.MethodPost,
		testServerURL+"/workspaces/"+owner.WorkspacePublicID+"/invites",
		owner.AccessToken, map[string]any{"role": "member"}, &invite)
	doJSON(t, http.MethodPost,
		testServerURL+"/invites/"+invite.Token+"/accept",
		member.AccessToken, nil, nil)

	// Owner removes member.
	var result struct {
		Ok bool `json:"ok"`
	}
	doJSON(t, http.MethodDelete,
		testServerURL+"/workspaces/"+owner.WorkspacePublicID+"/members/"+member.UserPublicID,
		owner.AccessToken, nil, &result)
	require.True(t, result.Ok)

	// Removed member can no longer access the workspace. They know the
	// workspace exists (they were in it), so the membership gate refuses
	// rather than conceals.
	status, body := doJSONStatus(t, http.MethodGet,
		testServerURL+"/workspaces/"+owner.WorkspacePublicID,
		member.AccessToken, nil)
	requireDenied(t, status, body, http.StatusForbidden, "WS.WORKSPACE.ACCESS_DENIED",
		"a removed member reading the workspace")
}

// TestWorkspaceUsersSummary verifies that GET /workspaces/{wsId}/users
// returns a minimal user list for actor-filter pickers.
func TestWorkspaceUsersSummary(t *testing.T) {
	bootstrap(t)
	t.Parallel()

	owner := newTenant(t)
	member := newTenant(t)

	var invite struct {
		Token string `json:"token"`
	}
	doJSON(t, http.MethodPost,
		testServerURL+"/workspaces/"+owner.WorkspacePublicID+"/invites",
		owner.AccessToken, map[string]any{"role": "member"}, &invite)
	doJSON(t, http.MethodPost,
		testServerURL+"/invites/"+invite.Token+"/accept",
		member.AccessToken, nil, nil)

	var users struct {
		Users []struct {
			ID          string `json:"id"`
			DisplayName string `json:"displayName"`
		} `json:"users"`
	}
	doJSON(t, http.MethodGet,
		testServerURL+"/workspaces/"+owner.WorkspacePublicID+"/users",
		owner.AccessToken, nil, &users)

	require.GreaterOrEqual(t, len(users.Users), 2,
		"must include at least owner and member")

	// Both users should appear.
	ids := map[string]bool{}
	for _, u := range users.Users {
		ids[u.ID] = true
		require.NotEmpty(t, u.DisplayName)
	}
	require.True(t, ids[owner.UserPublicID], "owner must appear")
	require.True(t, ids[member.UserPublicID], "member must appear")
}
