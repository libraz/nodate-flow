package e2e

import (
	"bytes"
	"context"
	"log/slog"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/libraz/nodate-flow/apps/flow-api/internal/ai/autoactions"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/generated"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/types"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/mutationlog"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/taskworkflow"
	"github.com/libraz/nodate-flow/apps/flow-api/tests/helpers"
)

// TestSubmissionRejectsSubmitterAsReviewer keeps the review gate from being
// satisfied by the person who did the work.
func TestSubmissionRejectsSubmitterAsReviewer(t *testing.T) {
	bootstrap(t)
	t.Parallel()

	owner := newTenant(t)
	taskID := seedPublicTask(t, owner, "Self review")
	configureReviewSubmissionTask(t, owner, taskID, owner.UserPublicID)

	status, raw := doJSONStatus(t, http.MethodPost, testServerURL+"/tasks/"+taskID+"/submissions", owner.AccessToken,
		map[string]any{
			"reviewerUserId":     owner.UserPublicID,
			"acceptanceCriteria": []string{"Reviewed by someone else"},
		})
	requireDenied(t, status, raw, http.StatusUnprocessableEntity, "VALIDATION.BODY.FIELD_INVALID",
		"the submitter selecting themselves as reviewer")
	require.Zero(t, workflowSubmissionEventCount(t, owner.WorkspacePublicID, taskID))
}

// TestSubmissionRejectsUnknownAttachmentAsBodyField answers an attachment id
// that does not belong to the task as a body-field problem, not as a missing
// task.
func TestSubmissionRejectsUnknownAttachmentAsBodyField(t *testing.T) {
	bootstrap(t)
	t.Parallel()

	owner := newTenant(t)
	reviewer := seedProjectRoleMember(t, owner, "viewer")
	taskID := seedPublicTask(t, owner, "Foreign attachment")
	configureReviewSubmissionTask(t, owner, taskID, reviewer.UserPublicID)

	status, raw := doJSONStatus(t, http.MethodPost, testServerURL+"/tasks/"+taskID+"/submissions", owner.AccessToken,
		map[string]any{
			"reviewerUserId":     reviewer.UserPublicID,
			"acceptanceCriteria": []string{"Evidence attached"},
			"attachmentIds":      []string{types.New().String()},
		})
	requireDenied(t, status, raw, http.StatusUnprocessableEntity, "VALIDATION.BODY.FIELD_INVALID",
		"an attachment id unknown to the task")
}

// TestCompletionPolicyLoweringRequiresElevatedRole lets an editor raise the
// bar but holds removing the review gate to a project lead or workspace admin.
func TestCompletionPolicyLoweringRequiresElevatedRole(t *testing.T) {
	bootstrap(t)
	t.Parallel()

	owner := newTenant(t)
	editor := seedProjectRoleMember(t, owner, "editor")
	lead := seedProjectRoleMember(t, owner, "lead")
	admin := seedElevatedMember(t, owner)
	taskID := seedPublicTask(t, owner, "Policy authority")
	policyURL := testServerURL + "/tasks/" + taskID + "/completion-policy"

	doJSON(t, http.MethodPost, policyURL, editor.AccessToken, map[string]any{"policy": "manual"}, nil)
	doJSON(t, http.MethodPost, policyURL, editor.AccessToken, map[string]any{"policy": "review_required"}, nil)

	status, raw := doJSONStatus(t, http.MethodPost, policyURL, editor.AccessToken, map[string]any{"policy": "manual"})
	requireDenied(t, status, raw, http.StatusForbidden, "WS.PROJECT.ACCESS_DENIED", "an editor lowering review_required")
	require.Equal(t, "review_required", workflowCompletionPolicy(t, owner, taskID))

	doJSON(t, http.MethodPost, policyURL, lead.AccessToken, map[string]any{"policy": "manual"}, nil)
	require.Equal(t, "manual", workflowCompletionPolicy(t, owner, taskID))

	doJSON(t, http.MethodPost, policyURL, editor.AccessToken, map[string]any{"policy": "review_required"}, nil)
	doJSON(t, http.MethodPost, policyURL, admin.AccessToken, map[string]any{"policy": "manual"}, nil)
	require.Equal(t, "manual", workflowCompletionPolicy(t, owner, taskID))
}

