package taskservice_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/dbretry"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/types"
	apierrors "github.com/libraz/nodate-flow/apps/flow-api/internal/errors"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/execution"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/itemkit"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/taskservice"
)

func priorityAction(p int32) taskservice.Action {
	return taskservice.Action{Kind: execution.ToolSetPriority, Priority: p}
}

func commentAction(body string) taskservice.Action {
	return taskservice.Action{Kind: execution.ToolAddComment, Body: body}
}

func TestExecute_SetPriority(t *testing.T) {
	for _, p := range []int32{0, 4} {
		k := newAgentKit(t)
		requireOutcome(t, taskservice.StepApplied, k.do(priorityAction(p)))
		require.Equal(t, p, k.task().priority)
		payload, actorUser, actorAgent := k.lastEvent("task.updated")
		require.False(t, actorUser.Valid)
		require.Equal(t, int64(k.f.agentID), int64(actorAgent.Int32))
		require.Equal(t, "agent", payload["via"])
		require.NotEmpty(t, payload["runKey"])
	}

	k := newAgentKit(t)
	before := k.allEvents()
	for _, p := range []int32{-1, 5, 1 << 30} {
		out := k.do(priorityAction(p))
		requireOutcome(t, taskservice.StepInvalid, out)
		require.Equal(t, apierrors.ValidationBodyFieldInvalid.Code, out.ErrorCode)
	}
	// The fixture task is at priority 1.
	requireOutcome(t, taskservice.StepNoop, k.do(priorityAction(1)))
	require.Equal(t, int32(1), k.task().priority)
	require.Equal(t, before, k.allEvents())
}

func TestExecute_SetDueOn(t *testing.T) {
	k := newAgentKit(t)
	due := func(d string) taskservice.Action {
		a := taskservice.Action{Kind: execution.ToolSetDueOn}
		if d != "" {
			a.DueOn = day(d)
		}
		return a
	}
	requireOutcome(t, taskservice.StepApplied, k.do(due("9999-12-31")))
	require.Equal(t, "9999-12-31", k.task().dueOn.Time.Format("2006-01-02"))
	requireOutcome(t, taskservice.StepNoop, k.do(due("9999-12-31")))

	// A null due date clears it.
	requireOutcome(t, taskservice.StepApplied, k.do(due("")))
	require.False(t, k.task().dueOn.Valid)
	requireOutcome(t, taskservice.StepNoop, k.do(due("")))

	k.mustSQL(`UPDATE tasks SET started_on = '2026-05-10' WHERE id = ?`, k.f.taskID)
	out := k.do(due("2026-05-09"))
	requireOutcome(t, taskservice.StepInvalid, out)
	require.Equal(t, apierrors.ValidationBodyDueBeforeStart.Code, out.ErrorCode)
	requireOutcome(t, taskservice.StepApplied, k.do(due("2026-05-10")))
}

func TestExecute_Transition(t *testing.T) {
	k := newAgentKit(t)
	tr := func(name string) taskservice.Action {
		return taskservice.Action{Kind: execution.ToolTransitionTask, Transition: name}
	}
	for _, name := range []string{"reopen", "cancel", "teleport", "unblock", "submit"} {
		requireOutcome(t, taskservice.StepInvalid, k.do(tr(name)))
	}
	require.Equal(t, "open", k.task().state)

	k.mustSQL(`UPDATE tasks SET completion_policy = 'review_required' WHERE id = ?`, k.f.taskID)
	out := k.do(tr("complete"))
	requireOutcome(t, taskservice.StepInvalid, out)
	require.Equal(t, apierrors.WsTaskCompletionNeedsApproval.Code, out.ErrorCode)
	k.mustSQL(`UPDATE tasks SET completion_policy = 'manual' WHERE id = ?`, k.f.taskID)

	requireOutcome(t, taskservice.StepApplied, k.do(tr("start")))
	require.Equal(t, "waiting", k.task().state)
	_, _, actorAgent := k.lastEvent("task.transition.start")
	require.Equal(t, int64(k.f.agentID), int64(actorAgent.Int32))

	// Once done the task is no longer workable: every later step stops.
	requireOutcome(t, taskservice.StepApplied, k.do(tr("complete")))
	out = k.do(commentAction("after"))
	requireOutcome(t, taskservice.StepStopped, out)
	require.Equal(t, apierrors.WsTaskAgentTaskNotWorkable.Code, out.ErrorCode)
}

