package e2e

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestCommentLifecycle exercises the comment CRUD stream on a task.
func TestCommentLifecycle(t *testing.T) {
	bootstrap(t)
	t.Parallel()

	tt := newTenant(t)

	// Create a task to host the comments.
	var task struct {
		ID string `json:"id"`
	}
	doJSON(t, http.MethodPost, testServerURL+"/tasks", tt.AccessToken, map[string]any{
		"projectId": tt.ProjectPublicID,
		"title":     "Comment host",
	}, &task)
	require.NotEmpty(t, task.ID)

	// Add a comment.
	var comment struct {
		ID   string `json:"id"`
		Body string `json:"body"`
	}
	doJSON(t, http.MethodPost, testServerURL+"/tasks/"+task.ID+"/comments",
		tt.AccessToken, map[string]any{"body": "first comment"}, &comment)
	require.NotEmpty(t, comment.ID)
	require.Equal(t, "first comment", comment.Body)

	// List comments.
	var list struct {
		Total    int64 `json:"total"`
		Comments []struct {
			ID   string `json:"id"`
			Body string `json:"body"`
		} `json:"comments"`
	}
	doJSON(t, http.MethodGet, testServerURL+"/tasks/"+task.ID+"/comments", tt.AccessToken, nil, &list)
	require.Equal(t, int64(1), list.Total)
	require.Len(t, list.Comments, 1)
	require.Equal(t, comment.ID, list.Comments[0].ID)

	// Edit the comment.
	var edited struct {
		Body string `json:"body"`
	}
	doJSON(t, http.MethodPatch,
		testServerURL+"/tasks/"+task.ID+"/comments/"+comment.ID,
		tt.AccessToken, map[string]any{"body": "edited comment"}, &edited)
	require.Equal(t, "edited comment", edited.Body)

	// Delete the comment.
	status, _ := doJSONStatus(t, http.MethodDelete,
		testServerURL+"/tasks/"+task.ID+"/comments/"+comment.ID,
		tt.AccessToken, nil)
	require.Equal(t, http.StatusOK, status)

	// Events rows for this workspace must be non-zero.
	var events int
	err := testDB.QueryRow(
		`SELECT COUNT(*) FROM events
		 WHERE workspace_id = (SELECT id FROM workspaces WHERE public_id = UUID_TO_BIN(?, 0))`,
		tt.WorkspacePublicID).Scan(&events)
	require.NoError(t, err)
	require.Greater(t, events, 0, "comment lifecycle must emit events")
}
