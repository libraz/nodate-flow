package taskservice_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/types"
	apierrors "github.com/libraz/nodate-flow/apps/flow-api/internal/errors"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/execution"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/taskservice"
)

func requireSpec(t *testing.T, err error, spec *apierrors.Spec) {
	t.Helper()
	var apiErr *apierrors.APIError
	require.True(t, errors.As(err, &apiErr), "want %s, got %v", spec.Code, err)
	require.Equal(t, spec.Code, apiErr.Spec.Code, "got %v", err)
}

// propose queues one action under approval mode and restores auto grants
// for later steps the test drives directly.
func (k *agentKit) propose(a taskservice.Action) types.PublicID {
	k.t.Helper()
	k.grantAll("approval")
	out := k.do(a)
	requireOutcome(k.t, taskservice.StepProposed, out)
	k.grantAll("auto")
	return out.ProposalID
}

func (k *agentKit) owner() execution.Principal {
	k.t.Helper()
	p, err := execution.NewUserPrincipal(k.f.workspaceID, k.f.userID)
	require.NoError(k.t, err)
	return p
}

func (k *agentKit) user(id uint32) execution.Principal {
	k.t.Helper()
	p, err := execution.NewUserPrincipal(k.f.workspaceID, id)
	require.NoError(k.t, err)
	return p
}

func (k *agentKit) decide(approver execution.Principal, id types.PublicID, verdict, reason string) (taskservice.DecideResult, error) {
	return k.svc.Decide(k.ctx, approver, k.f.taskPublicID, id, taskservice.Decision{Verdict: verdict, Reason: reason})
}

func (k *agentKit) proposalRow(id types.PublicID) (status string, errorCode, reason *string, decidedBy *uint32) {
	k.t.Helper()
	require.NoError(k.t, k.db.QueryRowContext(k.ctx, `SELECT status, error_code, decision_reason, decided_by_user_id FROM agent_action_proposals WHERE public_id = ?`, id).
		Scan(&status, &errorCode, &reason, &decidedBy))
	return
}

func (k *agentKit) audits(action string) int {
	return k.count(`SELECT COUNT(*) FROM audit_logs WHERE workspace_id = ? AND action = ?`, k.f.workspaceID, action)
}

func TestDecide_ApproveApplies(t *testing.T) {
	k := newAgentKit(t)
	id := k.propose(priorityAction(3))

	res, err := k.decide(k.owner(), id, taskservice.VerdictApprove, "")
	require.NoError(t, err)
	require.Equal(t, taskservice.DecideApplied, res.Status)
	require.Equal(t, int32(3), k.task().priority)

	payload, actorUser, actorAgent := k.lastEvent("task.updated")
	require.False(t, actorUser.Valid)
	require.Equal(t, int64(k.f.agentID), int64(actorAgent.Int32))
	require.Equal(t, k.f.userPublicID.String(), payload["approvedByUserId"])
	require.Equal(t, id.String(), payload["proposalId"])
	require.NotEmpty(t, payload["runKey"])

	status, _, _, decidedBy := k.proposalRow(id)
	require.Equal(t, "applied", status)
	require.Equal(t, k.f.userID, *decidedBy)
	decided, decidedUser, _ := k.lastEvent("agent.action.decided")
	require.Equal(t, int64(k.f.userID), int64(decidedUser.Int32))
	require.Equal(t, "applied", decided["status"])
	require.Equal(t, id.String(), decided["proposalId"])
	require.Equal(t, 1, k.audits("ai_agent.proposal.approve"))

	_, err = k.decide(k.owner(), id, taskservice.VerdictApprove, "")
	requireSpec(t, err, apierrors.WsAgentProposalAlreadyDecided)
	_, err = k.decide(k.owner(), id, taskservice.VerdictReject, "")
	requireSpec(t, err, apierrors.WsAgentProposalAlreadyDecided)
}

