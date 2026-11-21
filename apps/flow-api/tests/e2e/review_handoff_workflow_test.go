package e2e

import (
	"context"
	"database/sql"
	"fmt"
	"net/http"
	"testing"
	"time"

	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/generated"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/mutationlog"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/taskworkflow"
	"github.com/libraz/nodate-flow/apps/flow-api/tests/helpers"

	"github.com/stretchr/testify/require"
)

// TestReviewRequiredCompletionRequiresCurrentApprovedSubmission exercises the
// full human review path through HTTP. A rejection does not approve work; an
// approval applies only to the submitted revision; and manual tasks retain
// their existing lightweight completion path.
func TestReviewRequiredCompletionRequiresCurrentApprovedSubmission(t *testing.T) {
	bootstrap(t)
	t.Parallel()

	owner := newTenant(t)
	reviewer := seedProjectRoleMember(t, owner, "viewer")
	outsider := seedProjectRoleMember(t, owner, "viewer")
	taskID := seedPublicTask(t, owner, "Review-required work")
	base := testServerURL + "/tasks/" + taskID

	doJSON(t, http.MethodPost, base+"/actors", owner.AccessToken,
		map[string]any{"userId": reviewer.UserPublicID, "role": "reviewer"}, nil)
	doJSON(t, http.MethodPost, base+"/completion-policy", owner.AccessToken,
		map[string]any{"policy": "review_required"}, nil)

	// A task editor submits work; the currently selected reviewer can reject
	// or approve it even though the reviewer is only a project viewer.
	doJSON(t, http.MethodPost, base+"/transitions", owner.AccessToken,
		map[string]any{"transition": "start"}, nil)
	var rejectedSubmission struct {
		ID       string `json:"id"`
		Revision uint32 `json:"revision"`
	}
	doJSON(t, http.MethodPost, base+"/submissions", owner.AccessToken,
		map[string]any{
			"reviewerUserId":     reviewer.UserPublicID,
			"acceptanceCriteria": []string{"The result is reproducible"},
			"evidenceUrls":       []string{"https://example.test/evidence/first"},
		}, &rejectedSubmission)
	require.NotEmpty(t, rejectedSubmission.ID)
	require.EqualValues(t, 1, rejectedSubmission.Revision)

	status, raw := doJSONStatus(t, http.MethodPost,
		base+"/submissions/"+rejectedSubmission.ID+"/decision", outsider.AccessToken,
		map[string]any{"decision": "approve"})
	require.Equal(t, http.StatusForbidden, status,
		"a task reader who is not the selected reviewer must be denied, body=%s", string(raw))

	doJSON(t, http.MethodPost,
		base+"/submissions/"+rejectedSubmission.ID+"/decision", reviewer.AccessToken,
		map[string]any{"decision": "reject", "reason": "Please attach the reproducible output"}, nil)
	status, raw = doJSONStatus(t, http.MethodPost, base+"/transitions", owner.AccessToken,
		map[string]any{"transition": "complete"})
	requireDenied(t, status, raw, http.StatusUnprocessableEntity, "WS.TASK.COMPLETION_NEEDS_APPROVAL",
		"completing rejected work without a current approval")

	var approvedSubmission struct {
		ID       string `json:"id"`
		Revision uint32 `json:"revision"`
	}
	doJSON(t, http.MethodPost, base+"/submissions", owner.AccessToken,
		map[string]any{
			"reviewerUserId":     reviewer.UserPublicID,
			"acceptanceCriteria": []string{"The result is reproducible"},
			"evidenceUrls":       []string{"https://example.test/evidence/revised"},
		}, &approvedSubmission)
	require.NotEmpty(t, approvedSubmission.ID)
	require.EqualValues(t, rejectedSubmission.Revision, approvedSubmission.Revision,
		"a resubmission with no work edits remains on the same revision")
	doJSON(t, http.MethodPost,
		base+"/submissions/"+approvedSubmission.ID+"/decision", reviewer.AccessToken,
		map[string]any{"decision": "approve"}, nil)

	// Editing the artifact after approval invalidates that approval even if
	// the task remains in its review state.
	doJSON(t, http.MethodPatch, base, owner.AccessToken,
		map[string]any{"title": "Review-required work, revised"}, nil)
	status, raw = doJSONStatus(t, http.MethodPost, base+"/transitions", owner.AccessToken,
		map[string]any{"transition": "complete"})
	requireDenied(t, status, raw, http.StatusUnprocessableEntity, "WS.TASK.COMPLETION_NEEDS_APPROVAL",
		"completing an edited task with an approval for a prior revision")
	var staleWorkflow struct {
		WorkRevision      uint32 `json:"workRevision"`
		CurrentSubmission *struct {
			ID string `json:"id"`
		} `json:"currentSubmission"`
		RecentSubmissions []struct {
			ID        string   `json:"id"`
			Revision  uint32   `json:"revision"`
			CreatedAt int64    `json:"createdAt"`
			Evidence  []string `json:"evidenceUrls"`
			Decision  *struct {
				Decision string `json:"decision"`
				Reason   string `json:"reason"`
			} `json:"decision"`
		} `json:"recentSubmissions"`
	}
	doJSON(t, http.MethodGet, base+"/workflow", owner.AccessToken, nil, &staleWorkflow)
	require.Equal(t, approvedSubmission.Revision+1, staleWorkflow.WorkRevision)
	require.Nil(t, staleWorkflow.CurrentSubmission,
		"currentSubmission is reserved for the current work revision")
	require.Len(t, staleWorkflow.RecentSubmissions, 2,
		"editing the task must not erase the rejected and approved review history")
	var rejectedHistoryFound, approvedHistoryFound bool
	for _, item := range staleWorkflow.RecentSubmissions {
		switch item.ID {
		case rejectedSubmission.ID:
			rejectedHistoryFound = true
			require.Equal(t, rejectedSubmission.Revision, item.Revision)
			require.Positive(t, item.CreatedAt, "submission creation time should be Unix seconds")
			require.Equal(t, []string{"https://example.test/evidence/first"}, item.Evidence)
			require.NotNil(t, item.Decision)
			require.Equal(t, "rejected", item.Decision.Decision)
			require.Equal(t, "Please attach the reproducible output", item.Decision.Reason)
		case approvedSubmission.ID:
			approvedHistoryFound = true
			require.NotNil(t, item.Decision)
			require.Equal(t, "approved", item.Decision.Decision)
		}
	}
	require.True(t, rejectedHistoryFound, "rejected submission should remain in persisted history")
	require.True(t, approvedHistoryFound, "prior approval should remain in read-only history after artifact edits")

	var currentSubmission struct {
		ID       string `json:"id"`
		Revision uint32 `json:"revision"`
	}
	doJSON(t, http.MethodPost, base+"/submissions", owner.AccessToken,
		map[string]any{
			"reviewerUserId":     reviewer.UserPublicID,
			"acceptanceCriteria": []string{"The result is reproducible"},
			"evidenceUrls":       []string{"https://example.test/evidence/current"},
		}, &currentSubmission)
	require.Greater(t, currentSubmission.Revision, approvedSubmission.Revision,
		"editing task content must advance the submitted work revision")
	doJSON(t, http.MethodPost,
		base+"/submissions/"+currentSubmission.ID+"/decision", reviewer.AccessToken,
		map[string]any{"decision": "approve"}, nil)
	doJSON(t, http.MethodPost, base+"/transitions", owner.AccessToken,
		map[string]any{"transition": "complete"}, nil)
	var workflow struct {
		CompletionPolicy  string `json:"completionPolicy"`
		WorkRevision      uint32 `json:"workRevision"`
		CurrentSubmission *struct {
			ID              string `json:"id"`
			SubmitterUserID string `json:"submitterUserId"`
			ReviewerUserID  string `json:"reviewerUserId"`
			Decision        *struct {
				Decision string `json:"decision"`
			} `json:"decision"`
		} `json:"currentSubmission"`
	}
	doJSON(t, http.MethodGet, base+"/workflow", owner.AccessToken, nil, &workflow)
	require.Equal(t, "review_required", workflow.CompletionPolicy)
	require.Equal(t, currentSubmission.Revision, workflow.WorkRevision)
	require.NotNil(t, workflow.CurrentSubmission)
	require.Equal(t, currentSubmission.ID, workflow.CurrentSubmission.ID)
	require.Equal(t, owner.UserPublicID, workflow.CurrentSubmission.SubmitterUserID)
	require.Equal(t, reviewer.UserPublicID, workflow.CurrentSubmission.ReviewerUserID)
	require.NotNil(t, workflow.CurrentSubmission.Decision)
	require.Equal(t, "approved", workflow.CurrentSubmission.Decision.Decision)

	// Tasks remain manual by default, so they can still be completed without
	// creating a submission or review decision.
	manualTaskID := seedPublicTask(t, owner, "Manual completion remains available")
	var completed struct {
		DerivedState string `json:"derivedState"`
	}
	doJSON(t, http.MethodPost,
		testServerURL+"/tasks/"+manualTaskID+"/transitions", owner.AccessToken,
		map[string]any{"transition": "complete"}, &completed)
	require.Equal(t, "done", completed.DerivedState)
}

