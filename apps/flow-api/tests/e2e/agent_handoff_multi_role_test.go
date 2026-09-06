package e2e

import (
	"database/sql"
	"net/http"
	"testing"

	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/types"
	"github.com/libraz/nodate-flow/apps/flow-api/tests/helpers"
	"github.com/stretchr/testify/require"
)

// task_actors is unique on (task_id, agent_id, role), so one agent can
// hold several enabled rows on the same task. Both handoff endpoints
// list those rows and then disable them with a statement that carries no
// role of its own, so the first UPDATE takes every row the agent has and
// the ones after it match nothing. A zero count there is that first
// UPDATE having already done the work, not a handoff that failed — the
// endpoints read the counts together for that reason.

// attachAgentInRole adds an enabled task_actors row for an agent in a
// role AssignAgentToTask does not cover, and returns its public id.
func attachAgentInRole(t *testing.T, taskPublicID string, workspaceID, taskID, agentID uint32, role string) string {
	t.Helper()
	actorPub := types.New()
	_, err := testDB.Exec(`
		INSERT INTO task_actors (public_id, workspace_id, task_id, agent_id, kind, role, enabled)
		VALUES (?, ?, ?, ?, 'agent', ?, TRUE)`,
		actorPub, workspaceID, taskID, agentID, role)
	require.NoError(t, err, "attach agent to task %s as %s", taskPublicID, role)
	return actorPub.UUID().String()
}

// countAgentRowsInAnyRole counts the agent's task_actors rows on a task
// at a given enabled state, across every role.
func countAgentRowsInAnyRole(t *testing.T, workspaceID, taskID, agentID uint32, enabled bool) int {
	t.Helper()
	var n int
	require.NoError(t, testDB.QueryRow(`
		SELECT COUNT(*) FROM task_actors
		WHERE workspace_id = ? AND task_id = ? AND agent_id = ?
		  AND kind = 'agent' AND enabled = ?`,
		workspaceID, taskID, agentID, enabled).Scan(&n))
	return n
}

// TestHandbackTakesEveryRowTheAgentHoldsOnTheTask hands a task back from
// an agent that holds two rows on it. The handback has to succeed and
// leave neither row enabled.
func TestHandbackTakesEveryRowTheAgentHoldsOnTheTask(t *testing.T) {
	bootstrap(t)
	t.Parallel()

	tt := newTenant(t)
	taskID := createTaskForAgent(t, tt, "Handback with two agent rows")
	agent := helpers.SeedAgent(t, testDB, tt.WorkspacePublicID, helpers.SeedAgentOptions{})
	_ = helpers.AssignAgentToTask(t, testDB, tt.WorkspacePublicID, taskID, agent.AgentID)
	wsID, taskInternalID := helpers.AgentTaskInternalIDs(t, testDB, tt.WorkspacePublicID, taskID)
	attachAgentInRole(t, taskID, wsID, taskInternalID, agent.AgentID, "reviewer")
	require.Equal(t, 2, countAgentRowsInAnyRole(t, wsID, taskInternalID, agent.AgentID, true),
		"fixture: the agent must start with two enabled rows on the task")

	status, body := doJSONStatus(t, http.MethodPost,
		testServerURL+"/tasks/"+taskID+"/handoff/to-user",
		tt.AccessToken, map[string]any{"reason": "manual"})
	require.Equalf(t, http.StatusOK, status,
		"handing back an agent that holds more than one row on the task must succeed; body=%s", string(body))

	require.Equal(t, 0, countAgentRowsInAnyRole(t, wsID, taskInternalID, agent.AgentID, true),
		"no row may stay enabled: the task would keep routing to the agent it was handed back from")
	evs := selectEventsForTask(t, testDB, wsID, taskInternalID, "agent.task.handoff_to_user")
	require.Len(t, evs, 1, "a handback that took the agent off the task records exactly one event")
}

