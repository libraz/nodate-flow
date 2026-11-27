package e2e

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"

	apierrors "github.com/libraz/nodate-flow/apps/flow-api/internal/errors"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/eventbus"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/execution"
	"github.com/libraz/nodate-flow/apps/flow-api/tests/helpers"
)

// atScene is one agent assigned to one task, set up through the API.
type atScene struct {
	agent   *helpers.TaskAgent
	taskPub string
	taskID  uint32
	actorID string
}

// scene creates a task agent (default grants unless opts say otherwise)
// assigned to a fresh public task by the owner.
func (f *awFixture) scene(opts helpers.TaskAgentOptions) atScene {
	f.t.Helper()
	agent := f.agentWith(opts)
	taskPub := f.task("Trigger scene")
	return atScene{agent: agent, taskPub: taskPub, taskID: f.taskInternalID(taskPub), actorID: f.assign(f.tt, taskPub, agent)}
}

// eventOf returns the one event of the type recorded after since.
func (f *awFixture) eventOf(taskID uint32, kind eventbus.Kind, since uint64) helpers.EventRecord {
	f.t.Helper()
	evs := helpers.EventsOfType(f.t, testDB, f.wsID, taskID, string(kind), since)
	require.Lenf(f.t, evs, 1, "%s events after %d", kind, since)
	return evs[0]
}

func (f *awFixture) isCandidate(agent *helpers.TaskAgent, eventID uint64) bool {
	f.t.Helper()
	for _, id := range f.env.CandidateAgents(f.t, f.wsID, eventID) {
		if id == agent.ID {
			return true
		}
	}
	return false
}

// runsOf returns the trigger runs that belong to the agent, in order.
func agentRunsOf(runs []helpers.TriggeredRun, agent *helpers.TaskAgent) []helpers.TriggeredRun {
	var out []helpers.TriggeredRun
	for _, r := range runs {
		if r.AgentID == agent.ID {
			out = append(out, r)
		}
	}
	return out
}

// requireRunMode runs the trigger path from since and asserts the agent was
// started by the event in the given mode.
func (f *awFixture) requireRunMode(agent *helpers.TaskAgent, taskID uint32, since uint64, ev helpers.EventRecord, wantMode string) {
	f.t.Helper()
	mine := agentRunsOf(f.runSince(since), agent)
	started := helpers.EventsOfType(f.t, testDB, f.wsID, taskID, string(eventbus.AiAgentRunStarted), since)
	require.Len(f.t, started, len(mine))
	for i, r := range mine {
		if r.EventID != ev.ID {
			continue
		}
		require.NoError(f.t, r.Err)
		require.Equal(f.t, wantMode, awRunMode(started[i].Payload))
		return
	}
	require.FailNowf(f.t, "agent was not run by the event", "event %d (%s), runs %+v", ev.ID, ev.Type, mine)
}