// TestHumanHandoffKeepsResponsibilityUntilAcceptance checks that requesting a
// handoff does not silently transfer the task and that the recipient's live
// task visibility is checked again at acceptance time.
func TestHumanHandoffKeepsResponsibilityUntilAcceptance(t *testing.T) {
	bootstrap(t)
	t.Parallel()

	owner := newTenant(t)
	recipient := seedProjectRoleMember(t, owner, "viewer")
	outsider := seedProjectRoleMember(t, owner, "viewer")
	taskID := seedPublicTask(t, owner, "Accepted handoff")
	base := testServerURL + "/tasks/" + taskID

	var handoff struct {
		ID string `json:"id"`
	}
	doJSON(t, http.MethodPost, base+"/handoffs", owner.AccessToken,
		map[string]any{"role": "assignee", "toUserId": recipient.UserPublicID}, &handoff)
	require.NotEmpty(t, handoff.ID)
	var pendingWorkflow struct {
		Handoffs []struct {
			ID       string `json:"id"`
			ToUserID string `json:"toUserId"`
			Status   string `json:"status"`
		} `json:"handoffs"`
	}
	doJSON(t, http.MethodGet, base+"/workflow", owner.AccessToken, nil, &pendingWorkflow)
	require.Contains(t, pendingWorkflow.Handoffs, struct {
		ID       string `json:"id"`
		ToUserID string `json:"toUserId"`
		Status   string `json:"status"`
	}{ID: handoff.ID, ToUserID: recipient.UserPublicID, Status: "pending"})

	assertAssignee := func(wantUserID string) {
		t.Helper()
		var actors struct {
			Actors []struct {
				UserID string `json:"userId"`
				Role   string `json:"role"`
			} `json:"actors"`
		}
		doJSON(t, http.MethodGet, base+"/actors", owner.AccessToken, nil, &actors)
		var assignees []string
		for _, actor := range actors.Actors {
			if actor.Role == "assignee" {
				assignees = append(assignees, actor.UserID)
			}
		}
		require.Equal(t, []string{wantUserID}, assignees,
			"the task must have exactly the expected enabled human assignee")
	}
	assertAssignee(owner.UserPublicID)

	status, raw := doJSONStatus(t, http.MethodPost,
		base+"/handoffs/"+handoff.ID+"/decision", outsider.AccessToken,
		map[string]any{"decision": "accept"})
	require.Equal(t, http.StatusForbidden, status,
		"only the pending recipient may decide, body=%s", string(raw))
	assertAssignee(owner.UserPublicID)

	// The recipient was allowed to see this public task at request time, but
	// loses that access before acceptance. The pending handoff must not itself
	// grant access or authorize an actor mutation.
	doJSON(t, http.MethodPatch, base, owner.AccessToken,
		map[string]any{"visibility": "private"}, nil)
	status, raw = doJSONStatus(t, http.MethodPost,
		base+"/handoffs/"+handoff.ID+"/decision", recipient.AccessToken,
		map[string]any{"decision": "accept"})
	require.Equal(t, http.StatusNotFound, status,
		"acceptance must re-check current task read access, body=%s", string(raw))
	assertAssignee(owner.UserPublicID)

	doJSON(t, http.MethodPatch, base, owner.AccessToken,
		map[string]any{"visibility": "public"}, nil)
	doJSON(t, http.MethodPost,
		base+"/handoffs/"+handoff.ID+"/decision", recipient.AccessToken,
		map[string]any{"decision": "accept"}, nil)
	assertAssignee(recipient.UserPublicID)
	var acceptedWorkflow struct {
		Handoffs []struct {
			ID     string `json:"id"`
			Status string `json:"status"`
		} `json:"handoffs"`
	}
	doJSON(t, http.MethodGet, base+"/workflow", recipient.AccessToken, nil, &acceptedWorkflow)
	var acceptedFound bool
	for _, item := range acceptedWorkflow.Handoffs {
		if item.ID == handoff.ID && item.Status == "accepted" {
			acceptedFound = true
			break
		}
	}
	require.True(t, acceptedFound, "the accepted handoff must remain visible in the persisted workflow snapshot")
}

