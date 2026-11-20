package e2e

import (
	"context"
	"database/sql"
	"net/http"
	"testing"
	"time"

	"github.com/libraz/nodate-flow/apps/flow-api/internal/ai/autoactions"
	"github.com/libraz/nodate-flow/apps/flow-api/tests/helpers"
	"github.com/stretchr/testify/require"
)

// TestTaskActorRemovalIsBoundToAuthorizedTask checks that a caller who can
// edit task A and read task B cannot use B's visible actor ID on A's delete
// route. It also checks that successful user and agent removals on A record
// their events against A.
func TestTaskActorRemovalIsBoundToAuthorizedTask(t *testing.T) {
	bootstrap(t)
	t.Parallel()

	owner := newTenant(t)
	caller := seedProjectRoleMember(t, owner, "editor")
	projectB := createProject(t, owner, "Actor removal viewer project")
	addProjectRoleMember(t, owner, projectB, caller.UserPublicID, "viewer")

	taskA := createPublicActorMutationTask(t, owner.AccessToken, owner.ProjectPublicID, "Actor removal task A")
	taskB := createPublicActorMutationTask(t, owner.AccessToken, projectB, "Actor removal task B")

	agentA := helpers.SeedAgent(t, testDB, owner.WorkspacePublicID, helpers.SeedAgentOptions{})
	agentB := helpers.SeedAgent(t, testDB, owner.WorkspacePublicID, helpers.SeedAgentOptions{})
	actorAAgentID := addAgentActorForMutationTest(t, owner.AccessToken, taskA, agentA.AgentPublicID)
	actorBAgentID := addAgentActorForMutationTest(t, owner.AccessToken, taskB, agentB.AgentPublicID)

	// The caller's editor grant on A and viewer grant on B are independent.
	// Both actor IDs on B are therefore visible to this caller.
	actorAUserID := visibleUserActorID(t, caller.AccessToken, taskA, owner.UserPublicID)
	actorBUserID := visibleUserActorID(t, caller.AccessToken, taskB, owner.UserPublicID)
	visibleAgentBID := visibleAgentActorID(t, caller.AccessToken, taskB, agentB.AgentPublicID)
	require.NotEmpty(t, actorAUserID)
	require.NotEmpty(t, actorBUserID)
	require.NotEmpty(t, visibleAgentBID)
	require.Equal(t, actorBAgentID, visibleAgentBID)

	aRemovedBefore := taskActorEventCount(t, taskA, "task.actor.removed")
	bRemovedBefore := taskActorEventCount(t, taskB, "task.actor.removed")

	// A same-workspace actor ID from B is not in A's mutation scope. The
	// route must report not found and append no event to either task.
	for _, actorID := range []string{actorBUserID, visibleAgentBID} {
		status, raw := doJSONStatus(t, http.MethodDelete,
			testServerURL+"/tasks/"+taskA+"/actors/"+actorID,
			caller.AccessToken, nil)
		require.Equalf(t, http.StatusNotFound, status,
			"task A must not remove an actor belonging to task B: %s", string(raw))
		require.Equal(t, "WS.TASK_ACTOR.NOT_FOUND", decodeErrorCode(t, raw))
	}
	require.Equal(t, aRemovedBefore, taskActorEventCount(t, taskA, "task.actor.removed"),
		"rejected cross-task removals must not record events against task A")
	require.Equal(t, bRemovedBefore, taskActorEventCount(t, taskB, "task.actor.removed"),
		"rejected cross-task removals must not record events against task B")

	// The caller can still remove user and agent actors that actually belong
	// to A. Each successful event must be attributed to A alone.
	removeTaskActorForMutationTest(t, caller.AccessToken, taskA, actorAUserID)
	removeTaskActorForMutationTest(t, caller.AccessToken, taskA, actorAAgentID)
	require.Equal(t, aRemovedBefore+2, taskActorEventCount(t, taskA, "task.actor.removed"))
	require.Equal(t, bRemovedBefore, taskActorEventCount(t, taskB, "task.actor.removed"))

	var actorsA struct {
		Actors []struct {
			ID string `json:"id"`
		} `json:"actors"`
	}
	doJSON(t, http.MethodGet, testServerURL+"/tasks/"+taskA+"/actors", caller.AccessToken, nil, &actorsA)
	require.Empty(t, actorsA.Actors, "both authorized A actors must be removed")

	var agentsA struct {
		Agents []struct {
			ID string `json:"id"`
		} `json:"agents"`
	}
	doJSON(t, http.MethodGet, testServerURL+"/tasks/"+taskA+"/agents", caller.AccessToken, nil, &agentsA)
	require.Empty(t, agentsA.Agents, "authorized A agent actor must be removed")

	var actorsB struct {
		Actors []struct {
			ID string `json:"id"`
		} `json:"actors"`
	}
	doJSON(t, http.MethodGet, testServerURL+"/tasks/"+taskB+"/actors", caller.AccessToken, nil, &actorsB)
	require.Len(t, actorsB.Actors, 1, "the B user actor must remain attached")

	var agentsB struct {
		Agents []struct {
			ID string `json:"id"`
		} `json:"agents"`
	}
	doJSON(t, http.MethodGet, testServerURL+"/tasks/"+taskB+"/agents", caller.AccessToken, nil, &agentsB)
	require.Len(t, agentsB.Agents, 1, "the B agent actor must remain attached")
}