// TestAgentTrigger_Candidates: which events wake which agents, and whether
// the wake-up is task work or a plain tick. The wake-up is asked of the same
// query the event hook runs, and the run mode of the same runner the worker
// runs. Where an event does not wake the agent, the event is first shown to
// have woken it before the condition was taken away.
func TestAgentTrigger_Candidates(t *testing.T) {
	bootstrap(t)
	t.Parallel()

	f := newAwFixture(t)
	ownerID := helpers.ResolveUserInternalID(t, testDB, f.tt.UserPublicID)

	t.Run("comment by a person starts task work", func(t *testing.T) {
		s := f.scene(helpers.TaskAgentOptions{})
		since := f.mark()
		f.comment(f.tt, s.taskPub, "hello")
		ev := f.eventOf(s.taskID, eventbus.TaskCommentAdded, since)
		require.True(t, f.isCandidate(s.agent, ev.ID))
		f.requireRunMode(s.agent, s.taskID, since, ev, "task_work")
	})

	t.Run("comment by an agent wakes nobody", func(t *testing.T) {
		s := f.scene(helpers.TaskAgentOptions{Tools: map[string]string{execution.ToolAddComment: "auto"}})
		f.prov.Push(helpers.Move(execution.ToolAddComment, map[string]any{"body": "agent words"}), helpers.Finish("done"))
		since := f.mark()
		f.comment(f.tt, s.taskPub, "hello")
		mine := agentRunsOf(f.runSince(since), s.agent)
		require.Len(t, mine, 1, "the person's comment started one run and the agent's own comment none")

		var agentComment helpers.EventRecord
		for _, ev := range helpers.EventsOfType(t, testDB, f.wsID, s.taskID, string(eventbus.TaskCommentAdded), since) {
			if ev.ActorAgentID != 0 {
				agentComment = ev
			}
		}
		require.NotZero(t, agentComment.ID, "the agent wrote a comment")
		require.False(t, f.isCandidate(s.agent, agentComment.ID))
	})

	t.Run("event with no actor wakes nobody", func(t *testing.T) {
		s := f.scene(helpers.TaskAgentOptions{})
		payload := map[string]any{"taskId": s.taskPub}
		fromPerson := helpers.InsertEvent(t, testDB, f.wsID, s.taskID, ownerID, 0, string(eventbus.TaskCommentAdded), payload)
		require.True(t, f.isCandidate(s.agent, fromPerson), "control: the same event from a person wakes the agent")
		system := helpers.InsertEvent(t, testDB, f.wsID, s.taskID, 0, 0, string(eventbus.TaskCommentAdded), payload)
		require.False(t, f.isCandidate(s.agent, system))
	})

	t.Run("assignment of another agent wakes only that agent", func(t *testing.T) {
		first := f.scene(helpers.TaskAgentOptions{})
		other := f.agent(nil)
		since := f.mark()
		f.assign(f.tt, first.taskPub, other)
		ev := f.eventOf(first.taskID, eventbus.TaskActorAdded, since)
		require.True(t, f.isCandidate(other, ev.ID))
		require.False(t, f.isCandidate(first.agent, ev.ID), "the agent assigned earlier is not woken by someone else's assignment")
		f.requireRunMode(other, first.taskID, since, ev, "task_work")
	})

	t.Run("assignment as reviewer wakes nobody", func(t *testing.T) {
		s := f.scene(helpers.TaskAgentOptions{})
		payload := func(role string) map[string]any {
			return map[string]any{"taskId": s.taskPub, "agentId": s.agent.PublicID, "kind": "agent", "role": role}
		}
		asAssignee := helpers.InsertEvent(t, testDB, f.wsID, s.taskID, ownerID, 0, string(eventbus.TaskActorAdded), payload("assignee"))
		require.True(t, f.isCandidate(s.agent, asAssignee), "control: an assignment of the agent wakes it")
		asReviewer := helpers.InsertEvent(t, testDB, f.wsID, s.taskID, ownerID, 0, string(eventbus.TaskActorAdded), payload("reviewer"))
		require.False(t, f.isCandidate(s.agent, asReviewer))
	})

	t.Run("hand-off to the agent starts task work", func(t *testing.T) {
		agent := f.agent(nil)
		taskPub := f.task("Handed to an agent")
		taskID := f.taskInternalID(taskPub)
		since := f.mark()
		doJSON(t, http.MethodPost, testServerURL+"/tasks/"+taskPub+"/handoff/to-agent", f.tt.AccessToken,
			map[string]any{"agentId": agent.PublicID}, nil)
		ev := f.eventOf(taskID, eventbus.AgentTaskHandoffToAgent, since)
		require.True(t, f.isCandidate(agent, ev.ID))
		f.requireRunMode(agent, taskID, since, ev, "task_work")
	})

	t.Run("run request starts task work", func(t *testing.T) {
		s := f.scene(helpers.TaskAgentOptions{})
		since := f.mark()
		status, raw := f.runNow(f.tt, s.taskPub, s.agent.PublicID)
		require.Equal(t, http.StatusAccepted, status, string(raw))
		ev := f.eventOf(s.taskID, eventbus.AgentTaskRunRequested, since)
		require.True(t, f.isCandidate(s.agent, ev.ID))
		f.requireRunMode(s.agent, s.taskID, since, ev, "task_work")
	})

	// Each case below takes one condition away after the comment was made.
	losses := []struct {
		name   string
		mutate func(s atScene)
	}{
		{"grants are an empty object", func(s atScene) {
			doJSON(t, http.MethodPatch, testServerURL+"/workspaces/"+f.tt.WorkspacePublicID+"/ai/agents/"+s.agent.PublicID+"/tools",
				f.tt.AccessToken, map[string]any{"tools": map[string]string{}}, nil)
		}},
		{"grants are JSON null", func(s atScene) { setToolsJSON(t, s.agent, "CAST('null' AS JSON)") }},
		{"grants are SQL NULL", func(s atScene) { setToolsJSON(t, s.agent, "NULL") }},
		{"grants are the old list shape", func(s atScene) { setToolsJSON(t, s.agent, `JSON_ARRAY('add_comment')`) }},
		{"agent is paused", func(s atScene) { f.setPaused(s.agent, true) }},
		{"assignment is switched off", func(s atScene) {
			doJSON(t, http.MethodDelete, testServerURL+"/tasks/"+s.taskPub+"/actors/"+s.actorID, f.tt.AccessToken, nil, nil)
		}},
		{"task is done", func(s atScene) {
			for _, step := range []string{"start", "submit", "complete"} {
				doJSON(t, http.MethodPost, testServerURL+"/tasks/"+s.taskPub+"/transitions", f.tt.AccessToken,
					map[string]any{"transition": step}, nil)
			}
		}},
		{"task is archived", func(s atScene) {
			doJSON(t, http.MethodPost, testServerURL+"/tasks/"+s.taskPub+"/archive", f.tt.AccessToken, nil, nil)
		}},
		{"task is removed", func(s atScene) {
			doJSON(t, http.MethodDelete, testServerURL+"/tasks/"+s.taskPub, f.tt.AccessToken, nil, nil)
		}},
		{"task is project-visible", func(s atScene) {
			doJSON(t, http.MethodPatch, testServerURL+"/tasks/"+s.taskPub, f.tt.AccessToken, map[string]any{"visibility": "project"}, nil)
		}},
		{"task is private", func(s atScene) {
			doJSON(t, http.MethodPatch, testServerURL+"/tasks/"+s.taskPub, f.tt.AccessToken, map[string]any{"visibility": "private"}, nil)
		}},
	}
	for _, tc := range losses {
		t.Run(tc.name+" wakes nobody", func(t *testing.T) {
			s := f.scene(helpers.TaskAgentOptions{})
			since := f.mark()
			f.comment(f.tt, s.taskPub, "anyone?")
			ev := f.eventOf(s.taskID, eventbus.TaskCommentAdded, since)
			require.True(t, f.isCandidate(s.agent, ev.ID), "control: before the change the comment wakes the agent")

			tc.mutate(s)
			require.False(t, f.isCandidate(s.agent, ev.ID))
			require.Empty(t, agentRunsOf(f.runSince(since), s.agent), "the trigger path starts no run")
		})
	}

	t.Run("subscribed event on an assigned task is a tick", func(t *testing.T) {
		s := f.scene(helpers.TaskAgentOptions{ScheduleKind: "on_event", EventTriggerTypes: []string{string(eventbus.TaskUpdated)}})
		stepCalls := helpers.CountInvocations(t, testDB, f.wsID, "agent_task_step")
		since := f.mark()
		doJSON(t, http.MethodPatch, testServerURL+"/tasks/"+s.taskPub, f.tt.AccessToken,
			map[string]any{"priority": awOtherPriority(f.getTask(s.taskPub).Priority)}, nil)
		ev := f.eventOf(s.taskID, eventbus.TaskUpdated, since)
		require.True(t, f.isCandidate(s.agent, ev.ID))
		f.requireRunMode(s.agent, s.taskID, since, ev, "tick")
		require.Equal(t, stepCalls, helpers.CountInvocations(t, testDB, f.wsID, "agent_task_step"), "a tick makes no task-work call")
	})
}

