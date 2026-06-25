package e2e

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/nodate-flow/nodate-flow/apps/flow-api/tests/helpers"
)

// TestAddActorRejectsCrossTenantUser is a security regression for the
// cross-tenant assignee leak: task actor assignment must resolve the
// target user scoped to the task's workspace. A caller who can write a
// task in workspace A must NOT be able to attach a user that belongs
// only to workspace B as an actor.
//
// Two independent tenants are created. Each tenant is its own isolated
// workspace with its own user. The owner of workspace A then tries to
// add workspace B's user to a task in workspace A across all reachable
// entry points:
//
//  1. POST /tasks/{id}/actors      (AddActor handler)
//  2. POST /tasks with actors[]    (Create handler explicit actor list)
//
// Both must fail with WS.MEMBER.NOT_FOUND (404) and the foreign user
// must never appear on the task's actor listing.
func TestAddActorRejectsCrossTenantUser(t *testing.T) {
	bootstrap(t)
	t.Parallel()

	tenantA := newTenant(t)
	tenantB := newTenant(t)

	// Sanity: the two tenants are distinct users in distinct workspaces.
	require.NotEqual(t, tenantA.UserPublicID, tenantB.UserPublicID)
	require.NotEqual(t, tenantA.WorkspacePublicID, tenantB.WorkspacePublicID)

	// Owner A creates a task in workspace A (no explicit actors, so the
	// creator is auto-attached as the sole assignee).
	var task struct {
		ID         string `json:"id"`
		ActorCount int64  `json:"actorCount"`
	}
	doJSON(t, http.MethodPost, testServerURL+"/tasks", tenantA.AccessToken,
		map[string]any{
			"projectId": tenantA.ProjectPublicID,
			"title":     "Cross-tenant actor guard",
		}, &task)
	require.Equal(t, int64(1), task.ActorCount)

	// Path 1: POST /tasks/{id}/actors with tenant B's user must 404.
	status, raw := doJSONStatus(t, http.MethodPost,
		testServerURL+"/tasks/"+task.ID+"/actors", tenantA.AccessToken,
		map[string]any{"userId": tenantB.UserPublicID, "role": "assignee"})
	require.Equalf(t, http.StatusNotFound, status,
		"adding a foreign-workspace user as an actor must 404; got %d body=%s", status, string(raw))
	require.Equal(t, "WS.MEMBER.NOT_FOUND", decodeErrorCode(t, raw),
		"cross-tenant actor add must surface WS.MEMBER.NOT_FOUND")

	// The foreign user must not have been attached: only the creator
	// remains.
	var actors struct {
		Total  int64 `json:"total"`
		Actors []struct {
			UserID string `json:"userId"`
		} `json:"actors"`
	}
	doJSON(t, http.MethodGet, testServerURL+"/tasks/"+task.ID+"/actors",
		tenantA.AccessToken, nil, &actors)
	require.Equal(t, int64(1), actors.Total, "no foreign actor must be attached")
	for _, a := range actors.Actors {
		require.NotEqual(t, tenantB.UserPublicID, a.UserID,
			"workspace B user must never appear as an actor on a workspace A task")
	}

	// Path 2: POST /tasks with an explicit actors[] referencing tenant
	// B's user must also 404 and create nothing.
	status2, raw2 := doJSONStatus(t, http.MethodPost, testServerURL+"/tasks",
		tenantA.AccessToken, map[string]any{
			"projectId": tenantA.ProjectPublicID,
			"title":     "Cross-tenant explicit actor guard",
			"actors": []map[string]any{
				{"userId": tenantB.UserPublicID, "role": "assignee"},
			},
		})
	require.Equalf(t, http.StatusNotFound, status2,
		"creating a task with a foreign-workspace actor must 404; got %d body=%s", status2, string(raw2))
	require.Equal(t, "WS.MEMBER.NOT_FOUND", decodeErrorCode(t, raw2),
		"cross-tenant explicit-actor create must surface WS.MEMBER.NOT_FOUND")
}