func TestDecide_ApproveEachKind(t *testing.T) {
	k := newAgentKit(t)
	actions := []taskservice.Action{
		commentAction("approved comment"),
		{Kind: execution.ToolSetDueOn, DueOn: day("2030-03-04")},
		{Kind: execution.ToolTransitionTask, Transition: "start"},
		{Kind: execution.ToolCreateSubtask, Title: "Approved child", ProjectID: k.f.projectID},
	}
	for _, a := range actions {
		id := k.propose(a)
		res, err := k.decide(k.owner(), id, taskservice.VerdictApprove, "")
		require.NoError(t, err, "%s", a.Kind)
		require.Equal(t, taskservice.DecideApplied, res.Status)
	}
	require.Equal(t, 1, k.comments())
	require.Equal(t, "2030-03-04", k.task().dueOn.Time.Format("2006-01-02"))
	require.Equal(t, "waiting", k.task().state)
	require.Equal(t, 1, k.count(`SELECT COUNT(*) FROM tasks WHERE parent_task_id = ?`, k.f.taskID))
}

func TestDecide_Reject(t *testing.T) {
	k := newAgentKit(t)
	id := k.propose(priorityAction(3))

	_, err := k.decide(k.owner(), id, taskservice.VerdictReject, strings.Repeat("r", taskservice.MaxDecisionReasonRunes+1))
	require.ErrorIs(t, err, taskservice.ErrDecision)

	reason := strings.Repeat("理", taskservice.MaxDecisionReasonRunes)
	res, err := k.decide(k.owner(), id, taskservice.VerdictReject, reason)
	require.NoError(t, err)
	require.Equal(t, taskservice.DecideRejected, res.Status)
	require.Equal(t, int32(1), k.task().priority)
	status, _, stored, _ := k.proposalRow(id)
	require.Equal(t, "rejected", status)
	require.Equal(t, reason, *stored)
	decided, _, _ := k.lastEvent("agent.action.decided")
	require.Equal(t, "rejected", decided["status"])
	require.Equal(t, reason, decided["reason"])
	require.Equal(t, 1, k.audits("ai_agent.proposal.reject"))

	// An empty reason is accepted.
	id = k.propose(priorityAction(2))
	_, err = k.decide(k.owner(), id, taskservice.VerdictReject, "")
	require.NoError(t, err)
}

func TestDecide_Stale(t *testing.T) {
	cases := []struct {
		name   string
		action func(k *agentKit) taskservice.Action
		change func(k *agentKit)
		code   *apierrors.Spec
	}{
		{"set_priority", func(*agentKit) taskservice.Action { return priorityAction(3) },
			func(k *agentKit) { k.mustSQL(`UPDATE tasks SET priority = 2 WHERE id = ?`, k.f.taskID) }, apierrors.WsTaskUpdateConflict},
		{"set_due_on", func(*agentKit) taskservice.Action {
			return taskservice.Action{Kind: execution.ToolSetDueOn, DueOn: day("2030-01-01")}
		}, func(k *agentKit) { k.mustSQL(`UPDATE tasks SET due_on = '2029-01-01' WHERE id = ?`, k.f.taskID) }, apierrors.WsTaskUpdateConflict},
		{"transition", func(*agentKit) taskservice.Action {
			return taskservice.Action{Kind: execution.ToolTransitionTask, Transition: "complete"}
		}, func(k *agentKit) {
			// start then unblock returns to open, so only the event id tells.
			requireOutcome(k.t, taskservice.StepApplied, k.do(taskservice.Action{Kind: execution.ToolTransitionTask, Transition: "start"}))
			requireOutcome(k.t, taskservice.StepApplied, k.do(taskservice.Action{Kind: execution.ToolTransitionTask, Transition: "unblock"}))
		}, apierrors.WsTaskUpdateConflict},
		{"add_comment", func(*agentKit) taskservice.Action { return commentAction("late") },
			func(k *agentKit) { k.mustSQL(`UPDATE tasks SET archived_at = NOW() WHERE id = ?`, k.f.taskID) }, apierrors.WsTaskAgentTaskNotWorkable},
		{"create_subtask", func(k *agentKit) taskservice.Action {
			return taskservice.Action{Kind: execution.ToolCreateSubtask, Title: "late", ProjectID: k.f.projectID}
		}, func(k *agentKit) { k.mustSQL(`UPDATE tasks SET archived_at = NOW() WHERE id = ?`, k.f.taskID) }, apierrors.WsTaskAgentTaskNotWorkable},
		{"agent_deleted", func(*agentKit) taskservice.Action { return priorityAction(3) },
			func(k *agentKit) { k.mustSQL(`UPDATE ai_agents SET enabled = FALSE WHERE id = ?`, k.f.agentID) }, apierrors.WsTaskAgentNotAssigned},
		{"grant_removed", func(*agentKit) taskservice.Action { return priorityAction(3) },
			func(k *agentKit) { k.grant(map[string]string{execution.ToolAddComment: "auto"}) }, apierrors.WsTaskAgentNotAssigned},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			k := newAgentKit(t)
			id := k.propose(c.action(k))
			c.change(k)
			snapshot, comments := k.task(), k.comments()
			res, err := k.decide(k.owner(), id, taskservice.VerdictApprove, "")
			requireSpec(t, err, apierrors.WsAgentProposalStale)
			require.Equal(t, taskservice.DecideStale, res.Status)
			require.Equal(t, c.code.Code, res.ErrorCode)
			status, code, _, _ := k.proposalRow(id)
			require.Equal(t, "stale", status)
			require.Equal(t, c.code.Code, *code)
			require.Equal(t, snapshot, k.task())
			require.Equal(t, comments, k.comments())
		})
	}
}