func TestExecute_AddComment(t *testing.T) {
	k := newAgentKit(t)
	for _, body := range []string{"", strings.Repeat("a", taskservice.MaxCommentRunes+1), "bad \xff utf-8"} {
		requireOutcome(t, taskservice.StepInvalid, k.do(commentAction(body)))
	}
	require.Zero(t, k.comments())

	emoji := "進捗 🚀 done"
	for _, body := range []string{"   ", strings.Repeat("あ", taskservice.MaxCommentRunes), emoji} {
		requireOutcome(t, taskservice.StepApplied, k.do(commentAction(body)))
	}
	require.Equal(t, 3, k.comments())
	var stored string
	require.NoError(t, k.db.QueryRowContext(k.ctx, `SELECT body FROM comments WHERE task_id = ? ORDER BY id DESC LIMIT 1`, k.f.taskID).Scan(&stored))
	require.Equal(t, emoji, stored)
	payload, _, actorAgent := k.lastEvent("task.comment.added")
	require.Equal(t, int64(k.f.agentID), int64(actorAgent.Int32))
	require.Equal(t, taskservice.AuthorKindAgent, payload["authorKind"])
	require.Equal(t, k.f.agentPublicID.String(), payload["authorAgentId"])
}

func TestExecute_CreateSubtask(t *testing.T) {
	k := newAgentKit(t)
	sub := func(title string) taskservice.Action {
		return taskservice.Action{Kind: execution.ToolCreateSubtask, Title: title, ProjectID: k.f.projectID}
	}
	children := func() int {
		return k.count(`SELECT COUNT(*) FROM tasks WHERE parent_task_id = ?`, k.f.taskID)
	}
	for _, title := range []string{"   ", strings.Repeat("x", taskservice.MaxSubtaskTitleRunes+1), "bad \xff"} {
		requireOutcome(t, taskservice.StepInvalid, k.do(sub(title)))
	}
	require.Zero(t, children())
	for _, title := range []string{"x", strings.Repeat("題", taskservice.MaxSubtaskTitleRunes)} {
		requireOutcome(t, taskservice.StepApplied, k.do(sub(title)))
	}
	require.Equal(t, 2, children())
	payload, _, actorAgent := k.lastEvent("task.created")
	require.Equal(t, k.f.taskPublicID.String(), payload["parentTaskId"])
	require.Equal(t, int64(k.f.agentID), int64(actorAgent.Int32))
	var createdBy any
	var visibility string
	require.NoError(t, k.db.QueryRowContext(k.ctx, `SELECT created_by_user_id, visibility FROM tasks WHERE parent_task_id = ? ORDER BY id DESC LIMIT 1`,
		k.f.taskID).Scan(&createdBy, &visibility))
	require.Nil(t, createdBy)
	require.Equal(t, "public", visibility)

	_, err := k.svc.Execute(k.ctx, k.run(k.f.userID, k.f.userID), k.f.taskPublicID, taskservice.Action{Kind: execution.ToolCreateSubtask, Title: "x"}, newStep())
	require.ErrorIs(t, err, taskservice.ErrSubtaskProject)
}

func TestExecute_CreateSubtaskOnSubtask(t *testing.T) {
	k := newAgentKit(t)
	// Make the fixture task itself a child: depth is not limited.
	parent := k.insertID(`INSERT INTO tasks (public_id, workspace_id, project_id, created_by_user_id, task_number, title, visibility)
VALUES (?, ?, ?, ?, 900, 'Parent', 'public')`, types.New(), k.f.workspaceID, k.f.projectID, k.f.userID)
	k.mustSQL(`UPDATE tasks SET parent_task_id = ? WHERE id = ?`, parent, k.f.taskID)
	requireOutcome(t, taskservice.StepApplied, k.do(taskservice.Action{Kind: execution.ToolCreateSubtask, Title: "Grandchild", ProjectID: k.f.projectID}))
}