// setToolsJSON writes a stored grants value no API can produce, the shapes
// agents had before grants became per-operation objects.
func setToolsJSON(t *testing.T, agent *helpers.TaskAgent, expr string) {
	t.Helper()
	_, err := testDB.Exec(`UPDATE ai_agents SET tools_json = `+expr+` WHERE id = ?`, agent.ID)
	require.NoError(t, err)
}

// TestAgentTools_CreatedDefault: an agent created through the API starts
// with every operation on approval, and nothing on auto.
func TestAgentTools_CreatedDefault(t *testing.T) {
	bootstrap(t)
	t.Parallel()

	f := newAwFixture(t)
	agent := f.agent(nil)
	want := helpers.GrantAll("approval")

	var list struct {
		Agents []struct {
			ID    string            `json:"id"`
			Kind  string            `json:"kind"`
			Tools map[string]string `json:"tools"`
		} `json:"agents"`
	}
	doJSON(t, http.MethodGet, testServerURL+"/workspaces/"+f.tt.WorkspacePublicID+"/ai/agents", f.tt.AccessToken, nil, &list)
	require.Len(t, list.Agents, 1)
	require.Equal(t, agent.PublicID, list.Agents[0].ID)
	require.Equal(t, "task_agent", list.Agents[0].Kind)
	require.Equal(t, want, list.Agents[0].Tools)

	var stored string
	require.NoError(t, testDB.QueryRow(`SELECT CAST(tools_json AS CHAR) FROM ai_agents WHERE id = ?`, agent.ID).Scan(&stored))
	require.JSONEq(t, `{"add_comment":"approval","set_priority":"approval","set_due_on":"approval","transition_task":"approval","create_subtask":"approval"}`, stored)

	// The task's agent list tells the UI whether a run request can succeed.
	taskPub := f.task("Default grants")
	f.assign(f.tt, taskPub, agent)
	var roster struct {
		Agents []struct {
			AgentID   string `json:"agentId"`
			Kind      string `json:"kind"`
			Paused    bool   `json:"paused"`
			HasGrants bool   `json:"hasGrants"`
		} `json:"agents"`
	}
	doJSON(t, http.MethodGet, testServerURL+"/tasks/"+taskPub+"/agents", f.tt.AccessToken, nil, &roster)
	require.Len(t, roster.Agents, 1)
	require.Equal(t, agent.PublicID, roster.Agents[0].AgentID)
	require.Equal(t, "task_agent", roster.Agents[0].Kind)
	require.False(t, roster.Agents[0].Paused)
	require.True(t, roster.Agents[0].HasGrants)
}

