package e2e

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/generated"
	apierrors "github.com/libraz/nodate-flow/apps/flow-api/internal/errors"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/eventbus"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/execution"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/notification"
	"github.com/libraz/nodate-flow/apps/flow-api/tests/helpers"
)

// proposeOn makes an agent with default grants (every operation on approval)
// propose one move on the given task, which it is assigned to by the owner.
// It returns the proposal that run created.
func (f *awFixture) proposeOn(agent *helpers.TaskAgent, taskPub, move string) awProposal {
	f.t.Helper()
	known := map[string]bool{}
	for _, p := range f.proposals(f.tt, taskPub, "all") {
		known[p.ID] = true
	}
	f.prov.Push(move, helpers.Finish("proposed"))
	since := f.mark()
	f.comment(f.tt, taskPub, "please propose something "+randomHex(3))
	f.requireOneCleanRun(f.runSince(since), taskPub, agent)
	var fresh []awProposal
	for _, p := range f.proposals(f.tt, taskPub, "pending") {
		if !known[p.ID] {
			fresh = append(fresh, p)
		}
	}
	require.Len(f.t, fresh, 1)
	require.Equal(f.t, "pending", fresh[0].Status)
	return fresh[0]
}

// proposeFresh creates a task assigned to the agent and proposes on it.
func (f *awFixture) proposeFresh(agent *helpers.TaskAgent, title, move string) (string, awProposal) {
	f.t.Helper()
	taskPub := f.task(title)
	f.assign(f.tt, taskPub, agent)
	return taskPub, f.proposeOn(agent, taskPub, move)
}

func (f *awFixture) approve(who *helpers.TestTenant, taskPub, proposalID string) (int, []byte) {
	f.t.Helper()
	return doJSONStatus(f.t, http.MethodPost,
		testServerURL+"/tasks/"+taskPub+"/agent-proposals/"+proposalID+"/approve", who.AccessToken, nil)
}

func (f *awFixture) reject(who *helpers.TestTenant, taskPub, proposalID, reason string) (int, []byte) {
	f.t.Helper()
	body := map[string]any{}
	if reason != "" {
		body["reason"] = reason
	}
	return doJSONStatus(f.t, http.MethodPost,
		testServerURL+"/tasks/"+taskPub+"/agent-proposals/"+proposalID+"/reject", who.AccessToken, body)
}

// requireApproved approves and asserts the success body.
func (f *awFixture) requireApproved(taskPub, proposalID string) {
	f.t.Helper()
	status, raw := f.approve(f.tt, taskPub, proposalID)
	require.Equalf(f.t, http.StatusOK, status, "approve body=%s", string(raw))
	var out struct {
		ID     string `json:"id"`
		Status string `json:"status"`
	}
	awDecode(f.t, raw, &out)
	require.Equal(f.t, proposalID, out.ID)
	require.Equal(f.t, "applied", out.Status)
}

func (f *awFixture) requireApproveRefused(who *helpers.TestTenant, taskPub, proposalID string, spec *apierrors.Spec) {
	f.t.Helper()
	status, raw := f.approve(who, taskPub, proposalID)
	requireDenied(f.t, status, raw, spec.Status, spec.Code, "approve "+spec.Code)
}

func (f *awFixture) setProposalExpiry(proposalID string, expiresAt time.Time) {
	f.t.Helper()
	res, err := testDB.Exec(`UPDATE agent_action_proposals SET expires_at = ? WHERE workspace_id = ? AND public_id = UUID_TO_BIN(?, 0)`,
		expiresAt.UTC(), f.wsID, proposalID)
	require.NoError(f.t, err)
	n, err := res.RowsAffected()
	require.NoError(f.t, err)
	require.EqualValues(f.t, 1, n)
}

// workspaceEventCount counts every event of the workspace.
func (f *awFixture) workspaceEventCount() int {
	f.t.Helper()
	return awCountRows(f.t, `SELECT COUNT(*) FROM events WHERE workspace_id = ?`, f.wsID)
}

