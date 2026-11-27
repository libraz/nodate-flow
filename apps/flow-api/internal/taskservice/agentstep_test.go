package taskservice_test

import (
	"database/sql"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/stretchr/testify/require"

	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/types"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/execution"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/mutationlog"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/taskservice"
)

func TestRecordStep(t *testing.T) {
	k := newAgentKit(t)
	step := newStep()
	out := k.do(commentAction("x"))
	err := k.svc.RecordStep(k.ctx, k.run(k.f.userID, k.f.userID), k.f.taskPublicID, step, taskservice.StepRecord{
		Tool:    execution.ToolAddComment,
		Thought: strings.Repeat("考", 300),
		Action:  commentAction(strings.Repeat("b", 300)),
		Outcome: out,
	})
	require.NoError(t, err)
	payload, actorUser, actorAgent := k.lastEvent("agent.task.step")
	require.False(t, actorUser.Valid)
	require.Equal(t, int64(k.f.agentID), int64(actorAgent.Int32))
	require.Equal(t, step.RunKey, payload["runKey"])
	require.Equal(t, float64(step.Step), payload["step"])
	require.Equal(t, execution.ToolAddComment, payload["tool"])
	require.Equal(t, "applied", payload["outcome"])
	require.Equal(t, taskservice.MaxStepTextRunes, utf8.RuneCountInString(payload["thought"].(string)))
	require.Len(t, payload["argsSummary"].(string), taskservice.MaxStepTextRunes)
	require.NotContains(t, payload, "proposalId")

	// A stopped step is still recorded once the agent is unassigned.
	k.mustSQL(`UPDATE task_actors SET enabled = FALSE WHERE task_id = ? AND kind = 'agent'`, k.f.taskID)
	stopped := k.do(priorityAction(3))
	requireOutcome(t, taskservice.StepStopped, stopped)
	require.NoError(t, k.svc.RecordStep(k.ctx, k.run(0, 0), k.f.taskPublicID, newStep(), taskservice.StepRecord{
		Tool: execution.ToolSetPriority, Action: priorityAction(3), Outcome: stopped,
	}))
	payload, _, _ = k.lastEvent("agent.task.step")
	require.Equal(t, "stopped", payload["outcome"])
	require.Equal(t, stopped.ErrorCode, payload["errorCode"])
	require.Equal(t, 2, k.events("agent.task.step"))

	err = k.svc.RecordStep(k.ctx, k.run(0, 0), k.f.taskPublicID, newStep(), taskservice.StepRecord{Outcome: taskservice.Outcome{Result: "maybe"}})
	require.ErrorIs(t, err, taskservice.ErrStepOutcome)
}

func TestReadForAgent(t *testing.T) {
	k := newAgentKit(t)
	k.mustSQL(`UPDATE tasks SET description = ? WHERE id = ?`, strings.Repeat("d", taskservice.MaxContextDescriptionRunes+5), k.f.taskID)
	for i := range taskservice.MaxContextComments + 2 {
		body := "c"
		if i == taskservice.MaxContextComments+1 {
			body = strings.Repeat("L", taskservice.MaxContextCommentRunes+1)
		}
		requireOutcome(t, taskservice.StepApplied, k.do(commentAction(body+strings.Repeat(".", i))))
	}
	requireOutcome(t, taskservice.StepApplied, k.do(taskservice.Action{Kind: execution.ToolCreateSubtask, Title: "Child", ProjectID: k.f.projectID}))
	k.grantAll("approval")
	pending := k.do(priorityAction(3))
	requireOutcome(t, taskservice.StepProposed, pending)

	c, err := k.svc.ReadForAgent(k.ctx, k.principal(), k.f.taskPublicID)
	require.NoError(t, err)
	require.True(t, c.Task.DescriptionTruncated)
	require.Equal(t, taskservice.MaxContextDescriptionRunes, utf8.RuneCountInString(c.Task.Description))
	require.Equal(t, k.f.projectID, c.ProjectID)
	require.Len(t, c.Comments, taskservice.MaxContextComments)
	last := c.Comments[len(c.Comments)-1]
	require.True(t, last.Truncated, "newest comment last")
	require.Equal(t, taskservice.AuthorKindAgent, last.AuthorKind)
	require.Equal(t, k.f.agentName, last.AuthorName)
	require.Len(t, c.Children, 1)
	require.Len(t, c.PendingProposals, 1)
	require.Equal(t, pending.ProposalID, c.PendingProposals[0].ID)
	require.Equal(t, execution.GrantModeApproval, c.Grants[execution.ToolSetPriority])
	var maxID int64
	require.NoError(t, k.db.QueryRowContext(k.ctx, `SELECT MAX(id) FROM events WHERE task_id = ?`, k.f.taskID).Scan(&maxID))
	require.Equal(t, maxID, c.ContextEventID)
	var eventPublicID types.PublicID
	require.NoError(t, k.db.QueryRowContext(k.ctx, `SELECT public_id FROM events WHERE id = ?`, maxID).Scan(&eventPublicID))
	require.Equal(t, eventPublicID, c.ContextEventPublicID)
	var policy string
	require.NoError(t, k.db.QueryRowContext(k.ctx, `SELECT completion_policy FROM tasks WHERE id = ?`, k.f.taskID).Scan(&policy))
	require.Equal(t, policy, string(c.Task.CompletionPolicy))

	_, err = k.decide(k.owner(), pending.ProposalID, taskservice.VerdictReject, "not now")
	require.NoError(t, err)
	c, err = k.svc.ReadForAgent(k.ctx, k.principal(), k.f.taskPublicID)
	require.NoError(t, err)
	require.Empty(t, c.PendingProposals)
	require.Len(t, c.RejectedProposals, 1)
	require.Equal(t, "not now", c.RejectedProposals[0].Reason)
}

func TestWriteComment_AgentSkipsMentions(t *testing.T) {
	k := newCommentKit(t, true)
	written, err := k.write(taskservice.WriteArgs{
		Actor:         mutationlog.Actor{AgentID: k.f.agentID, WorkspaceID: k.f.workspaceID},
		TaskID:        k.f.taskID,
		TaskPublicID:  k.f.taskPublicID,
		AuthorAgentID: sql.NullInt64{Int64: int64(k.f.agentID), Valid: true},
		Body:          k.mentionBody("Agent"),
	})
	require.NoError(t, err)
	require.Equal(t, 0, k.mentionCount(written.ID))
	var n int
	require.NoError(t, k.db.QueryRowContext(k.ctx, `SELECT COUNT(*) FROM events WHERE workspace_id = ? AND type = 'mention.created'`,
		k.f.workspaceID).Scan(&n))
	require.Zero(t, n)

	// A human comment with the same body still mentions.
	human, err := k.write(k.userArgs(k.mentionBody("Human")))
	require.NoError(t, err)
	require.Equal(t, 1, k.mentionCount(human.ID))
}