// TestAgentTools_Update: the grants are replaced as a whole by an admin,
// refuse unknown entries, and cannot be set on a signal judge.
func TestAgentTools_Update(t *testing.T) {
	bootstrap(t)
	t.Parallel()

	f := newAwFixture(t)
	agent := f.agent(nil)
	url := testServerURL + "/workspaces/" + f.tt.WorkspacePublicID + "/ai/agents/" + agent.PublicID + "/tools"
	stored := func() string {
		var s string
		require.NoError(t, testDB.QueryRow(`SELECT CAST(tools_json AS CHAR) FROM ai_agents WHERE id = ?`, agent.ID).Scan(&s))
		return s
	}

	var saved struct {
		Tools map[string]string `json:"tools"`
	}
	doJSON(t, http.MethodPatch, url, f.tt.AccessToken,
		map[string]any{"tools": map[string]string{"add_comment": "auto", "set_priority": "approval"}}, &saved)
	require.Equal(t, map[string]string{"add_comment": "auto", "set_priority": "approval"}, saved.Tools)
	require.JSONEq(t, `{"add_comment":"auto","set_priority":"approval"}`, stored(), "the update replaces the whole set")

	for name, body := range map[string]map[string]any{
		"unknown operation": {"tools": map[string]any{"delete_task": "auto"}},
		"unknown mode":      {"tools": map[string]any{"add_comment": "always"}},
		"null mode":         {"tools": map[string]any{"add_comment": nil}},
	} {
		t.Run(name, func(t *testing.T) {
			status, raw := doJSONStatus(t, http.MethodPatch, url, f.tt.AccessToken, body)
			requireDenied(t, status, raw, apierrors.WsAgentToolsUnknownEntry.Status, apierrors.WsAgentToolsUnknownEntry.Code, name)
		})
	}
	status, _ := doJSONStatus(t, http.MethodPatch, url, f.tt.AccessToken, map[string]any{})
	require.Equal(t, http.StatusUnprocessableEntity, status, "a body without tools is refused")
	require.JSONEq(t, `{"add_comment":"auto","set_priority":"approval"}`, stored(), "refused updates change nothing")

	var emptied struct {
		Tools map[string]string `json:"tools"`
	}
	doJSON(t, http.MethodPatch, url, f.tt.AccessToken, map[string]any{"tools": map[string]string{}}, &emptied)
	require.Empty(t, emptied.Tools)
	require.JSONEq(t, `{}`, stored(), "an empty object grants nothing")

	judge := helpers.SeedAgent(t, testDB, f.tt.WorkspacePublicID, helpers.SeedAgentOptions{Kind: "signal_judge"})
	status, raw := doJSONStatus(t, http.MethodPatch,
		testServerURL+"/workspaces/"+f.tt.WorkspacePublicID+"/ai/agents/"+judge.AgentPublicID+"/tools",
		f.tt.AccessToken, map[string]any{"tools": helpers.GrantAll("auto")})
	requireDenied(t, status, raw, apierrors.WsAgentToolsKindNotGrantable.Status, apierrors.WsAgentToolsKindNotGrantable.Code, "tools on a signal judge")

	other := newTenant(t)
	status, raw = doJSONStatus(t, http.MethodPatch,
		testServerURL+"/workspaces/"+other.WorkspacePublicID+"/ai/agents/"+agent.PublicID+"/tools",
		other.AccessToken, map[string]any{"tools": helpers.GrantAll("auto")})
	requireDenied(t, status, raw, apierrors.AiAgentNotFound.Status, apierrors.AiAgentNotFound.Code, "another workspace's agent")
	require.JSONEq(t, `{}`, stored())

	member := seedProjectRoleMember(t, f.tt, "editor")
	status, raw = doJSONStatus(t, http.MethodPatch, url, member.AccessToken, map[string]any{"tools": helpers.GrantAll("auto")})
	require.Equalf(t, http.StatusForbidden, status, "a member who is not a workspace admin cannot grant, body=%s", string(raw))
	require.JSONEq(t, `{}`, stored())
}