// settledAuditCount reads the workspace's audit row count once the
// asynchronous writer has gone quiet.
func (f *awFixture) settledAuditCount() int {
	f.t.Helper()
	last := -1
	for i := 0; i < 40; i++ {
		n := awCountRows(f.t, `SELECT COUNT(*) FROM audit_logs WHERE workspace_id = ?`, f.wsID)
		if n == last {
			return n
		}
		last = n
		time.Sleep(150 * time.Millisecond)
	}
	require.FailNow(f.t, "audit log did not settle")
	return 0
}

// TestAgentProposal_ApproveApplies: approving a pending set_priority
// proposal changes the task, in the agent's name, with the approver and the
// proposal in the event payload.
func TestAgentProposal_ApproveApplies(t *testing.T) {
	bootstrap(t)
	t.Parallel()

	f := newAwFixture(t)
	agent := f.agent(nil)
	taskPub := f.task("Approve me")
	taskID := f.taskInternalID(taskPub)
	f.assign(f.tt, taskPub, agent)
	original := f.getTask(taskPub).Priority
	want := awOtherPriority(original)

	since := f.mark()
	p := f.proposeOn(agent, taskPub, helpers.Move(execution.ToolSetPriority, map[string]any{"priority": want}))

	require.Equal(t, "set_priority", p.Kind)
	require.NotNil(t, p.Args.Priority)
	require.Equal(t, want, *p.Args.Priority)
	require.NotNil(t, p.Before.Priority)
	require.Equal(t, original, *p.Before.Priority)
	require.Equal(t, agent.PublicID, p.Agent.ID)
	require.Equal(t, agent.Name, p.Agent.Name)
	require.Greater(t, p.ExpiresAt, p.CreatedAt)
	require.Equal(t, original, f.getTask(taskPub).Priority, "a pending proposal changes nothing")
	require.Equal(t, 1, f.proposalRows(taskID, "pending"))
	require.Empty(t, helpers.EventsOfType(t, testDB, f.wsID, taskID, string(eventbus.TaskUpdated), since))

	f.requireApproved(taskPub, p.ID)

	require.Equal(t, want, f.getTask(taskPub).Priority)
	require.Equal(t, "applied", f.proposalStatus(p.ID))
	updated := helpers.EventsOfType(t, testDB, f.wsID, taskID, string(eventbus.TaskUpdated), since)
	require.Len(t, updated, 1)
	require.Equal(t, agent.ID, updated[0].ActorAgentID)
	require.Zero(t, updated[0].ActorUserID)
	require.Equal(t, f.tt.UserPublicID, updated[0].Payload["approvedByUserId"])
	require.Equal(t, p.ID, updated[0].Payload["proposalId"])
	require.EqualValues(t, want, updated[0].Payload["priority"])
	awRequireUUID(t, updated[0].Payload["approvedByUserId"], "approvedByUserId")

	all := f.proposals(f.tt, taskPub, "all")
	require.Len(t, all, 1)
	require.Equal(t, "applied", all[0].Status)
	require.NotNil(t, all[0].DecidedBy)
	require.Equal(t, f.tt.UserPublicID, all[0].DecidedBy.ID)
	require.NotNil(t, all[0].DecidedAt)
	require.Empty(t, f.proposals(f.tt, taskPub, "pending"))

	decided := helpers.EventsOfType(t, testDB, f.wsID, taskID, string(eventbus.AgentActionDecided), since)
	require.Len(t, decided, 1)
	require.Equal(t, "applied", decided[0].Payload["status"])
	awAuditEventually(t, f.wsID, "ai_agent.proposal.approve", p.ID, true)
	awAuditEventually(t, f.wsID, "task.update", taskPub, false)
}

