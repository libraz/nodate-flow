package e2e

import (
	"net/http"
	"testing"

	"github.com/google/uuid"
)

// TestProjectMemberRoutesResolveUsersInsideTheWorkspace holds the project
// member routes to the caller's workspace: a user of another workspace and
// an id that names no one get the same not-found from both add and
// remove, so neither route can be used to probe accounts instance-wide.
func TestProjectMemberRoutesResolveUsersInsideTheWorkspace(t *testing.T) {
	bootstrap(t)
	t.Parallel()

	owner := newTenant(t)
	stranger := newTenant(t)
	base := testServerURL + "/projects/" + owner.ProjectPublicID + "/members"

	for _, target := range []struct {
		label string
		id    string
	}{
		{"user of another workspace", stranger.UserPublicID},
		{"unknown user", uuid.Must(uuid.NewV7()).String()},
	} {
		status, body := doJSONStatus(t, http.MethodDelete, base+"/"+target.id, owner.AccessToken, nil)
		requireDenied(t, status, body, http.StatusNotFound, "WS.MEMBER.NOT_FOUND", "remove "+target.label)

		status, body = doJSONStatus(t, http.MethodPost, base, owner.AccessToken,
			map[string]any{"userId": target.id, "role": "editor"})
		requireDenied(t, status, body, http.StatusNotFound, "WS.MEMBER.NOT_FOUND", "add "+target.label)
	}

	// The positive control: a workspace member is still added and removed.
	member := seedWorkspaceMemberTenant(t, owner)
	doJSON(t, http.MethodPost, base, owner.AccessToken, map[string]any{"userId": member.UserPublicID, "role": "editor"}, nil)
	doJSON(t, http.MethodDelete, base+"/"+member.UserPublicID, owner.AccessToken, nil, nil)
}