// TestExpiredHandoffEmitsOneDecisionWithoutTransferringRole exercises the
// bounded expiry producer against the real database. The worker is run twice
// at a supplied clock time so duplicate resolution events cannot hide behind
// process-local state.
func TestExpiredHandoffEmitsOneDecisionWithoutTransferringRole(t *testing.T) {
	bootstrap(t)

	owner := newTenant(t)
	recipient := seedProjectRoleMember(t, owner, "viewer")
	taskID := seedPublicTask(t, owner, "Expired handoff")
	dueAt := time.Now().UTC().Add(-time.Minute).Unix()
	var handoff struct {
		ID string `json:"id"`
	}
	doJSON(t, http.MethodPost, testServerURL+"/tasks/"+taskID+"/handoffs", owner.AccessToken,
		map[string]any{"role": "assignee", "toUserId": recipient.UserPublicID, "dueAt": dueAt}, &handoff)
	require.NotEmpty(t, handoff.ID)

	queries := generated.New(testDB)
	runner := taskworkflow.NewHandoffExpiryRunner(testDB, queries, mutationlog.New(testDB, queries))
	now := time.Now().UTC()
	processed, err := runner.RunOnce(context.Background(), now, 100)
	require.NoError(t, err)
	require.GreaterOrEqual(t, processed, 1, "the due handoff must be resolved in this run")
	_, err = runner.RunOnce(context.Background(), now, 100)
	require.NoError(t, err)

	var workflow struct {
		Handoffs []struct {
			ID     string `json:"id"`
			Status string `json:"status"`
		} `json:"handoffs"`
	}
	doJSON(t, http.MethodGet, testServerURL+"/tasks/"+taskID+"/workflow", owner.AccessToken, nil, &workflow)
	var expiredFound bool
	for _, item := range workflow.Handoffs {
		if item.ID == handoff.ID && item.Status == "expired" {
			expiredFound = true
			break
		}
	}
	require.True(t, expiredFound, "expiry must be persisted for reload")

	workspaceID, taskInternalID := helpers.AgentTaskInternalIDs(t, testDB, owner.WorkspacePublicID, taskID)
	var eventCount int
	var targetUserPublicID string
	err = testDB.QueryRowContext(context.Background(), `
		SELECT COUNT(*),
		       MAX(JSON_UNQUOTE(JSON_EXTRACT(payload_json, '$.targetUserPublicId')))
		FROM events
		WHERE workspace_id = ?
		  AND task_id = ?
		  AND type = 'task.handoff.decided'
		  AND JSON_UNQUOTE(JSON_EXTRACT(payload_json, '$.handoffId')) = ?
		  AND JSON_UNQUOTE(JSON_EXTRACT(payload_json, '$.decision')) = 'expired'
	`, workspaceID, taskInternalID, handoff.ID).Scan(&eventCount, &targetUserPublicID)
	require.NoError(t, err)
	require.Equal(t, 1, eventCount, "repeated RunOnce calls must not append duplicate expiry events")
	require.Equal(t, owner.UserPublicID, targetUserPublicID,
		"expiry notification targeting must retain the original role holder's public id")

	var actors struct {
		Actors []struct {
			UserID string `json:"userId"`
			Role   string `json:"role"`
		} `json:"actors"`
	}
	doJSON(t, http.MethodGet, testServerURL+"/tasks/"+taskID+"/actors", owner.AccessToken, nil, &actors)
	var assignees []string
	for _, actor := range actors.Actors {
		if actor.Role == "assignee" {
			assignees = append(assignees, actor.UserID)
		}
	}
	require.Equal(t, []string{owner.UserPublicID}, assignees,
		"expiry sends no actor transfer; the original holder stays responsible")
}