// TestAgentProposal_ApproveAppliesEveryKind: each of the five operations,
// proposed in one run and approved one by one, lands as the agent's change.
func TestAgentProposal_ApproveAppliesEveryKind(t *testing.T) {
	bootstrap(t)
	t.Parallel()

	f := newAwFixture(t)
	agent := f.agent(nil)
	taskPub := f.task("Five proposals")
	taskID := f.taskInternalID(taskPub)
	f.assign(f.tt, taskPub, agent)
	original := f.getTask(taskPub)
	wantPriority := awOtherPriority(original.Priority)
	f.prov.Push(
		helpers.Move(execution.ToolAddComment, map[string]any{"body": "A proposed note"}),
		helpers.Move(execution.ToolSetPriority, map[string]any{"priority": wantPriority}),
		helpers.Move(execution.ToolSetDueOn, map[string]any{"due_on": "2032-03-04"}),
		helpers.Move(execution.ToolTransitionTask, map[string]any{"transition": "start"}),
		helpers.Move(execution.ToolCreateSubtask, map[string]any{"title": "Proposed subtask"}),
		helpers.Finish("five proposals"),
	)

	since := f.mark()
	f.comment(f.tt, taskPub, "propose everything")
	f.requireOneCleanRun(f.runSince(since), taskPub, agent)

	pending := f.proposals(f.tt, taskPub, "pending")
	require.Len(t, pending, 5)
	byKind := map[string]awProposal{}
	for _, p := range pending {
		byKind[p.Kind] = p
	}
	require.Len(t, byKind, 5)
	require.Len(t, f.comments(taskPub), 1, "nothing was written before approval")
	require.Equal(t, original, f.getTask(taskPub))

	for _, kind := range []string{execution.ToolAddComment, execution.ToolSetPriority, execution.ToolSetDueOn, execution.ToolTransitionTask, execution.ToolCreateSubtask} {
		f.requireApproved(taskPub, byKind[kind].ID)
	}

	after := f.getTask(taskPub)
	require.Equal(t, wantPriority, after.Priority)
	require.Equal(t, "2032-03-04", after.DueOn)
	require.Equal(t, "waiting", after.DerivedState)
	comments := f.comments(taskPub)
	require.Len(t, comments, 2)
	require.Equal(t, "agent", comments[1].AuthorKind)
	require.Equal(t, "A proposed note", comments[1].Body)
	created := helpers.EventsOfType(t, testDB, f.wsID, 0, string(eventbus.TaskCreated), since)
	require.Len(t, created, 1)
	require.Equal(t, agent.ID, created[0].ActorAgentID)
	require.Equal(t, "Proposed subtask", created[0].Payload["title"])
	require.Equal(t, byKind[execution.ToolCreateSubtask].ID, created[0].Payload["proposalId"])
	require.Equal(t, f.tt.UserPublicID, created[0].Payload["approvedByUserId"])
	for _, p := range f.proposals(f.tt, taskPub, "all") {
		require.Equal(t, "applied", p.Status, p.Kind)
	}
	require.Equal(t, 5, f.proposalRows(taskID, "applied"))
}

// TestAgentProposal_DoubleDecide: a second verdict on a decided proposal is
// refused and changes nothing.
func TestAgentProposal_DoubleDecide(t *testing.T) {
	bootstrap(t)
	t.Parallel()

	f := newAwFixture(t)
	agent := f.agent(nil)
	taskPub := f.task("Decide twice")
	taskID := f.taskInternalID(taskPub)
	f.assign(f.tt, taskPub, agent)
	want := awOtherPriority(f.getTask(taskPub).Priority)
	p := f.proposeOn(agent, taskPub, helpers.Move(execution.ToolSetPriority, map[string]any{"priority": want}))

	f.requireApproved(taskPub, p.ID)
	since := f.mark()
	f.requireApproveRefused(f.tt, taskPub, p.ID, apierrors.WsAgentProposalAlreadyDecided)
	status, raw := f.reject(f.tt, taskPub, p.ID, "too late")
	requireDenied(t, status, raw, apierrors.WsAgentProposalAlreadyDecided.Status, apierrors.WsAgentProposalAlreadyDecided.Code, "reject after approve")

	require.Equal(t, "applied", f.proposalStatus(p.ID), "the first verdict stands")
	require.Equal(t, want, f.getTask(taskPub).Priority)
	require.Empty(t, helpers.EventsOfType(t, testDB, f.wsID, taskID, string(eventbus.TaskUpdated), since), "no second write")
	require.Empty(t, helpers.EventsOfType(t, testDB, f.wsID, taskID, string(eventbus.AgentActionDecided), since))
}