// TestRequiredConstraintGatesApprovedCompletion covers the policy's selected
// constraints: an approved submission is not enough while a required
// constraint is unsatisfied.
func TestRequiredConstraintGatesApprovedCompletion(t *testing.T) {
	bootstrap(t)
	t.Parallel()

	owner := newTenant(t)
	reviewer := seedProjectRoleMember(t, owner, "viewer")
	approver := seedProjectRoleMember(t, owner, "viewer")
	taskID := seedPublicTask(t, owner, "Required constraint")
	base := testServerURL + "/tasks/" + taskID

	var constraint struct {
		ID string `json:"id"`
	}
	doJSON(t, http.MethodPost, base+"/constraints", owner.AccessToken,
		map[string]any{
			"kind":       "approval",
			"expression": `{"op":"actor.has_role","arg":"approver"}`,
		}, &constraint)
	require.NotEmpty(t, constraint.ID)
	doJSON(t, http.MethodPost, base+"/actors", owner.AccessToken,
		map[string]any{"userId": reviewer.UserPublicID, "role": "reviewer"}, nil)
	doJSON(t, http.MethodPost, base+"/completion-policy", owner.AccessToken,
		map[string]any{"policy": "review_required", "requiredConstraintIds": []string{constraint.ID}}, nil)

	var submission struct {
		ID string `json:"id"`
	}
	doJSON(t, http.MethodPost, base+"/submissions", owner.AccessToken,
		map[string]any{
			"reviewerUserId":     reviewer.UserPublicID,
			"acceptanceCriteria": []string{"Approved and constrained"},
		}, &submission)
	doJSON(t, http.MethodPost, base+"/submissions/"+submission.ID+"/decision", reviewer.AccessToken,
		map[string]any{"decision": "approve"}, nil)

	status, raw := doJSONStatus(t, http.MethodPost, base+"/transitions", owner.AccessToken,
		map[string]any{"transition": "complete"})
	requireDenied(t, status, raw, http.StatusUnprocessableEntity, "WS.TASK.COMPLETION_NEEDS_APPROVAL",
		"an unsatisfied required constraint")

	doJSON(t, http.MethodPost, base+"/actors", owner.AccessToken,
		map[string]any{"userId": approver.UserPublicID, "role": "approver"}, nil)
	requireActorConstraintVerdict(t, constraint.ID, true)
	var completed struct {
		DerivedState string `json:"derivedState"`
	}
	doJSON(t, http.MethodPost, base+"/transitions", owner.AccessToken,
		map[string]any{"transition": "complete"}, &completed)
	require.Equal(t, "done", completed.DerivedState)
}

// TestMCPTransitionCompleteHonoursReviewGate keeps the agent path on the same
// completion gate as REST.
func TestMCPTransitionCompleteHonoursReviewGate(t *testing.T) {
	bootstrap(t)
	t.Parallel()

	owner := newTenant(t)
	reviewer := seedProjectRoleMember(t, owner, "viewer")
	taskID := seedPublicTask(t, owner, "MCP review gate")
	configureReviewSubmissionTask(t, owner, taskID, reviewer.UserPublicID)

	code := mcpToolErrorCode(t, owner, "transition_task", map[string]any{
		"taskId":     taskID,
		"transition": "complete",
	})
	require.Equal(t, "WS.TASK.COMPLETION_NEEDS_APPROVAL", code)
	require.Equal(t, "open", workflowTaskDerivedState(t, owner.WorkspacePublicID, taskID))
}