// TestAgentTools_SignalJudgeListed: a signal judge appears in the agent list
// with no grants, next to a task agent that has them.
func TestAgentTools_SignalJudgeListed(t *testing.T) {
	bootstrap(t)
	t.Parallel()

	f := newAwFixture(t)
	agent := f.agent(nil)
	judge := helpers.SeedAgent(t, testDB, f.tt.WorkspacePublicID, helpers.SeedAgentOptions{Kind: "signal_judge"})

	var list struct {
		Agents []struct {
			ID    string            `json:"id"`
			Kind  string            `json:"kind"`
			Tools map[string]string `json:"tools"`
		} `json:"agents"`
	}
	doJSON(t, http.MethodGet, testServerURL+"/workspaces/"+f.tt.WorkspacePublicID+"/ai/agents", f.tt.AccessToken, nil, &list)
	require.Len(t, list.Agents, 2)
	for _, a := range list.Agents {
		switch a.ID {
		case judge.AgentPublicID:
			require.Equal(t, "signal_judge", a.Kind)
			require.Nil(t, a.Tools, "a signal judge shows no grants")
		case agent.PublicID:
			require.Equal(t, helpers.GrantAll("approval"), a.Tools)
		default:
			require.FailNow(t, "unexpected agent in the list", a.ID)
		}
	}
}

// TestAgentTools_UpgradedRowIsInert: an agent whose stored grants are
// whatever it had before per-operation grants existed does nothing until
// an admin sets them, however it is triggered.
func TestAgentTools_UpgradedRowIsInert(t *testing.T) {
	bootstrap(t)
	t.Parallel()

	f := newAwFixture(t)
	shapes := []struct {
		name string
		expr string
	}{
		{"empty object", `JSON_OBJECT()`},
		{"JSON null", `CAST('null' AS JSON)`},
		{"SQL NULL", `NULL`},
		{"old list", `JSON_ARRAY('add_comment','set_priority')`},
	}
	for _, shape := range shapes {
		t.Run(shape.name, func(t *testing.T) {
			s := f.scene(helpers.TaskAgentOptions{})
			setToolsJSON(t, s.agent, shape.expr)

			before := helpers.CountInvocations(t, testDB, f.wsID, "agent_task_step")
			since := f.mark()
			f.comment(f.tt, s.taskPub, "does anyone listen?")
			require.Empty(t, agentRunsOf(f.runSince(since), s.agent))
			require.Equal(t, before, helpers.CountInvocations(t, testDB, f.wsID, "agent_task_step"))
			require.Empty(t, f.runs(s.taskPub), "no run is even recorded")

			f.requireRunRefused(f.tt, s.taskPub, s.agent.PublicID, apierrors.WsTaskAgentNoGrants, s.taskID)
		})
	}
}