// TestWorkflowReadRecentSubmissionsBoundedAndTaskScoped proves that review
// history survives reloads, remains bounded to the newest ten entries, and
// follows the task's current read ACL.
func TestWorkflowReadRecentSubmissionsBoundedAndTaskScoped(t *testing.T) {
	bootstrap(t)
	t.Parallel()

	owner := newTenant(t)
	reviewer := seedProjectRoleMember(t, owner, "viewer")
	outsider := seedProjectRoleMember(t, owner, "viewer")
	taskID := seedPublicTask(t, owner, "Bounded review history")
	base := testServerURL + "/tasks/" + taskID

	doJSON(t, http.MethodPost, base+"/actors", owner.AccessToken,
		map[string]any{"userId": reviewer.UserPublicID, "role": "reviewer"}, nil)
	doJSON(t, http.MethodPost, base+"/completion-policy", owner.AccessToken,
		map[string]any{"policy": "review_required"}, nil)
	doJSON(t, http.MethodPatch, base, owner.AccessToken,
		map[string]any{"visibility": "private"}, nil)

	createdIDs := make([]string, 12)
	for index := range createdIDs {
		var submission struct {
			ID       string `json:"id"`
			Revision uint32 `json:"revision"`
		}
		evidenceURL := "https://example.test/history/" + fmt.Sprint(index)
		doJSON(t, http.MethodPost, base+"/submissions", owner.AccessToken,
			map[string]any{
				"reviewerUserId":     reviewer.UserPublicID,
				"acceptanceCriteria": []string{"snapshot " + fmt.Sprint(index)},
				"evidenceUrls":       []string{evidenceURL},
			}, &submission)
		require.NotEmpty(t, submission.ID)
		createdIDs[index] = submission.ID
	}

	var workflow struct {
		WorkRevision      uint32 `json:"workRevision"`
		CurrentSubmission *struct {
			ID       string `json:"id"`
			Revision uint32 `json:"revision"`
		} `json:"currentSubmission"`
		RecentSubmissions []struct {
			ID                 string   `json:"id"`
			Revision           uint32   `json:"revision"`
			CreatedAt          int64    `json:"createdAt"`
			AcceptanceCriteria []string `json:"acceptanceCriteria"`
			EvidenceURLs       []string `json:"evidenceUrls"`
		} `json:"recentSubmissions"`
		Capabilities struct {
			CanSubmit bool `json:"canSubmit"`
		} `json:"capabilities"`
	}
	doJSON(t, http.MethodGet, base+"/workflow", owner.AccessToken, nil, &workflow)
	require.Len(t, workflow.RecentSubmissions, 10,
		"the workflow response must keep a bounded newest-ten review history")
	require.NotNil(t, workflow.CurrentSubmission)
	require.Equal(t, createdIDs[11], workflow.CurrentSubmission.ID)
	require.Equal(t, workflow.WorkRevision, workflow.CurrentSubmission.Revision)
	require.True(t, workflow.Capabilities.CanSubmit,
		"a task editor may submit again while the task is in review")
	for index, item := range workflow.RecentSubmissions {
		wantID := createdIDs[len(createdIDs)-1-index]
		require.Equal(t, wantID, item.ID, "history should be ordered newest first")
		require.Equal(t, workflow.WorkRevision, item.Revision)
		require.Positive(t, item.CreatedAt, "history timestamps use Unix seconds")
		require.Equal(t, []string{"snapshot " + fmt.Sprint(11-index)}, item.AcceptanceCriteria)
		require.Equal(t, []string{"https://example.test/history/" + fmt.Sprint(11-index)}, item.EvidenceURLs)
	}

	status, raw := doJSONStatus(t, http.MethodGet, base+"/workflow", outsider.AccessToken, nil)
	require.Equal(t, http.StatusNotFound, status,
		"workflow history must inherit current task-read ACL, body=%s", string(raw))
}