// TestHandoffRequesterCanCancel lets the requester or an elevated role
// withdraw a pending handoff, and frees the role for a new request.
func TestHandoffRequesterCanCancel(t *testing.T) {
	bootstrap(t)
	t.Parallel()

	owner := newTenant(t)
	editor := seedProjectRoleMember(t, owner, "editor")
	recipient := seedProjectRoleMember(t, owner, "viewer")
	outsider := seedProjectRoleMember(t, owner, "viewer")
	admin := seedElevatedMember(t, owner)
	taskID := seedPublicTask(t, owner, "Cancelled handoff")
	base := testServerURL + "/tasks/" + taskID

	doJSON(t, http.MethodPost, base+"/actors", owner.AccessToken,
		map[string]any{"userId": editor.UserPublicID, "role": "reviewer"}, nil)
	first := requestHandoff(t, editor, taskID, "reviewer", recipient.UserPublicID)

	for _, caller := range []*helpers.TestTenant{outsider, recipient} {
		status, raw := doJSONStatus(t, http.MethodPost, base+"/handoffs/"+first+"/decision", caller.AccessToken,
			map[string]any{"decision": "cancel"})
		requireDenied(t, status, raw, http.StatusForbidden, "WS.TASK.ACCESS_DENIED",
			"a caller who neither requested nor holds an elevated role")
	}

	var cancelled struct {
		Status string `json:"status"`
	}
	doJSON(t, http.MethodPost, base+"/handoffs/"+first+"/decision", editor.AccessToken,
		map[string]any{"decision": "cancel"}, &cancelled)
	require.Equal(t, "cancelled", cancelled.Status)
	require.Equal(t, "cancelled", workflowHandoffStatus(t, owner, taskID, first))
	require.Equal(t, 1, handoffDecisionEventCount(t, owner, taskID, first, "cancel"))

	status, raw := doJSONStatus(t, http.MethodPost, base+"/handoffs/"+first+"/decision", editor.AccessToken,
		map[string]any{"decision": "cancel"})
	requireDenied(t, status, raw, http.StatusConflict, "WS.TASK.UPDATE_CONFLICT", "cancelling a resolved handoff")

	second := requestHandoff(t, editor, taskID, "reviewer", recipient.UserPublicID)
	doJSON(t, http.MethodPost, base+"/handoffs/"+second+"/decision", admin.AccessToken,
		map[string]any{"decision": "cancel"}, nil)
	require.Equal(t, "cancelled", workflowHandoffStatus(t, owner, taskID, second))
	require.NotEmpty(t, requestHandoff(t, editor, taskID, "reviewer", recipient.UserPublicID))
}

// TestRemovingHandoffSourceCancelsPendingHandoff resolves a pending handoff
// whose source no longer holds the role, so the role can be handed off again.
func TestRemovingHandoffSourceCancelsPendingHandoff(t *testing.T) {
	bootstrap(t)
	t.Parallel()

	owner := newTenant(t)
	recipient := seedProjectRoleMember(t, owner, "viewer")
	successor := seedProjectRoleMember(t, owner, "editor")
	taskID := seedPublicTask(t, owner, "Source removed")
	base := testServerURL + "/tasks/" + taskID

	handoffID := requestHandoff(t, owner, taskID, "assignee", recipient.UserPublicID)
	doJSON(t, http.MethodDelete, base+"/actors/"+taskActorID(t, owner, taskID, owner.UserPublicID, "assignee"),
		owner.AccessToken, nil, nil)

	require.Equal(t, "cancelled", workflowHandoffStatus(t, owner, taskID, handoffID))
	require.Equal(t, 1, handoffDecisionEventCount(t, owner, taskID, handoffID, "cancel"))

	doJSON(t, http.MethodPost, base+"/actors", owner.AccessToken,
		map[string]any{"userId": successor.UserPublicID, "role": "assignee"}, nil)
	require.NotEmpty(t, requestHandoff(t, owner, taskID, "assignee", recipient.UserPublicID))
}