func TestExecute_SubtaskCap(t *testing.T) {
	k := newAgentKit(t)
	for i := range taskservice.MaxAgentChildren {
		k.mustSQL(`INSERT INTO tasks (public_id, workspace_id, project_id, parent_task_id, task_number, title, visibility)
VALUES (?, ?, ?, ?, ?, 'Seeded child', 'public')`, types.New(), k.f.workspaceID, k.f.projectID, k.f.taskID, 100+i)
	}
	out := k.do(taskservice.Action{Kind: execution.ToolCreateSubtask, Title: "One more", ProjectID: k.f.projectID})
	requireOutcome(t, taskservice.StepNotPermitted, out)
	require.Equal(t, taskservice.NotPermittedSubtaskCap, out.ErrorCode)
}

func TestExecute_UnknownAndUngrantedTool(t *testing.T) {
	k := newAgentKit(t)
	requireOutcome(t, taskservice.StepInvalid, k.do(taskservice.Action{Kind: "delete_task"}))
	k.grant(map[string]string{execution.ToolAddComment: "auto"})
	requireOutcome(t, taskservice.StepNotPermitted, k.do(priorityAction(3)))
	require.Equal(t, int32(1), k.task().priority)
}

func TestExecute_Stopped(t *testing.T) {
	cases := map[string]string{
		"archived":   `UPDATE tasks SET archived_at = NOW() WHERE id = ?`,
		"private":    `UPDATE tasks SET visibility = 'private' WHERE id = ?`,
		"paused":     `UPDATE ai_agents SET paused = TRUE WHERE id = (SELECT agent_id FROM task_actors WHERE task_id = ? AND kind = 'agent' LIMIT 1)`,
		"unassigned": `UPDATE task_actors SET enabled = FALSE WHERE task_id = ? AND kind = 'agent'`,
	}
	for name, stmt := range cases {
		t.Run(name, func(t *testing.T) {
			k := newAgentKit(t)
			k.mustSQL(stmt, k.f.taskID)
			before := k.allEvents()
			requireOutcome(t, taskservice.StepStopped, k.do(priorityAction(3)))
			require.Equal(t, int32(1), k.task().priority)
			require.Equal(t, before, k.allEvents())
		})
	}
}

func TestExecute_ProposalLimits(t *testing.T) {
	k := newAgentKit(t)
	k.grantAll("approval")

	first := k.do(commentAction("same"))
	requireOutcome(t, taskservice.StepProposed, first)
	require.NotEqual(t, types.PublicID{}, first.ProposalID)
	dup := k.do(commentAction("same"))
	requireOutcome(t, taskservice.StepSkipped, dup)
	require.Equal(t, taskservice.SkipDuplicate, dup.ErrorCode)

	for i := 1; i < taskservice.MaxPendingProposals; i++ {
		requireOutcome(t, taskservice.StepProposed, k.do(commentAction(strings.Repeat("n", i))))
	}
	capped := k.do(commentAction("one too many"))
	requireOutcome(t, taskservice.StepSkipped, capped)
	require.Equal(t, taskservice.SkipPendingCap, capped.ErrorCode)
	require.Equal(t, taskservice.MaxPendingProposals, k.proposals("pending"))
	require.Zero(t, k.comments())
	require.Equal(t, taskservice.MaxPendingProposals, k.events("agent.action.proposed"))
}

