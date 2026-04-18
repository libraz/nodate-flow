package e2e

import (
	"fmt"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// TestWorkspaceInviteLifecycle exercises the full invite link happy path:
// create, list, public info, accept, verify membership, idempotent
// accept, revoke, and info-after-revoke.
func TestWorkspaceInviteLifecycle(t *testing.T) {
	bootstrap(t)
	t.Parallel()

	host := newTenant(t)
	guest := newTenant(t)

	// 1. Create invite link.
	var created struct {
		Invite struct {
			ID       string `json:"id"`
			Role     string `json:"role"`
			MaxUses  *int32 `json:"maxUses"`
			UseCount uint32 `json:"useCount"`
		} `json:"invite"`
		Token string `json:"token"`
	}
	doJSON(t, http.MethodPost,
		testServerURL+"/workspaces/"+host.WorkspacePublicID+"/invites",
		host.AccessToken, map[string]any{
			"role":      "member",
			"expiresIn": 86400,
			"maxUses":   10,
		}, &created)

	require.NotEmpty(t, created.Token, "create invite must return a token")
	require.NotEmpty(t, created.Invite.ID, "create invite must return an id")
	require.Equal(t, "member", created.Invite.Role)
	require.NotNil(t, created.Invite.MaxUses)
	require.Equal(t, int32(10), *created.Invite.MaxUses)
	require.Equal(t, uint32(0), created.Invite.UseCount)

	inviteID := created.Invite.ID
	inviteToken := created.Token

	// 2. List invites.
	var listed struct {
		Total   int64 `json:"total"`
		Invites []struct {
			ID   string `json:"id"`
			Role string `json:"role"`
		} `json:"invites"`
	}
	doJSON(t, http.MethodGet,
		testServerURL+"/workspaces/"+host.WorkspacePublicID+"/invites",
		host.AccessToken, nil, &listed)

	require.GreaterOrEqual(t, listed.Total, int64(1))
	found := false
	for _, inv := range listed.Invites {
		if inv.ID == inviteID {
			found = true
			require.Equal(t, "member", inv.Role)
		}
	}
	require.True(t, found, "list invites must include the created invite")

	// 3. Invite info (public, no auth).
	var info struct {
		WorkspaceName string `json:"workspaceName"`
		Role          string `json:"role"`
	}
	doJSON(t, http.MethodGet,
		testServerURL+"/invites/"+inviteToken+"/info",
		"", nil, &info)

	require.NotEmpty(t, info.WorkspaceName, "invite info must include workspace name")
	require.Equal(t, "member", info.Role)

	// 4. Accept invite as guest.
	var accepted struct {
		WorkspaceID   string `json:"workspaceId"`
		WorkspaceName string `json:"workspaceName"`
		Role          string `json:"role"`
	}
	doJSON(t, http.MethodPost,
		testServerURL+"/invites/"+inviteToken+"/accept",
		guest.AccessToken, nil, &accepted)

	require.NotEmpty(t, accepted.WorkspaceID, "accept must return workspace id")
	require.Equal(t, "member", accepted.Role)

	// 5. Verify membership: guest appears in host's workspace members.
	var members struct {
		Total   int64 `json:"total"`
		Members []struct {
			UserID string `json:"userId"`
			Role   string `json:"role"`
		} `json:"members"`
	}
	doJSON(t, http.MethodGet,
		testServerURL+"/workspaces/"+host.WorkspacePublicID+"/members",
		host.AccessToken, nil, &members)

	guestFound := false
	for _, m := range members.Members {
		if m.UserID == guest.UserPublicID {
			guestFound = true
			require.Equal(t, "member", m.Role)
		}
	}
	require.True(t, guestFound, "guest must appear in workspace members after accepting invite")

	// 6. Idempotent accept: accepting again should still succeed.
	var accepted2 struct {
		WorkspaceID string `json:"workspaceId"`
		Role        string `json:"role"`
	}
	doJSON(t, http.MethodPost,
		testServerURL+"/invites/"+inviteToken+"/accept",
		guest.AccessToken, nil, &accepted2)

	require.NotEmpty(t, accepted2.WorkspaceID)

	// 7. Revoke invite.
	status, body := doJSONStatus(t, http.MethodDelete,
		testServerURL+"/workspaces/"+host.WorkspacePublicID+"/invites/"+inviteID,
		host.AccessToken, nil)
	require.Equal(t, http.StatusOK, status, "revoke invite: %s", string(body))

	// 8. Info after revoke should fail.
	statusAfter, _ := doJSONStatus(t, http.MethodGet,
		testServerURL+"/invites/"+inviteToken+"/info",
		"", nil)
	require.GreaterOrEqual(t, statusAfter, 400, "info after revoke must be non-2xx")
}

// TestWorkspaceInviteMaxUses verifies that an invite with maxUses=1 can
// only be accepted by one user; a second user receives a non-2xx response.
func TestWorkspaceInviteMaxUses(t *testing.T) {
	bootstrap(t)
	t.Parallel()

	host := newTenant(t)
	guest1 := newTenant(t)
	guest2 := newTenant(t)

	// Create invite with maxUses=1.
	var created struct {
		Invite struct {
			ID string `json:"id"`
		} `json:"invite"`
		Token string `json:"token"`
	}
	doJSON(t, http.MethodPost,
		testServerURL+"/workspaces/"+host.WorkspacePublicID+"/invites",
		host.AccessToken, map[string]any{
			"role":    "member",
			"maxUses": 1,
		}, &created)
	require.NotEmpty(t, created.Token)

	// guest1 accepts successfully.
	doJSON(t, http.MethodPost,
		testServerURL+"/invites/"+created.Token+"/accept",
		guest1.AccessToken, nil, nil)

	// guest2 should be rejected (invite exhausted).
	status, _ := doJSONStatus(t, http.MethodPost,
		testServerURL+"/invites/"+created.Token+"/accept",
		guest2.AccessToken, nil)
	require.GreaterOrEqual(t, status, 400,
		"accepting an exhausted invite must return non-2xx")
}

// TestWorkspaceInviteExpired verifies that an expired invite cannot be
// accepted.
func TestWorkspaceInviteExpired(t *testing.T) {
	bootstrap(t)
	t.Parallel()

	host := newTenant(t)
	guest := newTenant(t)

	// Create invite with a 1-second TTL.
	var created struct {
		Token string `json:"token"`
	}
	doJSON(t, http.MethodPost,
		testServerURL+"/workspaces/"+host.WorkspacePublicID+"/invites",
		host.AccessToken, map[string]any{
			"role":      "member",
			"expiresIn": 1,
		}, &created)
	require.NotEmpty(t, created.Token)

	// Wait for the invite to expire.
	time.Sleep(2 * time.Second)

	// Accept should fail.
	status, _ := doJSONStatus(t, http.MethodPost,
		testServerURL+"/invites/"+created.Token+"/accept",
		guest.AccessToken, nil)
	require.GreaterOrEqual(t, status, 400,
		"accepting an expired invite must return non-2xx")
}

// TestWorkspaceInviteRequiresAuth verifies that POST /invites/{token}/accept
// returns 401 when no authorization header is provided.
func TestWorkspaceInviteRequiresAuth(t *testing.T) {
	bootstrap(t)
	t.Parallel()

	host := newTenant(t)

	// Create an invite.
	var created struct {
		Token string `json:"token"`
	}
	doJSON(t, http.MethodPost,
		testServerURL+"/workspaces/"+host.WorkspacePublicID+"/invites",
		host.AccessToken, map[string]any{
			"role": "member",
		}, &created)
	require.NotEmpty(t, created.Token)

	// Accept without auth.
	status, _ := doJSONStatus(t, http.MethodPost,
		testServerURL+"/invites/"+created.Token+"/accept",
		"", nil)
	require.Equal(t, http.StatusUnauthorized, status,
		"accepting invite without auth must return 401")
}

// TestWorkspaceInviteRequiresAdmin verifies that a member (non-admin)
// cannot create invite links.
func TestWorkspaceInviteRequiresAdmin(t *testing.T) {
	bootstrap(t)
	t.Parallel()

	host := newTenant(t)
	member := newTenant(t)

	// Add member to host's workspace with role "member" (via direct invite).
	doJSON(t, http.MethodPost,
		testServerURL+"/workspaces/"+host.WorkspacePublicID+"/members",
		host.AccessToken, map[string]any{
			"email": member.Email,
			"role":  "member",
		}, nil)

	// Member tries to create an invite link (requires admin+).
	status, body := doJSONStatus(t, http.MethodPost,
		testServerURL+"/workspaces/"+host.WorkspacePublicID+"/invites",
		member.AccessToken, map[string]any{
			"role": "member",
		})
	require.Equal(t, http.StatusForbidden, status,
		fmt.Sprintf("member creating invite must be 403, got body=%s", string(body)))
}