// TestHandoffToAgentDisplacesEveryRowThePriorAgentHolds hands a task from
// one agent to another while the first holds two rows on it.
func TestHandoffToAgentDisplacesEveryRowThePriorAgentHolds(t *testing.T) {
	bootstrap(t)
	t.Parallel()

	tt := newTenant(t)
	taskID := createTaskForAgent(t, tt, "Handoff past two agent rows")
	prior := helpers.SeedAgent(t, testDB, tt.WorkspacePublicID, helpers.SeedAgentOptions{})
	next := helpers.SeedAgent(t, testDB, tt.WorkspacePublicID, helpers.SeedAgentOptions{})
	_ = helpers.AssignAgentToTask(t, testDB, tt.WorkspacePublicID, taskID, prior.AgentID)
	wsID, taskInternalID := helpers.AgentTaskInternalIDs(t, testDB, tt.WorkspacePublicID, taskID)
	attachAgentInRole(t, taskID, wsID, taskInternalID, prior.AgentID, "reviewer")
	require.Equal(t, 2, countAgentRowsInAnyRole(t, wsID, taskInternalID, prior.AgentID, true),
		"fixture: the prior agent must start with two enabled rows on the task")

	status, body := doJSONStatus(t, http.MethodPost,
		testServerURL+"/tasks/"+taskID+"/handoff/to-agent",
		tt.AccessToken, map[string]any{"agentId": next.AgentPublicID})
	require.Equalf(t, http.StatusOK, status,
		"handing off past an agent that holds more than one row on the task must succeed; body=%s", string(body))

	require.Equal(t, 0, countAgentRowsInAnyRole(t, wsID, taskInternalID, prior.AgentID, true),
		"the prior agent must be off the task entirely")
	require.Equal(t, 1, countAgentRowsInAnyRole(t, wsID, taskInternalID, next.AgentID, true),
		"the new agent must be the task's only enabled agent")
	evs := selectEventsForTask(t, testDB, wsID, taskInternalID, "agent.task.handoff_to_agent")
	require.Len(t, evs, 1)
}

// TestHandbackRefusesWhenTheAgentIsAlreadyOff pins the other side of the
// count: an agent whose rows are all disabled has already been handed
// back, and a second handback must not record another one.
func TestHandbackRefusesWhenTheAgentIsAlreadyOff(t *testing.T) {
	bootstrap(t)
	t.Parallel()

	tt := newTenant(t)
	taskID := createTaskForAgent(t, tt, "Handback twice")
	agent := helpers.SeedAgent(t, testDB, tt.WorkspacePublicID, helpers.SeedAgentOptions{})
	_ = helpers.AssignAgentToTask(t, testDB, tt.WorkspacePublicID, taskID, agent.AgentID)
	wsID, taskInternalID := helpers.AgentTaskInternalIDs(t, testDB, tt.WorkspacePublicID, taskID)

	status, body := doJSONStatus(t, http.MethodPost,
		testServerURL+"/tasks/"+taskID+"/handoff/to-user",
		tt.AccessToken, map[string]any{"reason": "manual"})
	require.Equalf(t, http.StatusOK, status, "first handback; body=%s", string(body))

	status, body = doJSONStatus(t, http.MethodPost,
		testServerURL+"/tasks/"+taskID+"/handoff/to-user",
		tt.AccessToken, map[string]any{"reason": "manual"})
	requireDenied(t, status, body, http.StatusUnprocessableEntity,
		"WS.TASK_AGENT.NOT_ASSIGNED", "second handback")

	evs := selectEventsForTask(t, testDB, wsID, taskInternalID, "agent.task.handoff_to_user")
	require.Len(t, evs, 1, "a refused handback must leave no second event behind")
	var stillEnabled sql.NullInt32
	require.NoError(t, testDB.QueryRow(`
		SELECT MAX(enabled) FROM task_actors
		WHERE workspace_id = ? AND task_id = ? AND agent_id = ?`,
		wsID, taskInternalID, agent.AgentID).Scan(&stillEnabled))
	require.EqualValues(t, 0, stillEnabled.Int32)
}