// TestAgentProposal_Reject: a rejection keeps the reason, applies nothing,
// starts no run, and reaches the agent's next prompt.
func TestAgentProposal_Reject(t *testing.T) {
	bootstrap(t)
	t.Parallel()

	f := newAwFixture(t)
	agent := f.agent(nil)
	taskPub := f.task("Reject me")
	taskID := f.taskInternalID(taskPub)
	f.assign(f.tt, taskPub, agent)
	original := f.getTask(taskPub).Priority
	p := f.proposeOn(agent, taskPub, helpers.Move(execution.ToolSetPriority, map[string]any{"priority": awOtherPriority(original)}))

	invocations := helpers.CountInvocations(t, testDB, f.wsID, "agent_task_step")
	runStarts := len(helpers.EventsOfType(t, testDB, f.wsID, taskID, string(eventbus.AiAgentRunStarted), 0))
	const reason = "Priority is set by the team lead only"
	status, raw := f.reject(f.tt, taskPub, p.ID, reason)
	require.Equalf(t, http.StatusOK, status, "reject body=%s", string(raw))
	var out struct {
		ID     string `json:"id"`
		Status string `json:"status"`
	}
	awDecode(t, raw, &out)
	require.Equal(t, p.ID, out.ID)
	require.Equal(t, "rejected", out.Status)

	require.Equal(t, "rejected", f.proposalStatus(p.ID))
	var stored string
	require.NoError(t, testDB.QueryRow(
		`SELECT decision_reason FROM agent_action_proposals WHERE workspace_id = ? AND public_id = UUID_TO_BIN(?, 0)`,
		f.wsID, p.ID).Scan(&stored))
	require.Equal(t, reason, stored)
	all := f.proposals(f.tt, taskPub, "all")
	require.Len(t, all, 1)
	require.Equal(t, "rejected", all[0].Status)
	require.Equal(t, reason, all[0].DecisionReason)
	require.NotNil(t, all[0].DecidedBy)
	require.Equal(t, original, f.getTask(taskPub).Priority)

	require.Equal(t, invocations, helpers.CountInvocations(t, testDB, f.wsID, "agent_task_step"), "rejecting calls no model")
	require.Equal(t, runStarts, len(helpers.EventsOfType(t, testDB, f.wsID, taskID, string(eventbus.AiAgentRunStarted), 0)), "rejecting starts no run")
	awAuditEventually(t, f.wsID, "ai_agent.proposal.reject", p.ID, true)

	// The reason is part of what the agent reads next time.
	f.prov.Push(helpers.Finish("noted"))
	since := f.mark()
	f.comment(f.tt, taskPub, "try again")
	f.runSince(since)
	reqs := f.prov.Requests()
	require.Contains(t, reqs[len(reqs)-1].Prompt, reason)
}