// TestAcceptingOverdueHandoffExpiresIt refuses an acceptance at or after the
// deadline and records the same expiry the background worker would.
func TestAcceptingOverdueHandoffExpiresIt(t *testing.T) {
	bootstrap(t)
	t.Parallel()

	owner := newTenant(t)
	recipient := seedProjectRoleMember(t, owner, "viewer")
	taskID := seedPublicTask(t, owner, "Overdue acceptance")
	base := testServerURL + "/tasks/" + taskID

	var handoff struct {
		ID string `json:"id"`
	}
	doJSON(t, http.MethodPost, base+"/handoffs", owner.AccessToken,
		map[string]any{
			"role":     "assignee",
			"toUserId": recipient.UserPublicID,
			"dueAt":    time.Now().UTC().Add(-time.Minute).Unix(),
		}, &handoff)

	status, raw := doJSONStatus(t, http.MethodPost, base+"/handoffs/"+handoff.ID+"/decision", recipient.AccessToken,
		map[string]any{"decision": "accept"})
	requireDenied(t, status, raw, http.StatusUnprocessableEntity, "WS.TASK.TRANSITION_REJECTED", "accepting after the deadline")
	require.Equal(t, "expired", workflowHandoffStatus(t, owner, taskID, handoff.ID))
	require.Equal(t, 1, handoffDecisionEventCount(t, owner, taskID, handoff.ID, "expired"))
	require.Equal(t, owner.UserPublicID, taskActorUserForRole(t, owner, taskID, "assignee"))

	status, raw = doJSONStatus(t, http.MethodPost, base+"/handoffs/"+handoff.ID+"/decision", recipient.AccessToken,
		map[string]any{"decision": "accept"})
	requireDenied(t, status, raw, http.StatusConflict, "WS.TASK.UPDATE_CONFLICT", "accepting an expired handoff")
}

// TestReviewDecisionWaitsForConcurrentReviewerRemoval makes the reviewer
// check a current read: a removal still in flight must be seen once it
// commits, not bypassed through the decision's snapshot.
func TestReviewDecisionWaitsForConcurrentReviewerRemoval(t *testing.T) {
	bootstrap(t)
	t.Parallel()

	owner := newTenant(t)
	reviewer := seedProjectRoleMember(t, owner, "viewer")
	taskID := seedPublicTask(t, owner, "Reviewer removed mid-request")
	base := testServerURL + "/tasks/" + taskID
	configureReviewSubmissionTask(t, owner, taskID, reviewer.UserPublicID)
	var submission struct {
		ID string `json:"id"`
	}
	doJSON(t, http.MethodPost, base+"/submissions", owner.AccessToken,
		map[string]any{
			"reviewerUserId":     reviewer.UserPublicID,
			"acceptanceCriteria": []string{"Decided by a current reviewer"},
		}, &submission)
	reviewerActorID := taskActorID(t, owner, taskID, reviewer.UserPublicID, "reviewer")
	var snapshot struct {
		Capabilities struct {
			CanDecideCurrentSubmission bool `json:"canDecideCurrentSubmission"`
		} `json:"capabilities"`
	}
	doJSON(t, http.MethodGet, base+"/workflow", reviewer.AccessToken, nil, &snapshot)
	require.True(t, snapshot.Capabilities.CanDecideCurrentSubmission,
		"the read-only workflow snapshot must still run the current reviewer check")

	ctx := context.Background()
	removal, err := testDB.BeginTx(ctx, nil)
	require.NoError(t, err)
	defer func() { _ = removal.Rollback() }()
	_, err = removal.ExecContext(ctx,
		`UPDATE task_actors SET enabled = FALSE WHERE public_id = UUID_TO_BIN(?, 0)`, reviewerActorID)
	require.NoError(t, err)

	type response struct {
		status int
		raw    []byte
		err    error
	}
	done := make(chan response, 1)
	go func() {
		status, raw, err := sendJSONStatus(http.MethodPost, base+"/submissions/"+submission.ID+"/decision",
			reviewer.AccessToken, map[string]any{"decision": "approve"})
		done <- response{status: status, raw: raw, err: err}
	}()
	select {
	case got := <-done:
		require.FailNowf(t, "decision did not wait for the uncommitted removal",
			"status=%d body=%s err=%v", got.status, string(got.raw), got.err)
	case <-time.After(500 * time.Millisecond):
	}
	require.NoError(t, removal.Commit())

	got := <-done
	require.NoError(t, got.err)
	requireDenied(t, got.status, got.raw, http.StatusForbidden, "WS.TASK.ACCESS_DENIED", "a reviewer removed mid-request")
}