// TestUserAndAgentActorMutationsReevaluateHasRole exercises the real API
// paths that change actor-role facts. Removing one of two reviewers must
// keep the constraint satisfied; removing the last reviewer must fail it.
func TestUserAndAgentActorMutationsReevaluateHasRole(t *testing.T) {
	bootstrap(t)
	t.Parallel()

	owner := newTenant(t)
	user1 := seedProjectRoleMember(t, owner, "editor")
	user2 := seedProjectRoleMember(t, owner, "editor")
	taskID := createTask(t, owner.AccessToken, owner.ProjectPublicID, "Actor role refresh")

	var constraint struct {
		ID string `json:"id"`
	}
	doJSON(t, http.MethodPost, testServerURL+"/tasks/"+taskID+"/constraints", owner.AccessToken,
		map[string]any{
			"kind":       "approval",
			"expression": `{"op":"actor.has_role","arg":"reviewer"}`,
		}, &constraint)
	require.NotEmpty(t, constraint.ID)
	requireActorConstraintVerdict(t, constraint.ID, false)

	addedBefore := taskActorEventCount(t, taskID, "task.actor.added")
	removedBefore := taskActorEventCount(t, taskID, "task.actor.removed")

	userActor1ID := addUserActorForMutationTest(t, owner.AccessToken, taskID, user1.UserPublicID)
	requireActorConstraintVerdict(t, constraint.ID, true)
	userActor2ID := addUserActorForMutationTest(t, owner.AccessToken, taskID, user2.UserPublicID)
	requireActorConstraintVerdict(t, constraint.ID, true)
	removeTaskActorForMutationTest(t, owner.AccessToken, taskID, userActor1ID)
	requireActorConstraintVerdict(t, constraint.ID, true)
	removeTaskActorForMutationTest(t, owner.AccessToken, taskID, userActor2ID)
	requireActorConstraintVerdict(t, constraint.ID, false)

	agent1 := helpers.SeedAgent(t, testDB, owner.WorkspacePublicID, helpers.SeedAgentOptions{})
	agent2 := helpers.SeedAgent(t, testDB, owner.WorkspacePublicID, helpers.SeedAgentOptions{})
	agentActor1ID := addAgentActorForMutationTest(t, owner.AccessToken, taskID, agent1.AgentPublicID)
	requireActorConstraintVerdict(t, constraint.ID, true)
	agentActor2ID := addAgentActorForMutationTest(t, owner.AccessToken, taskID, agent2.AgentPublicID)
	requireActorConstraintVerdict(t, constraint.ID, true)
	removeTaskActorForMutationTest(t, owner.AccessToken, taskID, agentActor1ID)
	requireActorConstraintVerdict(t, constraint.ID, true)
	removeTaskActorForMutationTest(t, owner.AccessToken, taskID, agentActor2ID)
	requireActorConstraintVerdict(t, constraint.ID, false)

	require.Equal(t, addedBefore+4, taskActorEventCount(t, taskID, "task.actor.added"),
		"both user and agent add paths must record task actor events")
	require.Equal(t, removedBefore+4, taskActorEventCount(t, taskID, "task.actor.removed"),
		"both user and agent remove paths must record task actor events")
}