func TestSubmissionTransitionsTaskToReviewCanonically(t *testing.T) {
	bootstrap(t)
	t.Parallel()

	owner := newTenant(t)
	reviewer := seedProjectRoleMember(t, owner, "viewer")

	openTask := seedPublicTask(t, owner, "Open submission starts at submit time")
	configureReviewSubmissionTask(t, owner, openTask, reviewer.UserPublicID)
	submitWorkflowTestSnapshot(t, owner, openTask, reviewer.UserPublicID, "open evidence")
	require.Equal(t, "review", workflowTaskDerivedState(t, owner.WorkspacePublicID, openTask))
	requireWorkflowTransitionSequence(t, owner, openTask, []string{
		"task.transition.start",
		"task.transition.submit",
	})

	// A resubmission while already in review adds a snapshot and review event,
	// but it must not manufacture another state transition.
	submitWorkflowTestSnapshot(t, owner, openTask, reviewer.UserPublicID, "review resubmission evidence")
	requireWorkflowTransitionSequence(t, owner, openTask, []string{
		"task.transition.start",
		"task.transition.submit",
	})

	waitingTask := seedPublicTask(t, owner, "Waiting submission")
	configureReviewSubmissionTask(t, owner, waitingTask, reviewer.UserPublicID)
	doJSON(t, http.MethodPost, testServerURL+"/tasks/"+waitingTask+"/transitions", owner.AccessToken,
		map[string]any{"transition": "start"}, nil)
	submitWorkflowTestSnapshot(t, owner, waitingTask, reviewer.UserPublicID, "waiting evidence")
	require.Equal(t, "review", workflowTaskDerivedState(t, owner.WorkspacePublicID, waitingTask))
	requireWorkflowTransitionSequence(t, owner, waitingTask, []string{
		"task.transition.start",
		"task.transition.submit",
	})

	blockedTask := seedPublicTask(t, owner, "Blocked submission")
	configureReviewSubmissionTask(t, owner, blockedTask, reviewer.UserPublicID)
	submitWorkflowTestSnapshot(t, owner, blockedTask, reviewer.UserPublicID, "pre-block evidence")
	doJSON(t, http.MethodPost, testServerURL+"/tasks/"+blockedTask+"/transitions", owner.AccessToken,
		map[string]any{"transition": "block"}, nil)
	beforeBlockedSubmission := workflowSubmissionEventCount(t, owner.WorkspacePublicID, blockedTask)
	status, raw := doJSONStatus(t, http.MethodPost, testServerURL+"/tasks/"+blockedTask+"/submissions", owner.AccessToken,
		map[string]any{
			"reviewerUserId":     reviewer.UserPublicID,
			"acceptanceCriteria": []string{"Must not pass while blocked"},
		})
	require.GreaterOrEqual(t, status, http.StatusBadRequest,
		"a blocked task cannot enter review, body=%s", string(raw))
	require.Less(t, status, http.StatusInternalServerError)
	require.Equal(t, "waiting", workflowTaskDerivedState(t, owner.WorkspacePublicID, blockedTask))
	requireWorkflowTransitionSequence(t, owner, blockedTask, []string{
		"task.transition.start",
		"task.transition.submit",
		"task.transition.block",
	})
	require.Equal(t, beforeBlockedSubmission, workflowSubmissionEventCount(t, owner.WorkspacePublicID, blockedTask),
		"a rejected submission must not append a submission event")
	var blockedWorkflow struct {
		Capabilities struct {
			CanSubmit bool `json:"canSubmit"`
		} `json:"capabilities"`
	}
	doJSON(t, http.MethodGet, testServerURL+"/tasks/"+blockedTask+"/workflow", owner.AccessToken, nil, &blockedWorkflow)
	require.False(t, blockedWorkflow.Capabilities.CanSubmit,
		"the read capability must reflect that an explicit unblock is required")

	doJSON(t, http.MethodPost, testServerURL+"/tasks/"+blockedTask+"/transitions", owner.AccessToken,
		map[string]any{"transition": "unblock"}, nil)
	submitWorkflowTestSnapshot(t, owner, blockedTask, reviewer.UserPublicID, "unblocked evidence")
	requireWorkflowTransitionSequence(t, owner, blockedTask, []string{
		"task.transition.start",
		"task.transition.submit",
		"task.transition.block",
		"task.transition.unblock",
		"task.transition.start",
		"task.transition.submit",
	})
}

