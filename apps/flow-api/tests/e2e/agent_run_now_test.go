package e2e

import (
	"net/http"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	apierrors "github.com/libraz/nodate-flow/apps/flow-api/internal/errors"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/eventbus"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/execution"
	"github.com/libraz/nodate-flow/apps/flow-api/tests/helpers"
)

func (f *awFixture) runNow(who *helpers.TestTenant, taskPub, agentPub string) (int, []byte) {
	f.t.Helper()
	return doJSONStatus(f.t, http.MethodPost, testServerURL+"/tasks/"+taskPub+"/agents/"+agentPub+"/run", who.AccessToken, nil)
}

// requireRunRefused asserts the request is refused with the spec and that
// nothing was recorded: a refused request must not leave a run request behind.
func (f *awFixture) requireRunRefused(who *helpers.TestTenant, taskPub, agentPub string, spec *apierrors.Spec, taskID uint32) {
	f.t.Helper()
	before := len(helpers.EventsOfType(f.t, testDB, f.wsID, taskID, string(eventbus.AgentTaskRunRequested), 0))
	status, raw := f.runNow(who, taskPub, agentPub)
	requireDenied(f.t, status, raw, spec.Status, spec.Code, "run now: "+spec.Code)
	require.Equal(f.t, before, len(helpers.EventsOfType(f.t, testDB, f.wsID, taskID, string(eventbus.AgentTaskRunRequested), 0)),
		"a refused run request must not be recorded")
}

// TestAgentRunNow_Success: the request records who pressed the button and
// the agent then works the task once.
func TestAgentRunNow_Success(t *testing.T) {
	bootstrap(t)
	t.Parallel()

	f := newAwFixture(t, helpers.Move(execution.ToolAddComment, map[string]any{"body": "On it."}), helpers.Finish("ran"))
	agent := f.agent(map[string]string{execution.ToolAddComment: "auto"})
	taskPub := f.task("Run now")
	taskID := f.taskInternalID(taskPub)
	f.assign(f.tt, taskPub, agent)

	since := f.mark()
	requestedFrom := time.Now().Unix()
	status, raw := f.runNow(f.tt, taskPub, agent.PublicID)
	require.Equalf(t, http.StatusAccepted, status, "body=%s", string(raw))
	var body map[string]any
	awDecode(t, raw, &body)
	delete(body, "$schema")
	require.Len(t, body, 1, "the acknowledgement carries only the request time")
	at, ok := body["runRequestedAt"].(float64)
	require.True(t, ok, "runRequestedAt must be a number: %v", body)
	require.GreaterOrEqual(t, int64(at), requestedFrom-1)
	require.LessOrEqual(t, int64(at), time.Now().Unix()+1)

	requested := helpers.EventsOfType(t, testDB, f.wsID, taskID, string(eventbus.AgentTaskRunRequested), since)
	require.Len(t, requested, 1)
	require.Equal(t, helpers.ResolveUserInternalID(t, testDB, f.tt.UserPublicID), requested[0].ActorUserID, "the person who pressed the button is the actor")
	require.Zero(t, requested[0].ActorAgentID)
	require.Equal(t, agent.PublicID, requested[0].Payload["agentId"])
	require.Equal(t, taskPub, requested[0].Payload["taskId"])
	awAuditEventually(t, f.wsID, "ai_agent.run.request", taskPub, true)

	runs := f.runSince(since)
	done := f.requireOneCleanRun(runs, taskPub, agent)
	require.Equal(t, requested[0].ID, runs[0].EventID, "the run was started by the request")
	require.Equal(t, "task_work", awRunMode(done))
	require.Equal(t, "done", awEnded(done))
	require.Equal(t, 2, f.prov.Calls())
	comments := f.comments(taskPub)
	require.Len(t, comments, 1)
	require.Equal(t, "agent", comments[0].AuthorKind)
	require.Equal(t, "On it.", comments[0].Body)
}