// TestAgentProposal_Stale: if the value a proposal targets changed after it
// was made, approving it applies nothing and leaves the person's value.
func TestAgentProposal_Stale(t *testing.T) {
	bootstrap(t)
	t.Parallel()

	f := newAwFixture(t)
	agent := f.agent(nil)

	t.Run("priority", func(t *testing.T) {
		taskPub := f.task("Stale priority")
		f.assign(f.tt, taskPub, agent)
		orig := f.getTask(taskPub).Priority
		proposed, human := awOtherPriority(orig), awOtherPriority(awOtherPriority(orig))
		p := f.proposeOn(agent, taskPub, helpers.Move(execution.ToolSetPriority, map[string]any{"priority": proposed}))

		doJSON(t, http.MethodPatch, testServerURL+"/tasks/"+taskPub, f.tt.AccessToken, map[string]any{"priority": human}, nil)
		f.requireApproveRefused(f.tt, taskPub, p.ID, apierrors.WsAgentProposalStale)

		require.Equal(t, "stale", f.proposalStatus(p.ID))
		require.Equal(t, human, f.getTask(taskPub).Priority, "the person's value is kept")
		require.Equal(t, "stale", f.proposals(f.tt, taskPub, "all")[0].Status)
	})

	t.Run("due_on", func(t *testing.T) {
		taskPub := f.task("Stale due date")
		f.assign(f.tt, taskPub, agent)
		p := f.proposeOn(agent, taskPub, helpers.Move(execution.ToolSetDueOn, map[string]any{"due_on": "2033-01-10"}))

		doJSON(t, http.MethodPatch, testServerURL+"/tasks/"+taskPub, f.tt.AccessToken, map[string]any{"dueOn": "2033-02-20"}, nil)
		f.requireApproveRefused(f.tt, taskPub, p.ID, apierrors.WsAgentProposalStale)

		require.Equal(t, "stale", f.proposalStatus(p.ID))
		require.Equal(t, "2033-02-20", f.getTask(taskPub).DueOn)
	})

	t.Run("transition", func(t *testing.T) {
		taskPub := f.task("Stale transition")
		taskID := f.taskInternalID(taskPub)
		f.assign(f.tt, taskPub, agent)
		p := f.proposeOn(agent, taskPub, helpers.Move(execution.ToolTransitionTask, map[string]any{"transition": "start"}))

		doJSON(t, http.MethodPost, testServerURL+"/tasks/"+taskPub+"/transitions", f.tt.AccessToken,
			map[string]any{"transition": "block"}, nil)
		f.requireApproveRefused(f.tt, taskPub, p.ID, apierrors.WsAgentProposalStale)

		require.Equal(t, "stale", f.proposalStatus(p.ID))
		require.Equal(t, "waiting", f.getTask(taskPub).DerivedState, "the person's transition stands")
		require.Empty(t, helpers.EventsOfType(t, testDB, f.wsID, taskID, string(eventbus.TaskTransitionStart), 0),
			"the agent's start was not applied")
	})
}

// TestAgentProposal_Expired: an overdue proposal cannot be approved or
// rejected, and reads as expired afterwards.
func TestAgentProposal_Expired(t *testing.T) {
	bootstrap(t)
	t.Parallel()

	f := newAwFixture(t)
	agent := f.agent(nil)
	taskPub := f.task("Expiring proposals")
	f.assign(f.tt, taskPub, agent)
	orig := f.getTask(taskPub).Priority
	byApprove := f.proposeOn(agent, taskPub, helpers.Move(execution.ToolSetPriority, map[string]any{"priority": awOtherPriority(orig)}))
	byReject := f.proposeOn(agent, taskPub, helpers.Move(execution.ToolSetDueOn, map[string]any{"due_on": "2034-04-04"}))

	f.setProposalExpiry(byApprove.ID, time.Now().Add(-time.Hour))
	f.setProposalExpiry(byReject.ID, time.Now().Add(-time.Hour))
	require.Empty(t, f.proposals(f.tt, taskPub, "pending"), "an overdue proposal is not listed as pending")

	f.requireApproveRefused(f.tt, taskPub, byApprove.ID, apierrors.WsAgentProposalExpired)
	require.Equal(t, "expired", f.proposalStatus(byApprove.ID))
	require.Equal(t, orig, f.getTask(taskPub).Priority)

	status, raw := f.reject(f.tt, taskPub, byReject.ID, "no longer needed")
	requireDenied(t, status, raw, apierrors.WsAgentProposalExpired.Status, apierrors.WsAgentProposalExpired.Code, "reject an expired proposal")
	require.Equal(t, "expired", f.proposalStatus(byReject.ID))
	require.Empty(t, f.getTask(taskPub).DueOn)
}

// TestAgentProposal_AgentPaused: approving while the agent is paused is
// refused and leaves the proposal pending, so it can be approved after
// the agent resumes.
func TestAgentProposal_AgentPaused(t *testing.T) {
	bootstrap(t)
	t.Parallel()

	f := newAwFixture(t)
	agent := f.agent(nil)
	taskPub := f.task("Paused agent")
	f.assign(f.tt, taskPub, agent)
	orig := f.getTask(taskPub).Priority
	want := awOtherPriority(orig)
	p := f.proposeOn(agent, taskPub, helpers.Move(execution.ToolSetPriority, map[string]any{"priority": want}))

	f.setPaused(agent, true)
	f.requireApproveRefused(f.tt, taskPub, p.ID, apierrors.AiAgentPaused)
	require.Equal(t, "pending", f.proposalStatus(p.ID))
	require.Equal(t, orig, f.getTask(taskPub).Priority)
	require.Len(t, f.proposals(f.tt, taskPub, "pending"), 1)

	f.setPaused(agent, false)
	f.requireApproved(taskPub, p.ID)
	require.Equal(t, want, f.getTask(taskPub).Priority)
}