// TestRemoveActorTakesTaskLock orders actor removal like every other task
// mutation: task row first. A transaction holding the task row can then lock
// the actor row without deadlocking against the waiting removal.
func TestRemoveActorTakesTaskLock(t *testing.T) {
	bootstrap(t)
	t.Parallel()

	owner := newTenant(t)
	reviewer := seedProjectRoleMember(t, owner, "viewer")
	taskID := seedPublicTask(t, owner, "Actor removal lock")
	base := testServerURL + "/tasks/" + taskID
	doJSON(t, http.MethodPost, base+"/actors", owner.AccessToken,
		map[string]any{"userId": reviewer.UserPublicID, "role": "reviewer"}, nil)
	actorID := taskActorID(t, owner, taskID, reviewer.UserPublicID, "reviewer")
	_, taskInternalID := helpers.AgentTaskInternalIDs(t, testDB, owner.WorkspacePublicID, taskID)

	ctx := context.Background()
	holder, err := testDB.BeginTx(ctx, nil)
	require.NoError(t, err)
	defer func() { _ = holder.Rollback() }()
	var lockedID uint32
	require.NoError(t, holder.QueryRowContext(ctx, `SELECT id FROM tasks WHERE id = ? FOR UPDATE`, taskInternalID).Scan(&lockedID))

	done := make(chan error, 1)
	go func() {
		status, raw, err := sendJSONStatus(http.MethodDelete, base+"/actors/"+actorID, owner.AccessToken, nil)
		if err == nil && status != http.StatusOK {
			err = &unexpectedStatusError{status: status, body: string(raw)}
		}
		done <- err
	}()
	time.Sleep(500 * time.Millisecond)
	var enabled bool
	require.NoError(t, holder.QueryRowContext(ctx,
		`SELECT enabled FROM task_actors WHERE public_id = UUID_TO_BIN(?, 0) FOR UPDATE`, actorID).Scan(&enabled),
		"the task-lock holder must reach the actor row; the removal waits on the task row first")
	require.True(t, enabled)
	require.NoError(t, holder.Commit())
	require.NoError(t, <-done)
	require.False(t, taskActorEnabled(t, actorID))
}

