package e2e

import (
	"net/http"
	"regexp"
	"testing"

	"github.com/stretchr/testify/require"
)

// uuidV7Regex matches the canonical hyphenated UUID v7 text form.
// Bug 2026-04-23-api-task-create-response-raw-uuid-bytes regressed
// `createdByUserId` into raw BINARY(16) bytes wrapped in a JSON
// string; asserting this regex guards against that class of bug.
var uuidV7Regex = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)

// TestCreateTaskAutoAttachesCreatorAsAssignee verifies two linked
// behaviors of POST /tasks:
//
//  1. The response body serializes `createdByUserId` as a canonical
//     UUID v7 text string, not the raw BINARY(16) bytes the column
//     holds internally.
//  2. When the request body omits `actors`, the authenticated caller
//     is auto-attached as the sole `assignee`, so `actorCount` is 1
//     on the response and the caller shows up in GET /tasks/{id}/actors.
//
// Both are motivated by the docs/bugs/2026-04-23-* calendar quick-
// create regression.
func TestCreateTaskAutoAttachesCreatorAsAssignee(t *testing.T) {
	bootstrap(t)
	t.Parallel()
	tt := newTenant(t)

	// Create a task without specifying actors.
	var task struct {
		ID              string `json:"id"`
		CreatedByUserID string `json:"createdByUserId"`
		ActorCount      int64  `json:"actorCount"`
	}
	doJSON(t, http.MethodPost, testServerURL+"/tasks", tt.AccessToken,
		map[string]any{
			"projectId": tt.ProjectPublicID,
			"title":     "Auto-attach verify",
		}, &task)

	// Bug 1 regression: createdByUserId must be a canonical UUID
	// string, not raw BINARY(16) bytes.
	require.Regexp(t, uuidV7Regex, task.CreatedByUserID,
		"createdByUserId must serialize as hyphenated UUID v7")
	require.Equal(t, tt.UserPublicID, task.CreatedByUserID,
		"createdByUserId must be the authenticated caller's public id")

	// Bug 2 regression: actorCount is 1 because the caller is
	// auto-attached.
	require.Equal(t, int64(1), task.ActorCount,
		"empty actors in request body must auto-attach the caller as sole assignee")

	// The actors listing must include the caller exactly once as
	// assignee.
	var actors struct {
		Total  int64 `json:"total"`
		Actors []struct {
			UserID string `json:"userId"`
			Role   string `json:"role"`
		} `json:"actors"`
	}
	doJSON(t, http.MethodGet, testServerURL+"/tasks/"+task.ID+"/actors",
		tt.AccessToken, nil, &actors)
	require.Equal(t, int64(1), actors.Total)
	require.Equal(t, tt.UserPublicID, actors.Actors[0].UserID)
	require.Equal(t, "assignee", actors.Actors[0].Role)

	// GET /me/tasks should surface the freshly created task because
	// the caller is the sole assignee. This is the actual calendar-
	// quick-create flow the bug was filed against.
	var myTasks struct {
		Total int64 `json:"total"`
		Tasks []struct {
			ID string `json:"id"`
		} `json:"tasks"`
	}
	doJSON(t, http.MethodGet, testServerURL+"/me/tasks",
		tt.AccessToken, nil, &myTasks)
	foundSelf := false
	for _, row := range myTasks.Tasks {
		if row.ID == task.ID {
			foundSelf = true
			break
		}
	}
	require.True(t, foundSelf,
		"auto-attached task must appear on GET /me/tasks")
}

// TestCreateTaskExplicitActorsBypassesAutoAttach verifies that passing
// an explicit non-empty `actors` array disables the auto-attach
// fallback. The creator must NOT be silently merged in; the explicit
// list is authoritative.
func TestCreateTaskExplicitActorsBypassesAutoAttach(t *testing.T) {
	bootstrap(t)
	t.Parallel()

	owner := newTenant(t)
	member := newTenant(t)

	// Invite member so the workspace has two real users.
	var invite struct {
		Token string `json:"token"`
	}
	doJSON(t, http.MethodPost,
		testServerURL+"/workspaces/"+owner.WorkspacePublicID+"/invites",
		owner.AccessToken, map[string]any{"role": "member"}, &invite)
	doJSON(t, http.MethodPost,
		testServerURL+"/invites/"+invite.Token+"/accept",
		member.AccessToken, nil, nil)

	// Owner creates a task whose only actor is `member`. Owner is
	// the creator but must NOT be auto-added because the explicit
	// actors list is authoritative.
	var task struct {
		ID              string `json:"id"`
		CreatedByUserID string `json:"createdByUserId"`
		ActorCount      int64  `json:"actorCount"`
	}
	doJSON(t, http.MethodPost, testServerURL+"/tasks", owner.AccessToken,
		map[string]any{
			"projectId": owner.ProjectPublicID,
			"title":     "Explicit actors only",
			"actors": []map[string]any{
				{"userId": member.UserPublicID, "role": "assignee"},
			},
		}, &task)

	// createdByUserId still canonical.
	require.Regexp(t, uuidV7Regex, task.CreatedByUserID)
	require.Equal(t, owner.UserPublicID, task.CreatedByUserID,
		"createdByUserId tracks the request authenticator, not the actor list")

	// Only one actor: the explicit member, not the creator.
	require.Equal(t, int64(1), task.ActorCount,
		"explicit non-empty actors list must be authoritative — creator is not merged in")

	var actors struct {
		Total  int64 `json:"total"`
		Actors []struct {
			UserID string `json:"userId"`
			Role   string `json:"role"`
		} `json:"actors"`
	}
	doJSON(t, http.MethodGet, testServerURL+"/tasks/"+task.ID+"/actors",
		owner.AccessToken, nil, &actors)
	require.Equal(t, int64(1), actors.Total)
	require.Equal(t, member.UserPublicID, actors.Actors[0].UserID,
		"only the explicitly-listed user must be an actor")
	require.Equal(t, "assignee", actors.Actors[0].Role)
}