func TestExecute_ProposalRecord(t *testing.T) {
	k := newAgentKit(t)
	k.grantAll("approval")
	out := k.do(priorityAction(3))
	requireOutcome(t, taskservice.StepProposed, out)
	require.Equal(t, int32(1), k.task().priority)

	var args, pre []byte
	var expires time.Time
	require.NoError(t, k.db.QueryRowContext(k.ctx, `SELECT args_json, precondition_json, expires_at FROM agent_action_proposals WHERE public_id = ?`,
		out.ProposalID).Scan(&args, &pre, &expires))
	require.JSONEq(t, `{"priority":3}`, string(args))
	require.JSONEq(t, `{"priority":1}`, string(pre))
	require.WithinDuration(t, time.Now().Add(taskservice.ProposalLifetime), expires, time.Minute)

	payload, actorUser, actorAgent := k.lastEvent("agent.action.proposed")
	require.False(t, actorUser.Valid)
	require.Equal(t, int64(k.f.agentID), int64(actorAgent.Int32))
	require.Equal(t, out.ProposalID.String(), payload["proposalId"])
	require.Equal(t, execution.ToolSetPriority, payload["kind"])
	require.Equal(t, k.f.agentPublicID.String(), payload["agentId"])
	require.Equal(t, k.f.userPublicID.String(), payload["targetUserPublicId"])
	require.NotEmpty(t, payload["runKey"])

	// Without a delegator the notification target is left to the fan-out.
	k.delegate(0)
	requireOutcome(t, taskservice.StepProposed, k.doAs(k.run(0, k.f.userID), commentAction("no delegator")))
	payload, _, _ = k.lastEvent("agent.action.proposed")
	require.NotContains(t, payload, "targetUserPublicId")

	// A transition stores the state and the latest transition event id.
	tr := k.do(taskservice.Action{Kind: execution.ToolTransitionTask, Transition: "start"})
	requireOutcome(t, taskservice.StepProposed, tr)
	require.NoError(t, k.db.QueryRowContext(k.ctx, `SELECT precondition_json FROM agent_action_proposals WHERE public_id = ?`, tr.ProposalID).Scan(&pre))
	require.JSONEq(t, `{"derivedState":"open","transitionEventId":0}`, string(pre))
}

// seedBrokenProjection links the task to a due projection event whose zone
// itemkit cannot load, so a due-date write is refused inside the kit.
func seedBrokenProjection(k *agentKit) {
	k.t.Helper()
	cal := k.insertID(`INSERT INTO calendars (public_id, workspace_id, kind, name) VALUES (?, ?, 'personal', 'Projection')`,
		types.New(), k.f.workspaceID)
	k.mustSQL(`INSERT INTO calendar_members (public_id, workspace_id, calendar_id, user_id, role) VALUES (?, ?, ?, ?, 'owner')`,
		types.New(), k.f.workspaceID, cal, k.f.userID)
	start := time.Now().UTC().Add(24 * time.Hour).Truncate(time.Second)
	var eventID uint32
	require.NoError(k.t, dbretry.InTx(k.ctx, k.db, "test.seed_projection", nil, func(ctx context.Context, tx *dbretry.Tx) error {
		_, id, err := itemkit.ScheduleTask(ctx, tx, itemkit.ScheduleTaskArgs{
			WorkspaceID: k.f.workspaceID, TaskID: k.f.taskID, CalendarID: cal, ActorUserID: k.f.userID,
			Role: itemkit.RoleDue, StartAt: start, EndAt: start.Add(time.Hour), Timezone: "UTC",
		})
		eventID = id
		return err
	}))
	k.mustSQL(`UPDATE calendar_events SET timezone = 'Mars/Olympus' WHERE id = ?`, eventID)
}

func TestExecute_ApplyFailureRollsBack(t *testing.T) {
	k := newAgentKit(t)
	seedBrokenProjection(k)
	before, events := k.task(), k.events("task.updated")
	out := k.do(taskservice.Action{Kind: execution.ToolSetDueOn, DueOn: day("2030-01-02")})
	requireOutcome(t, taskservice.StepFailed, out)
	require.Equal(t, apierrors.ItemItemkitInvariantViolation.Code, out.ErrorCode)
	require.Equal(t, before, k.task())
	require.Equal(t, events, k.events("task.updated"))
}

func TestExecute_StepRef(t *testing.T) {
	k := newAgentKit(t)
	for _, step := range []taskservice.StepRef{{RunKey: "", Step: 1}, {RunKey: strings.Repeat("r", 37), Step: 1}, {RunKey: "r", Step: 65536}} {
		_, err := k.svc.Execute(k.ctx, k.run(k.f.userID, k.f.userID), k.f.taskPublicID, priorityAction(2), step)
		require.ErrorIs(t, err, taskservice.ErrStepRef)
	}
}