func TestSubmissionRejectsBlockedDoneAndCancelledTasks(t *testing.T) {
	bootstrap(t)
	t.Parallel()

	owner := newTenant(t)
	reviewer := seedProjectRoleMember(t, owner, "viewer")

	doneTask := seedPublicTask(t, owner, "Completed submission is rejected")
	doJSON(t, http.MethodPost, testServerURL+"/tasks/"+doneTask+"/transitions", owner.AccessToken,
		map[string]any{"transition": "complete"}, nil)
	configureReviewSubmissionTask(t, owner, doneTask, reviewer.UserPublicID)
	requireRejectedWorkflowSubmission(t, owner, doneTask, reviewer.UserPublicID)
	require.Equal(t, "done", workflowTaskDerivedState(t, owner.WorkspacePublicID, doneTask))
	requireWorkflowTransitionSequence(t, owner, doneTask, []string{"task.transition.complete"})

	cancelledTask := seedPublicTask(t, owner, "Cancelled submission is rejected")
	configureReviewSubmissionTask(t, owner, cancelledTask, reviewer.UserPublicID)
	doJSON(t, http.MethodPost, testServerURL+"/tasks/"+cancelledTask+"/transitions", owner.AccessToken,
		map[string]any{"transition": "cancel"}, nil)
	requireRejectedWorkflowSubmission(t, owner, cancelledTask, reviewer.UserPublicID)
	require.Equal(t, "cancelled", workflowTaskDerivedState(t, owner.WorkspacePublicID, cancelledTask))
	requireWorkflowTransitionSequence(t, owner, cancelledTask, []string{"task.transition.cancel"})
}