// TestAgentProposal_ApplyFailedRollsBack: when applying an approved proposal
// is refused, the proposal is marked failed with its reason and the task,
// its events and the audit trail are exactly as before the attempt.
func TestAgentProposal_ApplyFailedRollsBack(t *testing.T) {
	bootstrap(t)
	t.Parallel()

	f := newAwFixture(t)
	agent := f.agent(nil)
	taskPub := f.task("Completion needs review")
	taskID := f.taskInternalID(taskPub)
	f.assign(f.tt, taskPub, agent)
	for _, step := range []string{"start", "submit"} {
		doJSON(t, http.MethodPost, testServerURL+"/tasks/"+taskPub+"/transitions", f.tt.AccessToken,
			map[string]any{"transition": step}, nil)
	}
	require.Equal(t, "review", f.getTask(taskPub).DerivedState)
	p := f.proposeOn(agent, taskPub, helpers.Move(execution.ToolTransitionTask, map[string]any{"transition": "complete"}))

	// Completion now needs a recorded review, which the agent cannot supply.
	doJSON(t, http.MethodPost, testServerURL+"/tasks/"+taskPub+"/completion-policy", f.tt.AccessToken,
		map[string]any{"policy": "review_required"}, nil)
	helpers.WaitReevalIdle(t, f.wsID)

	var updatedAt time.Time
	require.NoError(t, testDB.QueryRow(`SELECT updated_at FROM tasks WHERE workspace_id = ? AND id = ?`, f.wsID, taskID).Scan(&updatedAt))
	events := f.workspaceEventCount()
	audits := f.settledAuditCount()

	status, raw := f.approve(f.tt, taskPub, p.ID)
	spec := apierrors.WsTaskCompletionNeedsApproval
	requireDenied(t, status, raw, spec.Status, spec.Code, "approve a completion that needs review")

	require.Equal(t, "failed", f.proposalStatus(p.ID))
	var code string
	require.NoError(t, testDB.QueryRow(
		`SELECT error_code FROM agent_action_proposals WHERE workspace_id = ? AND public_id = UUID_TO_BIN(?, 0)`,
		f.wsID, p.ID).Scan(&code))
	require.Equal(t, spec.Code, code)
	all := f.proposals(f.tt, taskPub, "all")
	require.Equal(t, "failed", all[0].Status)
	require.Equal(t, spec.Code, all[0].ErrorCode)

	var afterUpdated time.Time
	var state string
	require.NoError(t, testDB.QueryRow(`SELECT updated_at, derived_state FROM tasks WHERE workspace_id = ? AND id = ?`, f.wsID, taskID).Scan(&afterUpdated, &state))
	require.Equal(t, "review", state)
	require.True(t, updatedAt.Equal(afterUpdated), "the task row was not touched")
	require.Equal(t, events, f.workspaceEventCount(), "a refused approval records no event")
	require.Equal(t, audits, f.settledAuditCount(), "a refused approval records no audit row")
}