// TestHandoffExpiryLocksOnlyHandoffRows keeps the expiry worker's lock off the
// joined users rows, which other products update: a held users row must
// neither be skipped nor blank the participants on the expiry event.
func TestHandoffExpiryLocksOnlyHandoffRows(t *testing.T) {
	bootstrap(t)

	owner := newTenant(t)
	recipient := seedProjectRoleMember(t, owner, "viewer")
	taskID := seedPublicTask(t, owner, "Expiry lock scope")
	var handoff struct {
		ID string `json:"id"`
	}
	doJSON(t, http.MethodPost, testServerURL+"/tasks/"+taskID+"/handoffs", owner.AccessToken,
		map[string]any{
			"role":     "assignee",
			"toUserId": recipient.UserPublicID,
			"dueAt":    time.Now().UTC().Add(-time.Minute).Unix(),
		}, &handoff)

	ctx := context.Background()
	userLock, err := testDB.BeginTx(ctx, nil)
	require.NoError(t, err)
	defer func() { _ = userLock.Rollback() }()
	rows, err := userLock.QueryContext(ctx,
		`SELECT id FROM users WHERE public_id IN (UUID_TO_BIN(?, 0), UUID_TO_BIN(?, 0)) FOR UPDATE`,
		owner.UserPublicID, recipient.UserPublicID)
	require.NoError(t, err)
	require.NoError(t, rows.Close())

	queries := generated.New(testDB)
	runner := taskworkflow.NewHandoffExpiryRunner(testDB, queries, mutationlog.New(testDB, queries))
	_, err = runner.RunOnce(ctx, time.Now().UTC(), 100)
	require.NoError(t, err)
	require.NoError(t, userLock.Rollback())
	require.Equal(t, "expired", workflowHandoffStatus(t, owner, taskID, handoff.ID))

	workspaceID, taskInternalID := helpers.AgentTaskInternalIDs(t, testDB, owner.WorkspacePublicID, taskID)
	var fromUser, toUser string
	require.NoError(t, testDB.QueryRowContext(ctx, `
		SELECT JSON_UNQUOTE(JSON_EXTRACT(payload_json, '$.fromUserPublicId')),
		       JSON_UNQUOTE(JSON_EXTRACT(payload_json, '$.toUserPublicId'))
		FROM events
		WHERE workspace_id = ? AND task_id = ? AND type = 'task.handoff.decided'
		  AND JSON_UNQUOTE(JSON_EXTRACT(payload_json, '$.handoffId')) = ?
	`, workspaceID, taskInternalID, handoff.ID).Scan(&fromUser, &toUser))
	require.Equal(t, owner.UserPublicID, fromUser)
	require.Equal(t, recipient.UserPublicID, toUser)
}

// TestStaleReviewActionSkipsReviewRequiredTasks keeps the auto-action from
// attempting a completion the review gate will refuse on every pass.
func TestStaleReviewActionSkipsReviewRequiredTasks(t *testing.T) {
	bootstrap(t)
	t.Parallel()
	lockAutoActionPass(t)

	tenant := newTenant(t)
	t.Cleanup(func() { helpers.PurgeWorkspace(t, testDB, tenant.WorkspacePublicID) })
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	wsID, userID := lookupWorkspaceAndOwner(ctx, t, tenant.WorkspacePublicID)
	setWorkspaceAutoActionThreshold(ctx, t, wsID, "0.50")
	taskID := seedTask(ctx, t, wsID, userID, "stale review-required task", "")
	_, err := testDB.ExecContext(ctx, `UPDATE tasks SET completion_policy = 'review_required' WHERE id = ?`, taskID)
	require.NoError(t, err)
	setTaskStateAndUpdatedAt(ctx, t, taskID, "review", time.Now().Add(-10*24*time.Hour))
	var taskPublicID string
	require.NoError(t, testDB.QueryRowContext(ctx,
		`SELECT BIN_TO_UUID(public_id, 0) FROM tasks WHERE id = ?`, taskID).Scan(&taskPublicID))

	var logs bytes.Buffer
	exec := &autoactions.Executor{
		DB: testDB,
		Config: autoactions.ExecutorConfig{
			Interval:            time.Nanosecond,
			ConfidenceThreshold: 0.5,
		},
		Logger: slog.New(slog.NewTextHandler(&logs, &slog.HandlerOptions{Level: slog.LevelDebug})),
	}
	requireCompletePass(ctx, t, exec)

	require.Equal(t, "review", readDerivedState(ctx, t, taskID))
	for _, line := range strings.Split(logs.String(), "\n") {
		if strings.Contains(line, taskPublicID) {
			require.NotContains(t, line, "transition rejected",
				"the stale-review action must not attempt completion on a review-required task")
		}
	}
}

type unexpectedStatusError struct {
	status int
	body   string
}

func (e *unexpectedStatusError) Error() string {
	return http.StatusText(e.status) + ": " + e.body
}

