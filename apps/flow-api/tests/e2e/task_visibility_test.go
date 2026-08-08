package e2e

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestPrivateTaskHiddenFromNonActor verifies that a private task is
// invisible to a workspace member who is not an actor on the task.
// The response must be 404 (not 403) to avoid leaking existence.
func TestPrivateTaskHiddenFromNonActor(t *testing.T) {
	bootstrap(t)
	t.Parallel()

	owner := newTenant(t)
	member := newTenant(t)

	// Invite member to owner's workspace.
	var invite struct {
		Token string `json:"token"`
	}
	doJSON(t, http.MethodPost,
		testServerURL+"/workspaces/"+owner.WorkspacePublicID+"/invites",
		owner.AccessToken, map[string]any{"role": "member"}, &invite)
	doJSON(t, http.MethodPost,
		testServerURL+"/invites/"+invite.Token+"/accept",
		member.AccessToken, nil, nil)

	// Owner creates a private task.
	var task struct {
		ID string `json:"id"`
	}
	doJSON(t, http.MethodPost, testServerURL+"/tasks", owner.AccessToken,
		map[string]any{
			"projectId":  owner.ProjectPublicID,
			"title":      "Owner's Private Task",
			"visibility": "private",
		}, &task)

	// Owner can access it.
	status, _ := doJSONStatus(t, http.MethodGet,
		testServerURL+"/tasks/"+task.ID, owner.AccessToken, nil)
	require.Equal(t, http.StatusOK, status,
		"owner must see their private task")

	// Member cannot see it. Unlike the cross-workspace case, a private
	// task inside a workspace the actor belongs to is concealed rather
	// than denied: the refusal is 404 WS.TASK.NOT_FOUND so a member
	// cannot use the status to confirm that the task exists at all.
	status, body := doJSONStatus(t, http.MethodGet,
		testServerURL+"/tasks/"+task.ID, member.AccessToken, nil)
	requireDenied(t, status, body, http.StatusNotFound, "WS.TASK.NOT_FOUND",
		"a non-actor member reading a private task")
	require.NotContains(t, string(body), "Owner's Private Task",
		"a refusal must not carry the task it concealed")
}

// TestPrivateTaskNotInMemberListView verifies that a private task does
// not appear in task list results for non-actor workspace members.
func TestPrivateTaskNotInMemberListView(t *testing.T) {
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

	// Owner creates a public task and a private task.
	var publicTask, privateTask struct {
		ID string `json:"id"`
	}
	doJSON(t, http.MethodPost, testServerURL+"/tasks", owner.AccessToken,
		map[string]any{
			"projectId":  owner.ProjectPublicID,
			"title":      "Public Task",
			"visibility": "public",
		}, &publicTask)
	doJSON(t, http.MethodPost, testServerURL+"/tasks", owner.AccessToken,
		map[string]any{
			"projectId":  owner.ProjectPublicID,
			"title":      "Private Task",
			"visibility": "private",
		}, &privateTask)

	// Member lists tasks — should see public but not private.
	var list struct {
		Tasks []struct {
			ID string `json:"id"`
		} `json:"tasks"`
	}
	doJSON(t, http.MethodGet, testServerURL+"/tasks?workspaceId="+owner.WorkspacePublicID,
		member.AccessToken, nil, &list)

	foundPublic := false
	foundPrivate := false
	for _, task := range list.Tasks {
		if task.ID == publicTask.ID {
			foundPublic = true
		}
		if task.ID == privateTask.ID {
			foundPrivate = true
		}
	}
	require.True(t, foundPublic, "member must see public task in list")
	require.False(t, foundPrivate, "member must NOT see private task in list")
}

// TestPrivateTaskUpdateDeniedForNonActor verifies that a workspace
// member who is not an actor cannot update a private task.
func TestPrivateTaskUpdateDeniedForNonActor(t *testing.T) {
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

	var task struct {
		ID string `json:"id"`
	}
	doJSON(t, http.MethodPost, testServerURL+"/tasks", owner.AccessToken,
		map[string]any{
			"projectId":  owner.ProjectPublicID,
			"title":      "Untouchable",
			"visibility": "private",
		}, &task)

	// Member cannot update it — concealed the same way as the read path.
	status, body := doJSONStatus(t, http.MethodPatch,
		testServerURL+"/tasks/"+task.ID, member.AccessToken,
		map[string]any{"title": "Hacked"})
	requireDenied(t, status, body, http.StatusNotFound, "WS.TASK.NOT_FOUND",
		"a non-actor member updating a private task")

	// Verify title unchanged.
	var got struct {
		Title string `json:"title"`
	}
	doJSON(t, http.MethodGet, testServerURL+"/tasks/"+task.ID,
		owner.AccessToken, nil, &got)
	require.Equal(t, "Untouchable", got.Title)
}

// TestAdminBypassesTaskVisibility verifies that a workspace admin can
// see all tasks regardless of visibility level.
func TestAdminBypassesTaskVisibility(t *testing.T) {
	bootstrap(t)
	t.Parallel()

	owner := newTenant(t)
	admin := newTenant(t)

	var invite struct {
		Token string `json:"token"`
	}
	doJSON(t, http.MethodPost,
		testServerURL+"/workspaces/"+owner.WorkspacePublicID+"/invites",
		owner.AccessToken, map[string]any{"role": "admin"}, &invite)
	doJSON(t, http.MethodPost,
		testServerURL+"/invites/"+invite.Token+"/accept",
		admin.AccessToken, nil, nil)

	// Owner creates a private task.
	var task struct {
		ID string `json:"id"`
	}
	doJSON(t, http.MethodPost, testServerURL+"/tasks", owner.AccessToken,
		map[string]any{
			"projectId":  owner.ProjectPublicID,
			"title":      "Admin Visible",
			"visibility": "private",
		}, &task)

	// Admin can see the private task.
	status, _ := doJSONStatus(t, http.MethodGet,
		testServerURL+"/tasks/"+task.ID, admin.AccessToken, nil)
	require.Equal(t, http.StatusOK, status,
		"admin must see private tasks")
}
