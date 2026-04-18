package e2e

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestOutsiderCannotCommentOnTask verifies that a user who is not a
// member of the task's workspace cannot post comments.
func TestOutsiderCannotCommentOnTask(t *testing.T) {
	bootstrap(t)
	t.Parallel()

	owner := newTenant(t)
	outsider := newTenant(t)

	var task struct {
		ID string `json:"id"`
	}
	doJSON(t, http.MethodPost, testServerURL+"/tasks", owner.AccessToken,
		map[string]any{"projectId": owner.ProjectPublicID, "title": "Commented Task"}, &task)

	// Outsider tries to comment.
	status, _ := doJSONStatus(t, http.MethodPost,
		testServerURL+"/tasks/"+task.ID+"/comments",
		outsider.AccessToken, map[string]any{"body": "I shouldn't be here"})
	require.GreaterOrEqual(t, status, 403,
		"outsider must not comment on another workspace's task")
}

// TestCommentAuthorCanEdit verifies that a comment author can edit
// their own comment.
func TestCommentAuthorCanEdit(t *testing.T) {
	bootstrap(t)
	t.Parallel()
	tt := newTenant(t)

	var task struct {
		ID string `json:"id"`
	}
	doJSON(t, http.MethodPost, testServerURL+"/tasks", tt.AccessToken,
		map[string]any{"projectId": tt.ProjectPublicID, "title": "Comment Edit Test"}, &task)

	// Create a comment.
	var comment struct {
		ID   string `json:"id"`
		Body string `json:"body"`
	}
	doJSON(t, http.MethodPost,
		testServerURL+"/tasks/"+task.ID+"/comments",
		tt.AccessToken, map[string]any{"body": "Original comment"}, &comment)
	require.NotEmpty(t, comment.ID)

	// Edit the comment.
	var edited struct {
		Body string `json:"body"`
	}
	doJSON(t, http.MethodPatch,
		testServerURL+"/tasks/"+task.ID+"/comments/"+comment.ID,
		tt.AccessToken, map[string]any{"body": "Edited comment"}, &edited)
	require.Equal(t, "Edited comment", edited.Body)
}

// TestNonAuthorCannotEditComment verifies that a workspace member who
// did not author a comment cannot edit it.
func TestNonAuthorCannotEditComment(t *testing.T) {
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
			"title":      "Shared Task",
			"visibility": "public",
		}, &task)

	// Owner posts a comment.
	var comment struct {
		ID string `json:"id"`
	}
	doJSON(t, http.MethodPost,
		testServerURL+"/tasks/"+task.ID+"/comments",
		owner.AccessToken, map[string]any{"body": "Owner's thought"}, &comment)

	// Member tries to edit owner's comment.
	status, _ := doJSONStatus(t, http.MethodPatch,
		testServerURL+"/tasks/"+task.ID+"/comments/"+comment.ID,
		member.AccessToken, map[string]any{"body": "Hijacked"})
	require.GreaterOrEqual(t, status, 403,
		"non-author must not edit another user's comment")
}

// TestAdminCanDeleteAnyComment verifies that a workspace admin can
// delete comments authored by other users. We use two admin-level
// users (owner + invited admin) to avoid project-membership gates.
func TestAdminCanDeleteAnyComment(t *testing.T) {
	bootstrap(t)
	t.Parallel()

	owner := newTenant(t)
	admin := newTenant(t)

	// Invite as admin so they have full task access.
	var invite struct {
		Token string `json:"token"`
	}
	doJSON(t, http.MethodPost,
		testServerURL+"/workspaces/"+owner.WorkspacePublicID+"/invites",
		owner.AccessToken, map[string]any{"role": "admin"}, &invite)
	doJSON(t, http.MethodPost,
		testServerURL+"/invites/"+invite.Token+"/accept",
		admin.AccessToken, nil, nil)

	var task struct {
		ID string `json:"id"`
	}
	doJSON(t, http.MethodPost, testServerURL+"/tasks", owner.AccessToken,
		map[string]any{
			"projectId":  owner.ProjectPublicID,
			"title":      "Admin Delete Comment Test",
			"visibility": "public",
		}, &task)

	// Owner posts a comment.
	var comment struct {
		ID string `json:"id"`
	}
	doJSON(t, http.MethodPost,
		testServerURL+"/tasks/"+task.ID+"/comments",
		owner.AccessToken, map[string]any{"body": "Owner's comment"}, &comment)
	require.NotEmpty(t, comment.ID)

	// Admin deletes owner's comment.
	status, _ := doJSONStatus(t, http.MethodDelete,
		testServerURL+"/tasks/"+task.ID+"/comments/"+comment.ID,
		admin.AccessToken, nil)
	require.Equal(t, http.StatusOK, status,
		"admin must be able to delete any user's comment")

	// Comment should no longer appear.
	var comments struct {
		Total int64 `json:"total"`
	}
	doJSON(t, http.MethodGet,
		testServerURL+"/tasks/"+task.ID+"/comments",
		owner.AccessToken, nil, &comments)
	require.Equal(t, int64(0), comments.Total,
		"deleted comment must not appear in list")
}

// TestAuthorCanDeleteOwnComment verifies that a comment author can
// delete their own comment.
func TestAuthorCanDeleteOwnComment(t *testing.T) {
	bootstrap(t)
	t.Parallel()
	tt := newTenant(t)

	var task struct {
		ID string `json:"id"`
	}
	doJSON(t, http.MethodPost, testServerURL+"/tasks", tt.AccessToken,
		map[string]any{"projectId": tt.ProjectPublicID, "title": "Self Delete Comment"}, &task)

	var comment struct {
		ID string `json:"id"`
	}
	doJSON(t, http.MethodPost,
		testServerURL+"/tasks/"+task.ID+"/comments",
		tt.AccessToken, map[string]any{"body": "My own comment"}, &comment)

	status, _ := doJSONStatus(t, http.MethodDelete,
		testServerURL+"/tasks/"+task.ID+"/comments/"+comment.ID,
		tt.AccessToken, nil)
	require.Equal(t, http.StatusOK, status,
		"author must be able to delete own comment")
}

// TestNonAuthorNonAdminCannotDeleteComment verifies that only the
// author or a workspace admin can delete a comment.
func TestNonAuthorNonAdminCannotDeleteComment(t *testing.T) {
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
			"title":      "Comment Delete Test",
			"visibility": "public",
		}, &task)

	var comment struct {
		ID string `json:"id"`
	}
	doJSON(t, http.MethodPost,
		testServerURL+"/tasks/"+task.ID+"/comments",
		owner.AccessToken, map[string]any{"body": "Owner's comment"}, &comment)

	// Member (not admin, not author) tries to delete.
	status, _ := doJSONStatus(t, http.MethodDelete,
		testServerURL+"/tasks/"+task.ID+"/comments/"+comment.ID,
		member.AccessToken, nil)
	require.GreaterOrEqual(t, status, 403,
		"non-author non-admin must not delete comment")
}
