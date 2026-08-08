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

	// Outsider tries to comment. The comment routes hang off the task
	// access gate, so the refusal is the task gate's 403 rather than
	// anything comment-specific.
	status, body := doJSONStatus(t, http.MethodPost,
		testServerURL+"/tasks/"+task.ID+"/comments",
		outsider.AccessToken, map[string]any{"body": "I shouldn't be here"})
	requireDenied(t, status, body, http.StatusForbidden, "WS.TASK.ACCESS_DENIED",
		"outsider commenting on another workspace's task")

	// No comment was recorded.
	var comments struct {
		Total int64 `json:"total"`
	}
	doJSON(t, http.MethodGet, testServerURL+"/tasks/"+task.ID+"/comments",
		owner.AccessToken, nil, &comments)
	require.Equal(t, int64(0), comments.Total,
		"the refused POST must not have written a comment")
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
	//
	// Note which gate answers: the member is not in the task's project,
	// so RequireProjectMember refuses before the comment authorship
	// check is ever consulted. Asserting the code makes that visible
	// instead of letting the test read as if it covered authorship.
	status, body := doJSONStatus(t, http.MethodPatch,
		testServerURL+"/tasks/"+task.ID+"/comments/"+comment.ID,
		member.AccessToken, map[string]any{"body": "Hijacked"})
	requireDenied(t, status, body, http.StatusForbidden, "WS.PROJECT.ACCESS_DENIED",
		"a non-author editing another user's comment")

	// The comment body is untouched.
	var listed struct {
		Comments []struct {
			ID   string `json:"id"`
			Body string `json:"body"`
		} `json:"comments"`
	}
	doJSON(t, http.MethodGet, testServerURL+"/tasks/"+task.ID+"/comments",
		owner.AccessToken, nil, &listed)
	require.Len(t, listed.Comments, 1)
	require.Equal(t, "Owner's thought", listed.Comments[0].Body,
		"the refused PATCH must not have rewritten the comment")
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

	// Member (not admin, not author) tries to delete. As with the edit
	// path, project membership is what refuses first.
	status, body := doJSONStatus(t, http.MethodDelete,
		testServerURL+"/tasks/"+task.ID+"/comments/"+comment.ID,
		member.AccessToken, nil)
	requireDenied(t, status, body, http.StatusForbidden, "WS.PROJECT.ACCESS_DENIED",
		"a non-author non-admin deleting a comment")

	// The comment is still there.
	var comments struct {
		Total int64 `json:"total"`
	}
	doJSON(t, http.MethodGet, testServerURL+"/tasks/"+task.ID+"/comments",
		owner.AccessToken, nil, &comments)
	require.Equal(t, int64(1), comments.Total,
		"the refused DELETE must not have removed the comment")
}

// TestProjectMemberCannotEditOthersComment covers the authorship rule
// itself, which the two tests above never reach.
//
// Both of them use a plain workspace member, and a plain workspace
// member is refused by project membership before the comment handler
// runs — so they prove the project gate works and say nothing about who
// may edit a comment. Here the non-author is an editor on the project,
// clears every gate, and is turned away by the authorship check.
func TestProjectMemberCannotEditOthersComment(t *testing.T) {
	bootstrap(t)
	t.Parallel()

	owner := newTenant(t)
	editor := newTenant(t)

	var invite struct {
		Token string `json:"token"`
	}
	doJSON(t, http.MethodPost,
		testServerURL+"/workspaces/"+owner.WorkspacePublicID+"/invites",
		owner.AccessToken, map[string]any{"role": "member"}, &invite)
	doJSON(t, http.MethodPost,
		testServerURL+"/invites/"+invite.Token+"/accept",
		editor.AccessToken, nil, nil)

	doJSON(t, http.MethodPost,
		testServerURL+"/projects/"+owner.ProjectPublicID+"/members",
		owner.AccessToken, map[string]any{
			"userId": editor.UserPublicID,
			"role":   "editor",
		}, nil)

	var task struct {
		ID string `json:"id"`
	}
	doJSON(t, http.MethodPost, testServerURL+"/tasks", owner.AccessToken,
		map[string]any{
			"projectId":  owner.ProjectPublicID,
			"title":      "Authorship Check",
			"visibility": "public",
		}, &task)

	var comment struct {
		ID string `json:"id"`
	}
	doJSON(t, http.MethodPost,
		testServerURL+"/tasks/"+task.ID+"/comments",
		owner.AccessToken, map[string]any{"body": "Owner's thought"}, &comment)

	// The editor can read the thread, which is what makes the refusal
	// below attributable to authorship rather than to access.
	var comments struct {
		Total int64 `json:"total"`
	}
	doJSON(t, http.MethodGet, testServerURL+"/tasks/"+task.ID+"/comments",
		editor.AccessToken, nil, &comments)
	require.Equal(t, int64(1), comments.Total,
		"fixture: the project editor must be able to read the thread")

	status, body := doJSONStatus(t, http.MethodPatch,
		testServerURL+"/tasks/"+task.ID+"/comments/"+comment.ID,
		editor.AccessToken, map[string]any{"body": "Hijacked"})
	// The authorship check reuses the task access code rather than
	// minting a comment-specific one, so the pair to pin is the same
	// 403 WS.TASK.ACCESS_DENIED — reached this time from the handler,
	// not from the middleware.
	requireDenied(t, status, body, http.StatusForbidden, "WS.TASK.ACCESS_DENIED",
		"a project editor editing someone else's comment")

	var listed struct {
		Comments []struct {
			Body string `json:"body"`
		} `json:"comments"`
	}
	doJSON(t, http.MethodGet, testServerURL+"/tasks/"+task.ID+"/comments",
		owner.AccessToken, nil, &listed)
	require.Len(t, listed.Comments, 1)
	require.Equal(t, "Owner's thought", listed.Comments[0].Body,
		"the refused PATCH must not have rewritten the comment")
}