func TestDecide_Expired(t *testing.T) {
	k := newAgentKit(t)
	approve := k.propose(priorityAction(3))
	reject := k.propose(priorityAction(2))
	k.mustSQL(`UPDATE agent_action_proposals SET expires_at = NOW() - INTERVAL 1 MINUTE WHERE task_id = ?`, k.f.taskID)

	list, err := k.svc.ListProposals(k.ctx, k.owner(), k.f.taskPublicID, taskservice.ProposalFilter{Status: taskservice.ProposalFilterAll})
	require.NoError(t, err)
	require.Len(t, list, 2)
	for _, p := range list {
		require.Equal(t, "expired", p.Status)
	}
	pending, err := k.svc.ListProposals(k.ctx, k.owner(), k.f.taskPublicID, taskservice.ProposalFilter{Status: taskservice.ProposalFilterPending})
	require.NoError(t, err)
	require.Empty(t, pending)
	require.Equal(t, 2, k.proposals("pending"), "listing must not write")

	res, err := k.decide(k.owner(), approve, taskservice.VerdictApprove, "")
	requireSpec(t, err, apierrors.WsAgentProposalExpired)
	require.Equal(t, taskservice.DecideExpired, res.Status)
	_, err = k.decide(k.owner(), reject, taskservice.VerdictReject, "late")
	requireSpec(t, err, apierrors.WsAgentProposalExpired)
	require.Equal(t, 2, k.proposals("expired"))
	require.Equal(t, int32(1), k.task().priority)
	_, _, _, decidedBy := k.proposalRow(approve)
	require.Nil(t, decidedBy)
}

func TestDecide_Paused(t *testing.T) {
	k := newAgentKit(t)
	id := k.propose(priorityAction(3))
	k.mustSQL(`UPDATE ai_agents SET paused = TRUE WHERE id = ?`, k.f.agentID)
	res, err := k.decide(k.owner(), id, taskservice.VerdictApprove, "")
	requireSpec(t, err, apierrors.AiAgentPaused)
	require.Equal(t, taskservice.DecidePaused, res.Status)
	require.Equal(t, 1, k.proposals("pending"))
	require.Equal(t, int32(1), k.task().priority)
}