// TestHandoffActorChangesReevaluateHasRole covers the handoff paths that
// replace an assignee directly inside their event transaction. The task
// starts with a reviewer and no assignee so each handoff changes the
// actor.has_role(assignee) fact observably.
func TestHandoffActorChangesReevaluateHasRole(t *testing.T) {
	bootstrap(t)
	t.Parallel()

	tenant := newTenant(t)
	var task struct {
		ID string `json:"id"`
	}
	doJSON(t, http.MethodPost, testServerURL+"/tasks", tenant.AccessToken, map[string]any{
		"projectId": tenant.ProjectPublicID,
		"title":     "Handoff actor role refresh",
		"actors": []map[string]any{
			{"userId": tenant.UserPublicID, "role": "reviewer"},
		},
	}, &task)
	require.NotEmpty(t, task.ID)

	var constraint struct {
		ID string `json:"id"`
	}
	doJSON(t, http.MethodPost, testServerURL+"/tasks/"+task.ID+"/constraints", tenant.AccessToken,
		map[string]any{
			"kind":       "approval",
			"expression": `{"op":"actor.has_role","arg":"assignee"}`,
		}, &constraint)
	require.NotEmpty(t, constraint.ID)
	requireActorConstraintVerdict(t, constraint.ID, false)

	agent1 := helpers.SeedAgent(t, testDB, tenant.WorkspacePublicID, helpers.SeedAgentOptions{})
	doJSON(t, http.MethodPost, testServerURL+"/tasks/"+task.ID+"/handoff/to-agent", tenant.AccessToken,
		map[string]any{"agentId": agent1.AgentPublicID}, nil)
	requireActorConstraintVerdict(t, constraint.ID, true)

	// A handback without a target removes the last assignee.
	doJSON(t, http.MethodPost, testServerURL+"/tasks/"+task.ID+"/handoff/to-user", tenant.AccessToken,
		map[string]any{"reason": "manual"}, nil)
	requireActorConstraintVerdict(t, constraint.ID, false)

	// Handing off again and naming a user must leave the role fact true
	// after the agent assignee is disabled and the user is attached.
	agent2 := helpers.SeedAgent(t, testDB, tenant.WorkspacePublicID, helpers.SeedAgentOptions{})
	doJSON(t, http.MethodPost, testServerURL+"/tasks/"+task.ID+"/handoff/to-agent", tenant.AccessToken,
		map[string]any{"agentId": agent2.AgentPublicID}, nil)
	requireActorConstraintVerdict(t, constraint.ID, true)
	doJSON(t, http.MethodPost, testServerURL+"/tasks/"+task.ID+"/handoff/to-user", tenant.AccessToken,
		map[string]any{
			"reason":             "manual",
			"targetUserPublicId": tenant.UserPublicID,
		}, nil)
	requireActorConstraintVerdict(t, constraint.ID, true)
}

// TestAutoActionsHandoffReevaluatesAssigneeRole verifies that a successful
// background handback refreshes the task's actor-role constraint after the
// agent assignee is disabled.
func TestAutoActionsHandoffReevaluatesAssigneeRole(t *testing.T) {
	bootstrap(t)
	t.Parallel()
	lockAutoActionPass(t)

	tenant := newTenant(t)
	var task struct {
		ID string `json:"id"`
	}
	doJSON(t, http.MethodPost, testServerURL+"/tasks", tenant.AccessToken, map[string]any{
		"projectId": tenant.ProjectPublicID,
		"title":     "Automatic handoff actor role refresh",
		"actors": []map[string]any{
			{"userId": tenant.UserPublicID, "role": "reviewer"},
		},
	}, &task)
	require.NotEmpty(t, task.ID)

	agent := helpers.SeedAgent(t, testDB, tenant.WorkspacePublicID, helpers.SeedAgentOptions{})
	_ = helpers.AssignAgentToTask(t, testDB, tenant.WorkspacePublicID, task.ID, agent.AgentID)
	wsID, taskInternalID := helpers.AgentTaskInternalIDs(t, testDB, tenant.WorkspacePublicID, task.ID)

	var constraint struct {
		ID string `json:"id"`
	}
	doJSON(t, http.MethodPost, testServerURL+"/tasks/"+task.ID+"/constraints", tenant.AccessToken,
		map[string]any{
			"kind":       "approval",
			"expression": `{"op":"actor.has_role","arg":"assignee"}`,
		}, &constraint)
	require.NotEmpty(t, constraint.ID)
	requireActorConstraintVerdict(t, constraint.ID, true)

	// Make the real auto-action rule see an agent that has been attempting
	// the task for longer than its idle window.
	helpers.SetTaskAgentMemo(t, testDB, tenant.WorkspacePublicID, task.ID, map[string]any{
		"attempts":         5,
		"last_finished_at": time.Now().Add(-12 * time.Hour).Unix(),
	})
	ageTaskUpdatedAt(t, testDB, wsID, taskInternalID, 12*time.Hour)

	exec := &autoactions.Executor{
		DB:     testDB,
		Logger: testLogger(t),
		Config: autoactions.ExecutorConfig{ConfidenceThreshold: 0.5},
	}
	requireCompletePass(context.Background(), t, exec)

	requireActorConstraintVerdict(t, constraint.ID, false)
	require.Equal(t, 0, countAgentActors(t, testDB, wsID, taskInternalID, agent.AgentID, true),
		"a successful auto handback must disable the agent assignee")
	require.Len(t, selectEventsForTask(t, testDB, wsID, taskInternalID, "agent.task.handoff_to_user"), 1,
		"the constraint refresh belongs to a committed handback")
}