// TestAgentRunNow_NotAssigned: an agent that is not the task's assignee
// cannot be asked to work on it, whether unattached or only a reviewer.
func TestAgentRunNow_NotAssigned(t *testing.T) {
	bootstrap(t)
	t.Parallel()

	f := newAwFixture(t)
	agent := f.agent(nil)
	taskPub := f.task("Unassigned")
	taskID := f.taskInternalID(taskPub)
	f.requireRunRefused(f.tt, taskPub, agent.PublicID, apierrors.WsTaskAgentNotAssigned, taskID)

	doJSON(t, http.MethodPost, testServerURL+"/tasks/"+taskPub+"/agents", f.tt.AccessToken,
		map[string]any{"agentId": agent.PublicID, "role": "reviewer"}, nil)
	f.requireRunRefused(f.tt, taskPub, agent.PublicID, apierrors.WsTaskAgentNotAssigned, taskID)
}

// TestAgentRunNow_Paused: a paused agent cannot be asked to run.
func TestAgentRunNow_Paused(t *testing.T) {
	bootstrap(t)
	t.Parallel()

	f := newAwFixture(t)
	agent := f.agent(nil)
	taskPub := f.task("Paused")
	taskID := f.taskInternalID(taskPub)
	f.assign(f.tt, taskPub, agent)
	f.setPaused(agent, true)
	f.requireRunRefused(f.tt, taskPub, agent.PublicID, apierrors.AiAgentPaused, taskID)
}

// TestAgentRunNow_NoGrants: an agent allowed to do nothing is refused up front.
func TestAgentRunNow_NoGrants(t *testing.T) {
	bootstrap(t)
	t.Parallel()

	f := newAwFixture(t)
	agent := f.agent(map[string]string{})
	taskPub := f.task("No grants")
	taskID := f.taskInternalID(taskPub)
	f.assign(f.tt, taskPub, agent)
	f.requireRunRefused(f.tt, taskPub, agent.PublicID, apierrors.WsTaskAgentNoGrants, taskID)
}

// TestAgentRunNow_TaskNotWorkable: finished, archived and private tasks are
// not worked on, so the request is refused.
func TestAgentRunNow_TaskNotWorkable(t *testing.T) {
	bootstrap(t)
	t.Parallel()

	f := newAwFixture(t)
	agent := f.agent(nil)
	cases := []struct {
		name  string
		bring func(taskPub string)
	}{
		{"done", func(taskPub string) {
			for _, step := range []string{"start", "submit", "complete"} {
				doJSON(t, http.MethodPost, testServerURL+"/tasks/"+taskPub+"/transitions", f.tt.AccessToken,
					map[string]any{"transition": step}, nil)
			}
		}},
		{"archived", func(taskPub string) {
			doJSON(t, http.MethodPost, testServerURL+"/tasks/"+taskPub+"/archive", f.tt.AccessToken, nil, nil)
		}},
		{"private", func(taskPub string) {
			doJSON(t, http.MethodPatch, testServerURL+"/tasks/"+taskPub, f.tt.AccessToken, map[string]any{"visibility": "private"}, nil)
		}},
		{"project", func(taskPub string) {
			doJSON(t, http.MethodPatch, testServerURL+"/tasks/"+taskPub, f.tt.AccessToken, map[string]any{"visibility": "project"}, nil)
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			taskPub := f.task("Not workable: " + tc.name)
			taskID := f.taskInternalID(taskPub)
			f.assign(f.tt, taskPub, agent)
			status, _ := f.runNow(f.tt, taskPub, agent.PublicID)
			require.Equal(t, http.StatusAccepted, status, "control: the same task is accepted while workable")
			tc.bring(taskPub)
			f.requireRunRefused(f.tt, taskPub, agent.PublicID, apierrors.WsTaskAgentTaskNotWorkable, taskID)
		})
	}
}