// TestAddActorAcceptsSameWorkspaceMember is the positive counterpart to
// the cross-tenant guard: a user who is a genuine enabled member of the
// workspace can still be attached as an actor. This proves the fix does
// not over-block legitimate same-tenant assignments.
func TestAddActorAcceptsSameWorkspaceMember(t *testing.T) {
	bootstrap(t)
	t.Parallel()

	owner := newTenant(t)
	member := newTenant(t)

	// Invite member into the owner's workspace and accept so the
	// workspace genuinely has two members.
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
			"projectId": owner.ProjectPublicID,
			"title":     "Same-workspace actor add",
		}, &task)

	// Adding the now-real member must succeed.
	doJSON(t, http.MethodPost, testServerURL+"/tasks/"+task.ID+"/actors",
		owner.AccessToken,
		map[string]any{"userId": member.UserPublicID, "role": "assignee"}, nil)

	var actors struct {
		Total  int64 `json:"total"`
		Actors []struct {
			UserID string `json:"userId"`
		} `json:"actors"`
	}
	doJSON(t, http.MethodGet, testServerURL+"/tasks/"+task.ID+"/actors",
		owner.AccessToken, nil, &actors)
	found := false
	for _, a := range actors.Actors {
		if a.UserID == member.UserPublicID {
			found = true
		}
	}
	require.True(t, found, "a genuine workspace member must be attachable as an actor")
}

// TestHandoffToUserRejectsCrossTenantTarget guards the agent handoff
// path (POST /tasks/{id}/handoff/to-user): a manual handback that names
// a target user must scope that user to the task's workspace. A user
// from another tenant must not be attachable as the new assignee.
func TestHandoffToUserRejectsCrossTenantTarget(t *testing.T) {
	bootstrap(t)
	t.Parallel()

	owner := newTenant(t)
	outsider := newTenant(t)

	// Owner creates a task and assigns an agent so the handoff endpoint's
	// "agent currently assigned" precondition is satisfied.
	taskID := createTaskForAgent(t, owner, "Cross-tenant handoff target")
	agent := helpers.SeedAgent(t, testDB, owner.WorkspacePublicID, helpers.SeedAgentOptions{})
	_ = helpers.AssignAgentToTask(t, testDB, owner.WorkspacePublicID, taskID, agent.AgentID)
	wsID, taskInternalID := helpers.AgentTaskInternalIDs(t, testDB, owner.WorkspacePublicID, taskID)

	// Handing back to a foreign-workspace user must 404 and must not
	// disable the agent or attach the outsider.
	status, raw := doJSONStatus(t, http.MethodPost,
		testServerURL+"/tasks/"+taskID+"/handoff/to-user", owner.AccessToken,
		map[string]any{"reason": "manual", "targetUserPublicId": outsider.UserPublicID})
	require.Equalf(t, http.StatusNotFound, status,
		"handoff to a foreign-workspace user must 404; got %d body=%s", status, string(raw))
	require.Equal(t, "WS.MEMBER.NOT_FOUND", decodeErrorCode(t, raw),
		"cross-tenant handoff target must surface WS.MEMBER.NOT_FOUND")

	// The handler resolves the target before any mutation, so the agent
	// must still be the enabled assignee (nothing was committed).
	require.Equal(t, 1, countAgentActors(t, testDB, wsID, taskInternalID, agent.AgentID, true),
		"a rejected cross-tenant handoff must not disable the agent assignee")

	// The outsider must never appear as an actor on the task.
	var actors struct {
		Actors []struct {
			UserID string `json:"userId"`
		} `json:"actors"`
	}
	doJSON(t, http.MethodGet, testServerURL+"/tasks/"+taskID+"/actors",
		owner.AccessToken, nil, &actors)
	for _, a := range actors.Actors {
		require.NotEqual(t, outsider.UserPublicID, a.UserID,
			"foreign-workspace user must never be attached via handoff")
	}
}