func configureReviewSubmissionTask(t *testing.T, owner *helpers.TestTenant, taskID, reviewerUserID string) {
	t.Helper()
	base := testServerURL + "/tasks/" + taskID
	doJSON(t, http.MethodPost, base+"/actors", owner.AccessToken,
		map[string]any{"userId": reviewerUserID, "role": "reviewer"}, nil)
	doJSON(t, http.MethodPost, base+"/completion-policy", owner.AccessToken,
		map[string]any{"policy": "review_required"}, nil)
}

func submitWorkflowTestSnapshot(t *testing.T, owner *helpers.TestTenant, taskID, reviewerUserID, evidence string) {
	t.Helper()
	doJSON(t, http.MethodPost, testServerURL+"/tasks/"+taskID+"/submissions", owner.AccessToken,
		map[string]any{
			"reviewerUserId":     reviewerUserID,
			"acceptanceCriteria": []string{"Work matches the stated criteria"},
			"evidenceUrls":       []string{"https://example.test/" + evidence},
		}, nil)
}

func requireRejectedWorkflowSubmission(t *testing.T, owner *helpers.TestTenant, taskID, reviewerUserID string) {
	t.Helper()
	status, raw := doJSONStatus(t, http.MethodPost,
		testServerURL+"/tasks/"+taskID+"/submissions", owner.AccessToken,
		map[string]any{
			"reviewerUserId":     reviewerUserID,
			"acceptanceCriteria": []string{"This submission should be rejected"},
		})
	require.GreaterOrEqual(t, status, http.StatusBadRequest,
		"submission from a terminal task must be rejected, body=%s", string(raw))
	require.Less(t, status, http.StatusInternalServerError)
	require.Zero(t, workflowSubmissionEventCount(t, owner.WorkspacePublicID, taskID),
		"rejected submissions must roll back without domain events")
}