func createPublicActorMutationTask(t *testing.T, token, projectID, title string) string {
	t.Helper()
	var task struct {
		ID string `json:"id"`
	}
	doJSON(t, http.MethodPost, testServerURL+"/tasks", token, map[string]any{
		"projectId":  projectID,
		"title":      title,
		"visibility": "public",
	}, &task)
	require.NotEmpty(t, task.ID)
	return task.ID
}

func addUserActorForMutationTest(t *testing.T, token, taskID, userID string) string {
	t.Helper()
	var actor struct {
		ID string `json:"id"`
	}
	doJSON(t, http.MethodPost, testServerURL+"/tasks/"+taskID+"/actors", token,
		map[string]any{"userId": userID, "role": "reviewer"}, &actor)
	require.NotEmpty(t, actor.ID)
	return actor.ID
}

func addAgentActorForMutationTest(t *testing.T, token, taskID, agentID string) string {
	t.Helper()
	var actor struct {
		ID string `json:"id"`
	}
	doJSON(t, http.MethodPost, testServerURL+"/tasks/"+taskID+"/agents", token,
		map[string]any{"agentId": agentID, "role": "reviewer"}, &actor)
	require.NotEmpty(t, actor.ID)
	return actor.ID
}

func visibleUserActorID(t *testing.T, token, taskID, userID string) string {
	t.Helper()
	var actors struct {
		Actors []struct {
			ID     string `json:"id"`
			UserID string `json:"userId"`
		} `json:"actors"`
	}
	doJSON(t, http.MethodGet, testServerURL+"/tasks/"+taskID+"/actors", token, nil, &actors)
	for _, actor := range actors.Actors {
		if actor.UserID == userID {
			return actor.ID
		}
	}
	require.FailNow(t, "user actor is not visible", "task=%s user=%s", taskID, userID)
	return ""
}

func visibleAgentActorID(t *testing.T, token, taskID, agentID string) string {
	t.Helper()
	var actors struct {
		Agents []struct {
			ID      string `json:"id"`
			AgentID string `json:"agentId"`
		} `json:"agents"`
	}
	doJSON(t, http.MethodGet, testServerURL+"/tasks/"+taskID+"/agents", token, nil, &actors)
	for _, actor := range actors.Agents {
		if actor.AgentID == agentID {
			return actor.ID
		}
	}
	require.FailNow(t, "agent actor is not visible", "task=%s agent=%s", taskID, agentID)
	return ""
}

func removeTaskActorForMutationTest(t *testing.T, token, taskID, actorID string) {
	t.Helper()
	var response struct {
		Ok bool `json:"ok"`
	}
	doJSON(t, http.MethodDelete, testServerURL+"/tasks/"+taskID+"/actors/"+actorID, token, nil, &response)
	require.True(t, response.Ok)
}

func requireActorConstraintVerdict(t *testing.T, constraintID string, satisfied bool) {
	t.Helper()
	satisfiedAt, failedAt := waitConstraintMarkers(t, constraintID, func(s, f sql.NullTime) bool {
		if satisfied {
			return s.Valid && !f.Valid
		}
		return !s.Valid && f.Valid
	})
	if satisfied {
		require.True(t, satisfiedAt.Valid, "actor role constraint must be satisfied")
		require.False(t, failedAt.Valid, "satisfied actor role constraint must clear failed_at")
		return
	}
	require.False(t, satisfiedAt.Valid, "missing actor role must leave actor role constraint unsatisfied")
	require.True(t, failedAt.Valid, "unsatisfied actor role constraint must record failed_at")
}

func taskActorEventCount(t *testing.T, taskID, eventType string) int {
	t.Helper()
	var count int
	require.NoError(t, testDB.QueryRow(`
		SELECT COUNT(*)
		FROM events e
		JOIN tasks t ON t.id = e.task_id AND t.workspace_id = e.workspace_id
		WHERE t.public_id = UUID_TO_BIN(?, 0)
		  AND e.type = ?`, taskID, eventType).Scan(&count))
	return count
}
