package e2e

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestLensVisibleToAllWorkspaceMembers verifies that lenses
// created by any workspace member are visible to other members.
func TestLensVisibleToAllWorkspaceMembers(t *testing.T) {
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

	wsURL := testServerURL + "/workspaces/" + owner.WorkspacePublicID

	// Owner creates a lens.
	var lens struct {
		ID string `json:"id"`
	}
	doJSON(t, http.MethodPost, wsURL+"/lenses", owner.AccessToken,
		map[string]any{
			"name":      "Shared Lens",
			"filter":    map[string]any{"status": map[string]any{"values": []string{"open"}}},
			"sort":      []map[string]any{},
			"isDefault": false,
		}, &lens)

	// Member can see it in the list.
	var list struct {
		Lenses []struct {
			ID string `json:"id"`
		} `json:"lenses"`
	}
	doJSON(t, http.MethodGet, wsURL+"/lenses",
		member.AccessToken, nil, &list)

	found := false
	for _, l := range list.Lenses {
		if l.ID == lens.ID {
			found = true
		}
	}
	require.True(t, found,
		"workspace member must see lenses created by other members")
}

// TestLensCrossTenantNotVisible verifies that lenses from one
// workspace are invisible to users of another workspace.
func TestLensCrossTenantNotVisible(t *testing.T) {
	bootstrap(t)
	t.Parallel()

	tenant1 := newTenant(t)
	tenant2 := newTenant(t)

	wsURL := testServerURL + "/workspaces/" + tenant1.WorkspacePublicID

	// Tenant1 creates a lens.
	var lens struct {
		ID string `json:"id"`
	}
	doJSON(t, http.MethodPost, wsURL+"/lenses", tenant1.AccessToken,
		map[string]any{
			"name":      "T1 Lens",
			"filter":    map[string]any{"status": map[string]any{"values": []string{"open"}}},
			"sort":      []map[string]any{},
			"isDefault": false,
		}, &lens)

	// Tenant2 cannot access tenant1's lens. The workspace id is in the
	// path and tenant2 supplied it, so the refusal is the membership
	// gate's 403 and nothing about the lens is disclosed either way.
	status, body := doJSONStatus(t, http.MethodGet,
		wsURL+"/lenses/"+lens.ID, tenant2.AccessToken, nil)
	requireDenied(t, status, body, http.StatusForbidden, "WS.WORKSPACE.ACCESS_DENIED",
		"outsider reading another workspace's lens")
	require.NotContains(t, string(body), "T1 Lens",
		"a refusal must not carry the lens it refused")

	// The name is discoverable by tenant1, so the assertion above is
	// about authorization rather than a route that answers nobody.
	status, body = doJSONStatus(t, http.MethodGet, wsURL+"/lenses/"+lens.ID, tenant1.AccessToken, nil)
	require.Equal(t, http.StatusOK, status, "the lens's own workspace must still be able to read it")
	require.Contains(t, string(body), "T1 Lens",
		"tenant1's read must contain the lens's name")
}