// TestAgentRunNow_AgentNotFound: an unknown agent is a 404, not an accepted request.
func TestAgentRunNow_AgentNotFound(t *testing.T) {
	bootstrap(t)
	t.Parallel()

	f := newAwFixture(t)
	taskPub := f.task("Unknown agent")
	taskID := f.taskInternalID(taskPub)
	f.requireRunRefused(f.tt, taskPub, uuid.Must(uuid.NewV7()).String(), apierrors.AiAgentNotFound, taskID)
}

// TestAgentRunNow_RequiresCommenter: a viewer cannot ask an agent to run.
func TestAgentRunNow_RequiresCommenter(t *testing.T) {
	bootstrap(t)
	t.Parallel()

	f := newAwFixture(t)
	agent := f.agent(nil)
	viewer := seedProjectRoleMember(t, f.tt, "viewer")
	taskPub := f.task("Viewer cannot run")
	taskID := f.taskInternalID(taskPub)
	f.assign(f.tt, taskPub, agent)

	before := len(helpers.EventsOfType(t, testDB, f.wsID, taskID, string(eventbus.AgentTaskRunRequested), 0))
	status, raw := f.runNow(viewer, taskPub, agent.PublicID)
	requireDenied(t, status, raw, http.StatusForbidden, projectACLDenied, "a viewer asking for a run")
	require.Equal(t, before, len(helpers.EventsOfType(t, testDB, f.wsID, taskID, string(eventbus.AgentTaskRunRequested), 0)))
}

// TestAgentRunNow_MonthlyCapReached: once the agent's month of spend reaches
// its cap, the request is refused.
func TestAgentRunNow_MonthlyCapReached(t *testing.T) {
	bootstrap(t)
	t.Parallel()

	f := newAwFixture(t, helpers.Finish("spent"))
	f.prov.WithCost(1_000_000) // one dollar a call, against a 50 cent cap
	agent := f.agentWith(helpers.TaskAgentOptions{MonthlyCostCapCents: 50})
	taskPub := f.task("Monthly cap")
	taskID := f.taskInternalID(taskPub)
	f.assign(f.tt, taskPub, agent)

	status, raw := f.runNow(f.tt, taskPub, agent.PublicID)
	require.Equalf(t, http.StatusAccepted, status, "control: under the cap the request is accepted, body=%s", string(raw))
	f.requireOneCleanRun(f.runSince(f.markBefore(taskID)), taskPub, agent)

	f.requireRunRefused(f.tt, taskPub, agent.PublicID, apierrors.WsTaskAgentCostCapExceeded, taskID)
}

// TestAgentRunNow_DailyBudgetReached: once the workspace has spent its
// daily budget, the request is refused.
func TestAgentRunNow_DailyBudgetReached(t *testing.T) {
	bootstrap(t)
	t.Parallel()
	if amMockMode() {
		t.Skip("the mock backend meters no spend, so the daily budget cannot be reached")
	}

	f := newAwFixture(t, helpers.Finish("spent"))
	f.prov.WithCost(500_000_000) // five hundred dollars in one call
	agent := f.agent(nil)
	taskPub := f.task("Daily budget")
	taskID := f.taskInternalID(taskPub)
	f.assign(f.tt, taskPub, agent)

	status, raw := f.runNow(f.tt, taskPub, agent.PublicID)
	require.Equalf(t, http.StatusAccepted, status, "control: before any spend the request is accepted, body=%s", string(raw))
	f.requireOneCleanRun(f.runSince(f.markBefore(taskID)), taskPub, agent)

	f.requireRunRefused(f.tt, taskPub, agent.PublicID, apierrors.AiCostGuardExceeded, taskID)
}

// markBefore returns an event id just before the task's run request, so a
// trigger pass covers that request and what it caused.
func (f *awFixture) markBefore(taskID uint32) uint64 {
	f.t.Helper()
	requested := helpers.EventsOfType(f.t, testDB, f.wsID, taskID, string(eventbus.AgentTaskRunRequested), 0)
	require.NotEmpty(f.t, requested)
	return requested[0].ID - 1
}
