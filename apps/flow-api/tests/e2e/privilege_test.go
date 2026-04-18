package e2e

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestMemberCannotElevateOwnRole verifies that a regular member cannot
// call the update-role endpoint to promote themselves to admin.
func TestMemberCannotElevateOwnRole(t *testing.T) {
	bootstrap(t)
	t.Parallel()

	owner := newTenant(t)
	member := newTenant(t)

	// Invite member as "member" role.
	var invite struct {
		Token string `json:"token"`
	}
	doJSON(t, http.MethodPost,
		testServerURL+"/workspaces/"+owner.WorkspacePublicID+"/invites",
		owner.AccessToken, map[string]any{"role": "member"}, &invite)
	doJSON(t, http.MethodPost,
		testServerURL+"/invites/"+invite.Token+"/accept",
		member.AccessToken, nil, nil)

	// Member tries to promote themselves to admin.
	status, _ := doJSONStatus(t, http.MethodPatch,
		testServerURL+"/workspaces/"+owner.WorkspacePublicID+"/members/"+member.UserPublicID,
		member.AccessToken, map[string]any{"role": "admin"})
	require.GreaterOrEqual(t, status, 403,
		"member must not be able to elevate own role")
}

// TestMemberCannotChangeOtherRoles verifies that a regular member
// cannot change anyone else's role either.
func TestMemberCannotChangeOtherRoles(t *testing.T) {
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

	// Member tries to demote the owner.
	status, _ := doJSONStatus(t, http.MethodPatch,
		testServerURL+"/workspaces/"+owner.WorkspacePublicID+"/members/"+owner.UserPublicID,
		member.AccessToken, map[string]any{"role": "guest"})
	require.GreaterOrEqual(t, status, 403,
		"member must not be able to change owner's role")
}

// TestMemberCannotRemoveOtherMembers verifies that a regular member
// cannot remove other members from the workspace.
func TestMemberCannotRemoveOtherMembers(t *testing.T) {
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

	// Member tries to remove the owner.
	status, _ := doJSONStatus(t, http.MethodDelete,
		testServerURL+"/workspaces/"+owner.WorkspacePublicID+"/members/"+owner.UserPublicID,
		member.AccessToken, nil)
	require.GreaterOrEqual(t, status, 403,
		"member must not be able to remove other members")
}

// TestMemberCannotCreateInvites verifies that a regular member (not
// admin) cannot create workspace invite links.
func TestMemberCannotCreateInvites(t *testing.T) {
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

	// Member tries to create an invite.
	status, _ := doJSONStatus(t, http.MethodPost,
		testServerURL+"/workspaces/"+owner.WorkspacePublicID+"/invites",
		member.AccessToken, map[string]any{"role": "member"})
	require.GreaterOrEqual(t, status, 403,
		"member must not be able to create invites")
}

// TestGuestCannotPerformAdminActions verifies that a guest-role member
// cannot perform admin-only operations (invites, webhooks, member
// management, workspace deletion).
func TestGuestCannotPerformAdminActions(t *testing.T) {
	bootstrap(t)
	t.Parallel()

	owner := newTenant(t)
	guest := newTenant(t)

	// Invite as guest.
	var invite struct {
		Token string `json:"token"`
	}
	doJSON(t, http.MethodPost,
		testServerURL+"/workspaces/"+owner.WorkspacePublicID+"/invites",
		owner.AccessToken, map[string]any{"role": "guest"}, &invite)
	doJSON(t, http.MethodPost,
		testServerURL+"/invites/"+invite.Token+"/accept",
		guest.AccessToken, nil, nil)

	wsBase := testServerURL + "/workspaces/" + owner.WorkspacePublicID

	adminOps := []struct {
		name   string
		method string
		path   string
		body   any
	}{
		{"create invite", http.MethodPost, wsBase + "/invites",
			map[string]any{"role": "member"}},
		{"create webhook", http.MethodPost, wsBase + "/webhooks",
			map[string]any{"url": "https://evil.com", "description": "x",
				"eventTypes": json.RawMessage(`["*"]`)}},
		{"list webhooks", http.MethodGet, wsBase + "/webhooks", nil},
		{"change member role", http.MethodPatch,
			wsBase + "/members/" + owner.UserPublicID,
			map[string]any{"role": "member"}},
		{"remove member", http.MethodDelete,
			wsBase + "/members/" + owner.UserPublicID, nil},
		{"disable workspace", http.MethodDelete, wsBase, nil},
	}

	for _, op := range adminOps {
		t.Run(op.name, func(t *testing.T) {
			status, _ := doJSONStatus(t, op.method, op.path, guest.AccessToken, op.body)
			require.GreaterOrEqual(t, status, 403,
				"guest must not be able to %s", op.name)
		})
	}
}

// TestNonOwnerCannotDisableWorkspace verifies that only workspace
// owners can soft-delete a workspace.
func TestNonOwnerCannotDisableWorkspace(t *testing.T) {
	bootstrap(t)
	t.Parallel()

	owner := newTenant(t)
	admin := newTenant(t)

	// Invite as admin (not owner).
	var invite struct {
		Token string `json:"token"`
	}
	doJSON(t, http.MethodPost,
		testServerURL+"/workspaces/"+owner.WorkspacePublicID+"/invites",
		owner.AccessToken, map[string]any{"role": "admin"}, &invite)
	doJSON(t, http.MethodPost,
		testServerURL+"/invites/"+invite.Token+"/accept",
		admin.AccessToken, nil, nil)

	// Admin tries to delete workspace.
	status, _ := doJSONStatus(t, http.MethodDelete,
		testServerURL+"/workspaces/"+owner.WorkspacePublicID,
		admin.AccessToken, nil)
	require.GreaterOrEqual(t, status, 403,
		"only owner should be able to disable workspace")
}
