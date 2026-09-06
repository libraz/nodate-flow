package e2e

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/libraz/nodate-flow/apps/flow-api/tests/helpers"
)

// inviteInto joins joiner to owner's workspace with the given role.
func inviteInto(t *testing.T, owner, joiner *helpers.TestTenant, role string) {
	t.Helper()
	var invite struct {
		Token string `json:"token"`
	}
	doJSON(t, http.MethodPost,
		testServerURL+"/workspaces/"+owner.WorkspacePublicID+"/invites",
		owner.AccessToken, map[string]any{"role": role}, &invite)
	doJSON(t, http.MethodPost,
		testServerURL+"/invites/"+invite.Token+"/accept",
		joiner.AccessToken, nil, nil)
}

// createLens saves a lens in wsID as the holder of token and returns its
// public id.
func createLens(t *testing.T, wsID, token, name string) string {
	t.Helper()
	var created struct {
		ID string `json:"id"`
	}
	doJSON(t, http.MethodPost, testServerURL+"/workspaces/"+wsID+"/lenses", token,
		map[string]any{
			"name":      name,
			"filter":    json.RawMessage(`{"priority":{"gte":3}}`),
			"sort":      json.RawMessage(`[]`),
			"isDefault": false,
		}, &created)
	require.NotEmpty(t, created.ID)
	return created.ID
}

// TestLensWriteRequiresCreatorOrAdmin pins who may change a saved view.
//
// Publishing a lens is gated to its creator and to workspace admins,
// because the result is a projection of the workspace's tasks on an
// unauthenticated URL. Editing carries the same authority: replacing a
// published lens's filter changes what that URL serves, so a member who
// may not publish may not rewrite someone else's view either — nor
// delete it. The refusal is the workspace role denial, which is what the
// publish gate already answers.
func TestLensWriteRequiresCreatorOrAdmin(t *testing.T) {
	bootstrap(t)
	t.Parallel()

	owner := newTenant(t)
	member := newTenant(t)
	admin := newTenant(t)
	inviteInto(t, owner, member, "member")
	inviteInto(t, owner, admin, "admin")

	base := testServerURL + "/workspaces/" + owner.WorkspacePublicID + "/lenses"
	lensID := createLens(t, owner.WorkspacePublicID, owner.AccessToken, "Owner View "+randomHex(4))

	// A member who is neither creator nor admin may read the lens but may
	// not change what it selects.
	doJSON(t, http.MethodGet, base+"/"+lensID, member.AccessToken, nil, nil)

	status, body := doJSONStatus(t, http.MethodPatch, base+"/"+lensID, member.AccessToken,
		map[string]any{"filter": json.RawMessage(`{}`)})
	requireDenied(t, status, body, http.StatusForbidden, "WS.MEMBER.ROLE_DENIED",
		"plain member patching another member's lens")

	status, body = doJSONStatus(t, http.MethodDelete, base+"/"+lensID, member.AccessToken, nil)
	requireDenied(t, status, body, http.StatusForbidden, "WS.MEMBER.ROLE_DENIED",
		"plain member deleting another member's lens")

	// The refusal held: the filter the member tried to replace is intact.
	var afterRefusal struct {
		Filter json.RawMessage `json:"filter"`
	}
	doJSON(t, http.MethodGet, base+"/"+lensID, owner.AccessToken, nil, &afterRefusal)
	require.JSONEq(t, `{"priority":{"gte":3}}`, string(afterRefusal.Filter),
		"a refused patch must not have changed the lens")

	// A workspace admin may still edit it, so the rule is ownership and
	// not "nobody but the creator".
	var patched struct {
		Name string `json:"name"`
	}
	doJSON(t, http.MethodPatch, base+"/"+lensID, admin.AccessToken,
		map[string]any{"name": "Admin Renamed"}, &patched)
	require.Equal(t, "Admin Renamed", patched.Name)

	// The creator may still edit and delete their own view, so the
	// refusals above cannot pass on a handler that refuses everyone.
	doJSON(t, http.MethodPatch, base+"/"+lensID, owner.AccessToken,
		map[string]any{"name": "Creator Renamed"}, &patched)
	require.Equal(t, "Creator Renamed", patched.Name)

	status, body = doJSONStatus(t, http.MethodDelete, base+"/"+lensID, owner.AccessToken, nil)
	require.Equalf(t, http.StatusOK, status, "creator deleting own lens: body=%s", string(body))
}

// TestLensMemberCanWriteOwnLens pins the other side of the ownership
// rule: a plain member is not locked out of the lenses they created.
func TestLensMemberCanWriteOwnLens(t *testing.T) {
	bootstrap(t)
	t.Parallel()

	owner := newTenant(t)
	member := newTenant(t)
	inviteInto(t, owner, member, "member")

	base := testServerURL + "/workspaces/" + owner.WorkspacePublicID + "/lenses"
	lensID := createLens(t, owner.WorkspacePublicID, member.AccessToken, "Member View "+randomHex(4))

	var patched struct {
		Name string `json:"name"`
	}
	doJSON(t, http.MethodPatch, base+"/"+lensID, member.AccessToken,
		map[string]any{"name": "Member Renamed"}, &patched)
	require.Equal(t, "Member Renamed", patched.Name)

	status, body := doJSONStatus(t, http.MethodDelete, base+"/"+lensID, member.AccessToken, nil)
	require.Equalf(t, http.StatusOK, status, "member deleting own lens: body=%s", string(body))
}
