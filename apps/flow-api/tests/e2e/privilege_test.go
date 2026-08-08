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
	//
	// The actor is a member of this workspace, so hiding the route
	// behind a 404 would tell them nothing they do not already know:
	// the refusal is 403 WS.MEMBER.ROLE_DENIED, from the role gate.
	status, body := doJSONStatus(t, http.MethodPatch,
		testServerURL+"/workspaces/"+owner.WorkspacePublicID+"/members/"+member.UserPublicID,
		member.AccessToken, map[string]any{"role": "admin"})
	requireDenied(t, status, body, http.StatusForbidden, "WS.MEMBER.ROLE_DENIED",
		"a member elevating their own role")

	// The role did not move.
	var members struct {
		Members []struct {
			UserID string `json:"userId"`
			Role   string `json:"role"`
		} `json:"members"`
	}
	doJSON(t, http.MethodGet,
		testServerURL+"/workspaces/"+owner.WorkspacePublicID+"/members",
		owner.AccessToken, nil, &members)
	var found bool
	for _, m := range members.Members {
		if m.UserID == member.UserPublicID {
			found = true
			require.Equal(t, "member", m.Role,
				"the refused PATCH must leave the role as invited")
		}
	}
	require.True(t, found, "the invited member must still be listed")
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
	status, body := doJSONStatus(t, http.MethodPatch,
		testServerURL+"/workspaces/"+owner.WorkspacePublicID+"/members/"+owner.UserPublicID,
		member.AccessToken, map[string]any{"role": "guest"})
	requireDenied(t, status, body, http.StatusForbidden, "WS.MEMBER.ROLE_DENIED",
		"a member demoting the owner")
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
	status, body := doJSONStatus(t, http.MethodDelete,
		testServerURL+"/workspaces/"+owner.WorkspacePublicID+"/members/"+owner.UserPublicID,
		member.AccessToken, nil)
	requireDenied(t, status, body, http.StatusForbidden, "WS.MEMBER.ROLE_DENIED",
		"a member removing the owner")
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
	status, body := doJSONStatus(t, http.MethodPost,
		testServerURL+"/workspaces/"+owner.WorkspacePublicID+"/invites",
		member.AccessToken, map[string]any{"role": "member"})
	requireDenied(t, status, body, http.StatusForbidden, "WS.MEMBER.ROLE_DENIED",
		"a member creating an invite")
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
		// DELETE /workspaces/{wsId} requires {"confirm": true} in the body
		// to be reachable past the schema layer; we send it so the role
		// check (this test's actual subject) is what produces the 403,
		// not the missing-confirm guard (which would yield 400).
		{"delete workspace", http.MethodDelete, wsBase,
			map[string]any{"confirm": true}},
	}

	// Each of these is refused by the role gate, which the guest reaches
	// as a genuine (if minimally privileged) member — hence 403 rather
	// than a concealing 404, and hence one shared code for all of them.
	for _, op := range adminOps {
		t.Run(op.name, func(t *testing.T) {
			status, body := doJSONStatus(t, op.method, op.path, guest.AccessToken, op.body)
			requireDenied(t, status, body, http.StatusForbidden, "WS.MEMBER.ROLE_DENIED",
				"a guest performing "+op.name)
		})
	}
}

// TestNonOwnerCannotDeleteWorkspace verifies that only workspace owners
// can immediate-delete a workspace via DELETE /workspaces/{wsId}.
//
// The confirm=true body is included so we are exclusively testing the
// role-based rejection (RequireWorkspaceRole), not the missing-confirm
// guard (WORKSPACE.DELETE.CONFIRM_REQUIRED). Even with a valid confirm
// payload, a non-owner must be rejected with 403.
func TestNonOwnerCannotDeleteWorkspace(t *testing.T) {
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

	// Admin tries to delete workspace WITH confirm=true so we isolate
	// the role check from the missing-confirm guard.
	status, body := doJSONStatus(t, http.MethodDelete,
		testServerURL+"/workspaces/"+owner.WorkspacePublicID,
		admin.AccessToken, map[string]any{"confirm": true})
	requireDenied(t, status, body, http.StatusForbidden, "WS.MEMBER.ROLE_DENIED",
		"a non-owner admin deleting the workspace")

	// The workspace is still there.
	status, _ = doJSONStatus(t, http.MethodGet,
		testServerURL+"/workspaces/"+owner.WorkspacePublicID, owner.AccessToken, nil)
	require.Equal(t, http.StatusOK, status,
		"the workspace must survive the refused delete")
}