func requestHandoff(t *testing.T, requester *helpers.TestTenant, taskID, role, toUserID string) string {
	t.Helper()
	var handoff struct {
		ID string `json:"id"`
	}
	doJSON(t, http.MethodPost, testServerURL+"/tasks/"+taskID+"/handoffs", requester.AccessToken,
		map[string]any{"role": role, "toUserId": toUserID}, &handoff)
	require.NotEmpty(t, handoff.ID)
	return handoff.ID
}

func workflowCompletionPolicy(t *testing.T, reader *helpers.TestTenant, taskID string) string {
	t.Helper()
	var workflow struct {
		CompletionPolicy string `json:"completionPolicy"`
	}
	doJSON(t, http.MethodGet, testServerURL+"/tasks/"+taskID+"/workflow", reader.AccessToken, nil, &workflow)
	return workflow.CompletionPolicy
}

func workflowHandoffStatus(t *testing.T, reader *helpers.TestTenant, taskID, handoffID string) string {
	t.Helper()
	var workflow struct {
		Handoffs []struct {
			ID     string `json:"id"`
			Status string `json:"status"`
		} `json:"handoffs"`
	}
	doJSON(t, http.MethodGet, testServerURL+"/tasks/"+taskID+"/workflow", reader.AccessToken, nil, &workflow)
	for _, item := range workflow.Handoffs {
		if item.ID == handoffID {
			return item.Status
		}
	}
	require.FailNowf(t, "handoff missing from workflow snapshot", "handoff=%s", handoffID)
	return ""
}

func handoffDecisionEventCount(t *testing.T, owner *helpers.TestTenant, taskID, handoffID, decision string) int {
	t.Helper()
	workspaceID, taskInternalID := helpers.AgentTaskInternalIDs(t, testDB, owner.WorkspacePublicID, taskID)
	var count int
	require.NoError(t, testDB.QueryRowContext(context.Background(), `
		SELECT COUNT(*) FROM events
		WHERE workspace_id = ? AND task_id = ? AND type = 'task.handoff.decided'
		  AND JSON_UNQUOTE(JSON_EXTRACT(payload_json, '$.handoffId')) = ?
		  AND JSON_UNQUOTE(JSON_EXTRACT(payload_json, '$.decision')) = ?
	`, workspaceID, taskInternalID, handoffID, decision).Scan(&count))
	return count
}

func taskActorID(t *testing.T, reader *helpers.TestTenant, taskID, userID, role string) string {
	t.Helper()
	var actors struct {
		Actors []struct {
			ID     string `json:"id"`
			UserID string `json:"userId"`
			Role   string `json:"role"`
		} `json:"actors"`
	}
	doJSON(t, http.MethodGet, testServerURL+"/tasks/"+taskID+"/actors", reader.AccessToken, nil, &actors)
	for _, actor := range actors.Actors {
		if actor.UserID == userID && actor.Role == role {
			return actor.ID
		}
	}
	require.FailNowf(t, "task actor not found", "user=%s role=%s", userID, role)
	return ""
}

func taskActorUserForRole(t *testing.T, reader *helpers.TestTenant, taskID, role string) string {
	t.Helper()
	var actors struct {
		Actors []struct {
			UserID string `json:"userId"`
			Role   string `json:"role"`
		} `json:"actors"`
	}
	doJSON(t, http.MethodGet, testServerURL+"/tasks/"+taskID+"/actors", reader.AccessToken, nil, &actors)
	var users []string
	for _, actor := range actors.Actors {
		if actor.Role == role {
			users = append(users, actor.UserID)
		}
	}
	require.Len(t, users, 1, "expected exactly one %s", role)
	return users[0]
}

func taskActorEnabled(t *testing.T, actorPublicID string) bool {
	t.Helper()
	var enabled bool
	require.NoError(t, testDB.QueryRowContext(context.Background(),
		`SELECT enabled FROM task_actors WHERE public_id = UUID_TO_BIN(?, 0)`, actorPublicID).Scan(&enabled))
	return enabled
}
