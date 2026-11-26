package e2e

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/libraz/nodate-flow/apps/flow-api/tests/helpers"
)

// TestMemberEmailsAreForTheWorkspaceOwnerOnly verifies that every listing of
// people returns another member's address only to the workspace owner. An
// admin, like a plain member, reads only their own, and the field is absent
// rather than empty.
func TestMemberEmailsAreForTheWorkspaceOwnerOnly(t *testing.T) {
	bootstrap(t)
	t.Parallel()

	owner := newTenant(t)
	admin := joinWorkspaceAs(t, owner, "admin")
	member := joinWorkspaceAs(t, owner, "member")

	taskID := createTaskForAttachment(t, owner.AccessToken, owner.ProjectPublicID, "Email visibility")
	for _, u := range []*helpers.TestTenant{admin, member} {
		doJSON(t, http.MethodPost, testServerURL+"/projects/"+owner.ProjectPublicID+"/members",
			owner.AccessToken, map[string]any{"userId": u.UserPublicID, "role": "editor"}, nil)
		doJSON(t, http.MethodPost, testServerURL+"/tasks/"+taskID+"/actors", owner.AccessToken,
			map[string]any{"userId": u.UserPublicID, "role": "assignee"}, nil)
	}

	listings := []struct{ name, url, key string }{
		{"workspace members", testServerURL + "/workspaces/" + owner.WorkspacePublicID + "/members", "members"},
		{"project members", testServerURL + "/projects/" + owner.ProjectPublicID + "/members", "members"},
		{"task actors", testServerURL + "/tasks/" + taskID + "/actors", "actors"},
	}
	emailOf := map[string]string{
		owner.UserPublicID:  owner.Email,
		admin.UserPublicID:  admin.Email,
		member.UserPublicID: member.Email,
	}
	for _, l := range listings {
		for _, caller := range []*helpers.TestTenant{owner, admin, member} {
			var body map[string]any
			doJSON(t, http.MethodGet, l.url, caller.AccessToken, nil, &body)
			rows, _ := body[l.key].([]any)
			require.NotEmpty(t, rows, "%s as %s", l.name, caller.Email)
			for _, r := range rows {
				row, ok := r.(map[string]any)
				require.True(t, ok)
				userID, _ := row["userId"].(string)
				if caller == owner || userID == caller.UserPublicID {
					require.Equal(t, emailOf[userID], row["email"], "%s: %s reading %s", l.name, caller.Email, userID)
					continue
				}
				require.NotContains(t, row, "email", "%s: %s must not read %s's address", l.name, caller.Email, userID)
			}
		}
	}
}