func TestDecide_FailedRollsBack(t *testing.T) {
	k := newAgentKit(t)
	// Scheduling the projection writes the due date, so it goes first.
	seedBrokenProjection(k)
	id := k.propose(taskservice.Action{Kind: execution.ToolSetDueOn, DueOn: day("2030-01-02")})
	before, updates := k.task(), k.events("task.updated")

	res, err := k.decide(k.owner(), id, taskservice.VerdictApprove, "")
	requireSpec(t, err, apierrors.ItemItemkitInvariantViolation)
	require.Equal(t, taskservice.DecideFailed, res.Status)
	status, code, _, decidedBy := k.proposalRow(id)
	require.Equal(t, "failed", status)
	require.Equal(t, apierrors.ItemItemkitInvariantViolation.Code, *code)
	require.Equal(t, k.f.userID, *decidedBy)
	require.Equal(t, before, k.task())
	require.Equal(t, updates, k.events("task.updated"))
	require.Zero(t, k.events("agent.action.decided"))
}

func TestDecide_ApproverLacksCapability(t *testing.T) {
	k := newAgentKit(t)
	id := k.propose(priorityAction(3))
	commenter, _ := k.addUser("commenter")
	events := k.allEvents()
	for _, verdict := range []string{taskservice.VerdictApprove, taskservice.VerdictReject} {
		_, err := k.decide(k.user(commenter), id, verdict, "")
		requireSpec(t, err, apierrors.WsProjectAccessDenied)
	}
	require.Equal(t, 1, k.proposals("pending"))
	require.Equal(t, events, k.allEvents())
	require.Equal(t, int32(1), k.task().priority)
}

func TestDecide_NotFound(t *testing.T) {
	k := newAgentKit(t)
	other := newAgentKit(t)
	foreign := other.propose(priorityAction(3))
	_, err := k.decide(k.owner(), foreign, taskservice.VerdictApprove, "")
	requireSpec(t, err, apierrors.WsAgentProposalNotFound)
	_, err = k.decide(k.owner(), types.New(), taskservice.VerdictApprove, "")
	requireSpec(t, err, apierrors.WsAgentProposalNotFound)
	_, err = k.svc.Decide(k.ctx, k.owner(), types.New(), foreign, taskservice.Decision{Verdict: taskservice.VerdictApprove})
	requireSpec(t, err, apierrors.WsTaskNotFound)
	// Another workspace's task is not found either.
	_, err = k.svc.Decide(k.ctx, k.owner(), other.f.taskPublicID, foreign, taskservice.Decision{Verdict: taskservice.VerdictApprove})
	requireSpec(t, err, apierrors.WsTaskNotFound)
}

func TestListProposals(t *testing.T) {
	k := newAgentKit(t)
	first := k.propose(priorityAction(3))
	second := k.propose(taskservice.Action{Kind: execution.ToolSetDueOn, DueOn: day("2031-01-01")})
	_, err := k.decide(k.owner(), first, taskservice.VerdictReject, "no")
	require.NoError(t, err)

	all, err := k.svc.ListProposals(k.ctx, k.owner(), k.f.taskPublicID, taskservice.ProposalFilter{Status: taskservice.ProposalFilterAll})
	require.NoError(t, err)
	require.Len(t, all, 2)
	require.Equal(t, second, all[0].ID)
	require.Equal(t, "pending", all[0].Status)
	require.Equal(t, "2031-01-01", *all[0].Args.DueOn)
	require.Equal(t, "rejected", all[1].Status)
	require.Equal(t, int32(3), *all[1].Args.Priority)
	require.Equal(t, int32(1), *all[1].Before.Priority)
	require.Equal(t, "no", all[1].DecisionReason)
	require.Equal(t, k.f.userPublicID, all[1].DecidedBy.ID)
	require.Equal(t, k.f.agentPublicID, all[1].AgentID)

	pending, err := k.svc.ListProposals(k.ctx, k.owner(), k.f.taskPublicID, taskservice.ProposalFilter{Status: taskservice.ProposalFilterPending})
	require.NoError(t, err)
	require.Len(t, pending, 1)

	_, err = k.svc.ListProposals(k.ctx, k.owner(), k.f.taskPublicID, taskservice.ProposalFilter{Status: "all", Limit: 101})
	require.ErrorIs(t, err, taskservice.ErrProposalFilter)
}
