package e2e

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestDoubleDeletePage verifies that deleting a page twice does not
// cause a 500 error (consistent 404 or 200 on second attempt).
func TestDoubleDeletePage(t *testing.T) {
	bootstrap(t)
	t.Parallel()
	tt := newTenant(t)

	base := testServerURL + "/workspaces/" + tt.WorkspacePublicID + "/pages"

	var page struct {
		ID string `json:"id"`
	}
	doJSON(t, http.MethodPost, base, tt.AccessToken,
		map[string]any{"title": "Twice Deleted"}, &page)

	// First delete succeeds.
	status, _ := doJSONStatus(t, http.MethodDelete, base+"/"+page.ID,
		tt.AccessToken, nil)
	require.Equal(t, http.StatusOK, status)

	// Second delete should not cause 500.
	status, _ = doJSONStatus(t, http.MethodDelete, base+"/"+page.ID,
		tt.AccessToken, nil)
	require.NotEqual(t, http.StatusInternalServerError, status,
		"double delete must not cause 500")
	require.True(t, status == http.StatusOK || status == http.StatusNotFound,
		"double delete should return 200 (idempotent) or 404, got %d", status)
}

// TestDoubleDeleteTimebox verifies that deleting a timebox twice does
// not cause a 500 error.
func TestDoubleDeleteTimebox(t *testing.T) {
	bootstrap(t)
	t.Parallel()
	tt := newTenant(t)

	base := testServerURL + "/workspaces/" + tt.WorkspacePublicID + "/timeboxes"

	var tb struct {
		ID string `json:"id"`
	}
	doJSON(t, http.MethodPost, base, tt.AccessToken, map[string]any{
		"name": "Twice Deleted Sprint", "startsOn": "2025-10-01", "endsOn": "2025-10-14",
	}, &tb)

	status, _ := doJSONStatus(t, http.MethodDelete, base+"/"+tb.ID,
		tt.AccessToken, nil)
	require.Equal(t, http.StatusOK, status)

	status, _ = doJSONStatus(t, http.MethodDelete, base+"/"+tb.ID,
		tt.AccessToken, nil)
	require.NotEqual(t, http.StatusInternalServerError, status,
		"double delete must not cause 500")
}

// TestDoubleDeleteWidget verifies that deleting a widget twice does
// not cause a 500 error.
func TestDoubleDeleteWidget(t *testing.T) {
	bootstrap(t)
	t.Parallel()
	tt := newTenant(t)

	base := testServerURL + "/workspaces/" + tt.WorkspacePublicID + "/dashboard/widgets"

	var w struct {
		ID string `json:"id"`
	}
	doJSON(t, http.MethodPost, base, tt.AccessToken, map[string]any{
		"widgetType": "task_summary", "title": "Temp",
		"positionX": 0, "positionY": 0, "width": 2, "height": 2,
	}, &w)

	status, _ := doJSONStatus(t, http.MethodDelete, base+"/"+w.ID,
		tt.AccessToken, nil)
	require.Equal(t, http.StatusOK, status)

	status, _ = doJSONStatus(t, http.MethodDelete, base+"/"+w.ID,
		tt.AccessToken, nil)
	require.NotEqual(t, http.StatusInternalServerError, status,
		"double delete must not cause 500")
}

// TestRevokedInviteNotUsable verifies that a revoked invite link
// cannot be accepted.
func TestRevokedInviteNotUsable(t *testing.T) {
	bootstrap(t)
	t.Parallel()

	owner := newTenant(t)
	joiner := newTenant(t)

	base := testServerURL + "/workspaces/" + owner.WorkspacePublicID + "/invites"

	// Create and revoke an invite.
	var created struct {
		Invite struct {
			ID string `json:"id"`
		} `json:"invite"`
		Token string `json:"token"`
	}
	doJSON(t, http.MethodPost, base, owner.AccessToken,
		map[string]any{"role": "member"}, &created)

	doJSON(t, http.MethodDelete, base+"/"+created.Invite.ID,
		owner.AccessToken, nil, nil)

	// Joiner tries to accept the revoked invite.
	status, _ := doJSONStatus(t, http.MethodPost,
		testServerURL+"/invites/"+created.Token+"/accept",
		joiner.AccessToken, nil)
	require.GreaterOrEqual(t, status, 400,
		"revoked invite must not be accepted")

	// Info endpoint should also fail.
	status, _ = doJSONStatus(t, http.MethodGet,
		testServerURL+"/invites/"+created.Token+"/info",
		"", nil)
	require.GreaterOrEqual(t, status, 400,
		"revoked invite info must not be accessible")
}

// TestAcceptInviteIdempotent verifies that accepting the same invite
// link twice (as the same user) does not create duplicate members.
func TestAcceptInviteIdempotent(t *testing.T) {
	bootstrap(t)
	t.Parallel()

	owner := newTenant(t)
	joiner := newTenant(t)

	var invite struct {
		Token string `json:"token"`
	}
	doJSON(t, http.MethodPost,
		testServerURL+"/workspaces/"+owner.WorkspacePublicID+"/invites",
		owner.AccessToken, map[string]any{"role": "member"}, &invite)

	// Accept once.
	doJSON(t, http.MethodPost,
		testServerURL+"/invites/"+invite.Token+"/accept",
		joiner.AccessToken, nil, nil)

	// Accept again — should be idempotent (not error or create duplicate).
	status, _ := doJSONStatus(t, http.MethodPost,
		testServerURL+"/invites/"+invite.Token+"/accept",
		joiner.AccessToken, nil)
	require.Less(t, status, 500,
		"double accept must not cause 500")

	// Verify member count is correct (owner + joiner = 2, not 3).
	var members struct {
		Total int64 `json:"total"`
	}
	doJSON(t, http.MethodGet,
		testServerURL+"/workspaces/"+owner.WorkspacePublicID+"/members",
		owner.AccessToken, nil, &members)
	require.Equal(t, int64(2), members.Total,
		"double accept must not create duplicate member")
}