func workflowTaskDerivedState(t *testing.T, workspacePublicID, taskPublicID string) string {
	t.Helper()
	_, taskID := helpers.AgentTaskInternalIDs(t, testDB, workspacePublicID, taskPublicID)
	var state string
	require.NoError(t, testDB.QueryRowContext(context.Background(),
		"SELECT derived_state FROM tasks WHERE id = ?", taskID).Scan(&state))
	return state
}

func workflowSubmissionEventCount(t *testing.T, workspacePublicID, taskPublicID string) int {
	t.Helper()
	workspaceID, taskID := helpers.AgentTaskInternalIDs(t, testDB, workspacePublicID, taskPublicID)
	var count int
	require.NoError(t, testDB.QueryRowContext(context.Background(), `
		SELECT COUNT(*) FROM events
		WHERE workspace_id = ? AND task_id = ? AND type = 'task.submission.created'
	`, workspaceID, taskID).Scan(&count))
	return count
}

func requireWorkflowTransitionSequence(t *testing.T, owner *helpers.TestTenant, taskPublicID string, want []string) {
	t.Helper()
	workspaceID, taskID := helpers.AgentTaskInternalIDs(t, testDB, owner.WorkspacePublicID, taskPublicID)
	rows, err := testDB.QueryContext(context.Background(), `
		SELECT type, JSON_UNQUOTE(JSON_EXTRACT(payload_json, '$.via')), actor_user_id
		FROM events
		WHERE workspace_id = ? AND task_id = ? AND type LIKE 'task.transition.%'
		ORDER BY id ASC
	`, workspaceID, taskID)
	require.NoError(t, err)
	defer rows.Close()

	var (
		got     []string
		ownerID = lookupUserInternalID(context.Background(), t, testDB, owner.UserPublicID)
	)
	for rows.Next() {
		var eventType string
		var via sql.NullString
		var actorID sql.NullInt32
		require.NoError(t, rows.Scan(&eventType, &via, &actorID))
		got = append(got, eventType)
		require.True(t, actorID.Valid, "%s must carry the initiating human actor", eventType)
		require.Equal(t, int64(ownerID), int64(actorID.Int32), "%s actor should be the submitting editor", eventType)
		require.True(t, via.Valid, "%s must record its API origin", eventType)
		require.Equal(t, "api", via.String)
	}
	require.NoError(t, rows.Err())
	require.Equal(t, want, got, "submission must emit only the canonical state transitions")
}