// TestAgentProposal_Notifies: a proposal notifies the person who delegated
// the task; with no delegator it goes to the assignee, and with no assignee
// to the creator.
func TestAgentProposal_Notifies(t *testing.T) {
	bootstrap(t)
	t.Parallel()

	f := newAwFixture(t)
	agent := f.agent(nil)
	ctx := context.Background()
	fanout := func(eventID uint64) {
		t.Helper()
		n := notification.NewFanout(testDB, generated.New(testDB))
		n.SetTimeout(5 * time.Second)
		n.Hook()(ctx, f.wsID, string(eventbus.AgentActionProposed), eventID)
		require.NoError(t, n.Shutdown(ctxWithTimeout(t, 10*time.Second)))
	}
	recipients := func(eventID uint64) []uint32 {
		t.Helper()
		rows, err := testDB.Query(`SELECT recipient_user_id FROM notifications WHERE workspace_id = ? AND source_event_id = ? AND event_type = ?`,
			f.wsID, eventID, string(eventbus.AgentActionProposed))
		require.NoError(t, err)
		defer rows.Close()
		var ids []uint32
		for rows.Next() {
			var id uint32
			require.NoError(t, rows.Scan(&id))
			ids = append(ids, id)
		}
		require.NoError(t, rows.Err())
		return ids
	}
	proposalEvent := func(taskID uint32, since uint64) helpers.EventRecord {
		t.Helper()
		evs := helpers.EventsOfType(t, testDB, f.wsID, taskID, string(eventbus.AgentActionProposed), since)
		require.Len(t, evs, 1)
		return evs[0]
	}
	internal := func(who *helpers.TestTenant) uint32 { return lookupUserInternalID(ctx, t, testDB, who.UserPublicID) }
	owner := f.tt
	delegator := seedProjectRoleMember(t, owner, "editor")
	assignee := seedProjectRoleMember(t, owner, "editor")

	t.Run("delegator", func(t *testing.T) {
		taskPub := f.task("Delegated by an editor")
		taskID := f.taskInternalID(taskPub)
		f.assign(delegator, taskPub, agent)
		since := f.mark()
		f.proposeOn(agent, taskPub, helpers.Move(execution.ToolSetPriority, map[string]any{"priority": awOtherPriority(f.getTask(taskPub).Priority)}))
		ev := proposalEvent(taskID, since)
		require.Equal(t, agent.ID, ev.ActorAgentID)
		require.Equal(t, delegator.UserPublicID, ev.Payload["targetUserPublicId"])
		fanout(ev.ID)
		require.Equal(t, []uint32{internal(delegator)}, recipients(ev.ID))
	})

	t.Run("assignee when no delegator", func(t *testing.T) {
		var task struct {
			ID string `json:"id"`
		}
		doJSON(t, http.MethodPost, testServerURL+"/tasks", owner.AccessToken, map[string]any{
			"projectId": owner.ProjectPublicID, "title": "Assigned to an editor",
			"actors": []map[string]any{{"userId": assignee.UserPublicID, "role": "assignee"}},
		}, &task)
		taskPub := task.ID
		taskID := f.taskInternalID(taskPub)
		helpers.AssignAgentToTask(t, testDB, owner.WorkspacePublicID, taskPub, agent.ID)
		since := f.mark()
		f.proposeOn(agent, taskPub, helpers.Move(execution.ToolSetPriority, map[string]any{"priority": awOtherPriority(f.getTask(taskPub).Priority)}))
		ev := proposalEvent(taskID, since)
		fanout(ev.ID)
		got := recipients(ev.ID)
		require.Len(t, got, 1)
		require.Contains(t, got, internal(assignee), "the human assignee is told")
		require.NotContains(t, got, internal(owner))
	})

	t.Run("creator when no assignee", func(t *testing.T) {
		taskPub := f.task("Nobody assigned")
		taskID := f.taskInternalID(taskPub)
		var actors struct {
			Actors []struct {
				ID   string `json:"id"`
				Kind string `json:"kind"`
			} `json:"actors"`
		}
		doJSON(t, http.MethodGet, testServerURL+"/tasks/"+taskPub+"/actors", owner.AccessToken, nil, &actors)
		require.Len(t, actors.Actors, 1, "the creator starts as the only assignee")
		doJSON(t, http.MethodDelete, testServerURL+"/tasks/"+taskPub+"/actors/"+actors.Actors[0].ID, owner.AccessToken, nil, nil)
		helpers.AssignAgentToTask(t, testDB, owner.WorkspacePublicID, taskPub, agent.ID)
		since := f.mark()
		f.proposeOn(agent, taskPub, helpers.Move(execution.ToolSetPriority, map[string]any{"priority": awOtherPriority(f.getTask(taskPub).Priority)}))
		ev := proposalEvent(taskID, since)
		fanout(ev.ID)
		require.Equal(t, []uint32{internal(owner)}, recipients(ev.ID))
	})
}
