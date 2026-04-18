package e2e

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestProjectMemberCRUD exercises adding, listing, and removing
// a project member.
func TestProjectMemberCRUD(t *testing.T) {
	bootstrap(t)
	t.Parallel()

	owner := newTenant(t)
	member := newTenant(t)

	// Invite member to the workspace first.
	var invite struct {
		Token string `json:"token"`
	}
	doJSON(t, http.MethodPost,
		testServerURL+"/workspaces/"+owner.WorkspacePublicID+"/invites",
		owner.AccessToken, map[string]any{"role": "member"}, &invite)
	doJSON(t, http.MethodPost,
		testServerURL+"/invites/"+invite.Token+"/accept",
		member.AccessToken, nil, nil)

	prjURL := testServerURL + "/projects/" + owner.ProjectPublicID

	// Add member to project.
	var added struct {
		UserID string `json:"userId"`
		Role   string `json:"role"`
	}
	doJSON(t, http.MethodPost, prjURL+"/members",
		owner.AccessToken, map[string]any{
			"userId": member.UserPublicID,
			"role":   "editor",
		}, &added)
	require.Equal(t, member.UserPublicID, added.UserID)
	require.Equal(t, "editor", added.Role)

	// List project members — should include both owner and member.
	var list struct {
		Total   int64 `json:"total"`
		Members []struct {
			UserID string `json:"userId"`
			Role   string `json:"role"`
		} `json:"members"`
	}
	doJSON(t, http.MethodGet, prjURL+"/members",
		owner.AccessToken, nil, &list)
	require.GreaterOrEqual(t, list.Total, int64(2))

	found := false
	for _, m := range list.Members {
		if m.UserID == member.UserPublicID {
			found = true
			require.Equal(t, "editor", m.Role)
		}
	}
	require.True(t, found, "added member must appear in list")

	// Remove member from project.
	var removed struct {
		Ok bool `json:"ok"`
	}
	doJSON(t, http.MethodDelete,
		prjURL+"/members/"+member.UserPublicID,
		owner.AccessToken, nil, &removed)
	require.True(t, removed.Ok)

	// Verify member is gone.
	var after struct {
		Members []struct {
			UserID string `json:"userId"`
		} `json:"members"`
	}
	doJSON(t, http.MethodGet, prjURL+"/members",
		owner.AccessToken, nil, &after)
	for _, m := range after.Members {
		require.NotEqual(t, member.UserPublicID, m.UserID,
			"removed member must not appear in list")
	}
}

// TestProjectMemberAddIdempotent verifies that adding the same
// user to a project twice does not error — it is idempotent.
func TestProjectMemberAddIdempotent(t *testing.T) {
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

	prjURL := testServerURL + "/projects/" + owner.ProjectPublicID

	body := map[string]any{"userId": member.UserPublicID, "role": "editor"}

	// First add.
	doJSON(t, http.MethodPost, prjURL+"/members",
		owner.AccessToken, body, nil)

	// Second add — should succeed, not 409.
	status, _ := doJSONStatus(t, http.MethodPost, prjURL+"/members",
		owner.AccessToken, body)
	require.Less(t, status, 300,
		"adding same project member twice must be idempotent")
}

// TestProjectMemberRequiresWorkspaceMembership verifies that only
// workspace members can be added to a project.
func TestProjectMemberRequiresWorkspaceMembership(t *testing.T) {
	bootstrap(t)
	t.Parallel()

	owner := newTenant(t)
	outsider := newTenant(t)

	prjURL := testServerURL + "/projects/" + owner.ProjectPublicID

	status, _ := doJSONStatus(t, http.MethodPost, prjURL+"/members",
		owner.AccessToken, map[string]any{
			"userId": outsider.UserPublicID,
			"role":   "editor",
		})
	require.GreaterOrEqual(t, status, 400,
		"non-workspace member must not be added to project")
}
