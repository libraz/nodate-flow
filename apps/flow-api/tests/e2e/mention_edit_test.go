package e2e

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestCommentEditDoesNotRenotifyMentions holds that a comment edit which
// keeps an existing mention appends no mention.created event, while an
// edit that names someone new appends exactly one.
func TestCommentEditDoesNotRenotifyMentions(t *testing.T) {
	bootstrap(t)
	t.Parallel()

	tt := newTenant(t)

	var task struct {
		ID string `json:"id"`
	}
	doJSON(t, http.MethodPost, testServerURL+"/tasks", tt.AccessToken, map[string]any{
		"projectId": tt.ProjectPublicID,
		"title":     "Mention host",
	}, &task)
	require.NotEmpty(t, task.ID)

	mention := "@[Me](user:" + tt.UserPublicID + ")"
	mentionEvents := func() int {
		var n int
		require.NoError(t, testDB.QueryRow(
			`SELECT COUNT(*) FROM events
			 WHERE type = 'mention.created'
			   AND workspace_id = (SELECT id FROM workspaces WHERE public_id = UUID_TO_BIN(?, 0))`,
			tt.WorkspacePublicID).Scan(&n))
		return n
	}

	var comment struct {
		ID string `json:"id"`
	}
	doJSON(t, http.MethodPost, testServerURL+"/tasks/"+task.ID+"/comments",
		tt.AccessToken, map[string]any{"body": "hello " + mention}, &comment)
	require.NotEmpty(t, comment.ID)
	require.Equal(t, 1, mentionEvents())

	doJSON(t, http.MethodPatch, testServerURL+"/tasks/"+task.ID+"/comments/"+comment.ID,
		tt.AccessToken, map[string]any{"body": "hello again " + mention}, nil)
	require.Equal(t, 1, mentionEvents(), "an edit keeping the mention must not append another event")

	doJSON(t, http.MethodPatch, testServerURL+"/tasks/"+task.ID+"/comments/"+comment.ID,
		tt.AccessToken, map[string]any{"body": "no mention"}, nil)
	doJSON(t, http.MethodPatch, testServerURL+"/tasks/"+task.ID+"/comments/"+comment.ID,
		tt.AccessToken, map[string]any{"body": "back " + mention}, nil)
	require.Equal(t, 2, mentionEvents(), "naming the user again after removal is a new mention")
}
