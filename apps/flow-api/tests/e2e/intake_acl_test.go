package e2e

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestIntakeConvertRejectsNonMemberProject verifies that converting an intake
// item into a project the actor is not a member of (and is not workspace
// elevated for) is rejected. A plain workspace member without a project role
// must not be able to create tasks in an arbitrary project via intake convert.
func TestIntakeConvertRejectsNonMemberProject(t *testing.T) {
	bootstrap(t)
	t.Parallel()

	owner := newTenant(t)
	member := newTenant(t)

	// Invite the second user as a plain workspace member (not admin/owner),
	// so they are not workspace-elevated for the owner's default project and
	// hold no project_members row for it.
	var invite struct {
		Token string `json:"token"`
	}
	doJSON(t, http.MethodPost,
		testServerURL+"/workspaces/"+owner.WorkspacePublicID+"/invites",
		owner.AccessToken, map[string]any{"role": "member"}, &invite)
	doJSON(t, http.MethodPost,
		testServerURL+"/invites/"+invite.Token+"/accept",
		member.AccessToken, nil, nil)

	// Owner drops an intake item onto the workspace queue.
	var item struct {
		ID string `json:"id"`
	}
	doJSON(t, http.MethodPost,
		testServerURL+"/workspaces/"+owner.WorkspacePublicID+"/intake",
		owner.AccessToken, map[string]any{"title": "Inbound work"}, &item)
	require.NotEmpty(t, item.ID)

	// Member tries to convert it into the owner's default project, which they
	// are not a project member of. The project-editor gate must reject it.
	status, raw := doJSONStatus(t, http.MethodPost,
		testServerURL+"/workspaces/"+owner.WorkspacePublicID+"/intake/"+item.ID+"/convert",
		member.AccessToken, map[string]any{"projectId": owner.ProjectPublicID})
	require.Equalf(t, http.StatusForbidden, status,
		"non project-member must not convert into that project, body=%s", string(raw))
}

// TestIntakeGuestRejectedOnWrites verifies that a guest-role member (read-only
// per the workspace role contract) cannot create, triage, or convert intake
// items. Guests may still list and read the queue.
func TestIntakeGuestRejectedOnWrites(t *testing.T) {
	bootstrap(t)
	t.Parallel()

	owner := newTenant(t)
	guest := newTenant(t)

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

	// Guest is denied at create.
	createStatus, createRaw := doJSONStatus(t, http.MethodPost,
		wsBase+"/intake", guest.AccessToken,
		map[string]any{"title": "guest capture"})
	require.Equalf(t, http.StatusForbidden, createStatus,
		"guest must not create intake items, body=%s", string(createRaw))

	// Owner creates an item so guest triage/convert have a real target and
	// the 403 comes from the role gate, not a missing item.
	var item struct {
		ID string `json:"id"`
	}
	doJSON(t, http.MethodPost, wsBase+"/intake",
		owner.AccessToken, map[string]any{"title": "Inbound work"}, &item)
	require.NotEmpty(t, item.ID)

	// Guest is denied at triage.
	triageStatus, triageRaw := doJSONStatus(t, http.MethodPatch,
		wsBase+"/intake/"+item.ID, guest.AccessToken,
		map[string]any{"status": "accepted"})
	require.Equalf(t, http.StatusForbidden, triageStatus,
		"guest must not triage intake items, body=%s", string(triageRaw))

	// Guest is denied at convert.
	convertStatus, convertRaw := doJSONStatus(t, http.MethodPost,
		wsBase+"/intake/"+item.ID+"/convert", guest.AccessToken,
		map[string]any{"projectId": owner.ProjectPublicID})
	require.Equalf(t, http.StatusForbidden, convertStatus,
		"guest must not convert intake items, body=%s", string(convertRaw))
}
