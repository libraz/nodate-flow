package e2e

import (
	"database/sql"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/libraz/nodate-flow/apps/flow-api/tests/helpers"
)

// constraintWorkspaceID returns the tenant's internal workspace id, the key
// helpers.WaitReevalIdle tracks jobs by.
func constraintWorkspaceID(t *testing.T, tt *helpers.TestTenant) uint32 {
	t.Helper()
	return helpers.ResolveWorkspaceInternalID(t, testDB, tt.WorkspacePublicID)
}

// addTaskConstraint adds a constraint and returns its public id.
func addTaskConstraint(t *testing.T, token, taskID, kind, expression string) string {
	t.Helper()
	var con struct {
		ID string `json:"id"`
	}
	doJSON(t, http.MethodPost, testServerURL+"/tasks/"+taskID+"/constraints", token,
		map[string]any{"kind": kind, "expression": expression}, &con)
	require.NotEmpty(t, con.ID)
	return con.ID
}

// waitConstraintSatisfied polls until the constraint reads as satisfied.
func waitConstraintSatisfied(t *testing.T, constraintID string) {
	t.Helper()
	waitConstraintMarkers(t, constraintID, func(s, f sql.NullTime) bool { return s.Valid && !f.Valid })
}

// waitConstraintFailed polls until the constraint reads as failing.
func waitConstraintFailed(t *testing.T, constraintID string) {
	t.Helper()
	waitConstraintMarkers(t, constraintID, func(s, f sql.NullTime) bool { return !s.Valid && f.Valid })
}

// verdictEventCount counts the task's verdict events of one verdict
// ("satisfied" or "failed").
func verdictEventCount(t *testing.T, taskID, verdict string) int {
	t.Helper()
	return taskActorEventCount(t, taskID, "task.constraint."+verdict)
}

// requireVerdictEvents asserts the task's verdict event counts.
func requireVerdictEvents(t *testing.T, taskID string, satisfied, failed int, msg string) {
	t.Helper()
	require.Equal(t, satisfied, verdictEventCount(t, taskID, "satisfied"), "%s: satisfied events", msg)
	require.Equal(t, failed, verdictEventCount(t, taskID, "failed"), "%s: failed events", msg)
}

func addBlockingEdge(t *testing.T, token, fromTaskID, toTaskID, kind string) {
	t.Helper()
	doJSON(t, http.MethodPost, testServerURL+"/tasks/"+fromTaskID+"/dependencies", token,
		map[string]any{"toTaskId": toTaskID, "kind": kind}, nil)
}

func completeTaskViaTransitions(t *testing.T, token, taskID string) {
	t.Helper()
	for _, tr := range []string{"start", "submit", "complete"} {
		doJSON(t, http.MethodPost, testServerURL+"/tasks/"+taskID+"/transitions", token,
			map[string]any{"transition": tr}, nil)
	}
}

// TestPatchDueOnReevaluatesConstraints pins that a due_on edit moves the
// deadline constraint's marker with no explicit evaluate call, both ways.
func TestPatchDueOnReevaluatesConstraints(t *testing.T) {
	bootstrap(t)
	t.Parallel()
	tt := newTenant(t)

	var task struct {
		ID string `json:"id"`
	}
	doJSON(t, http.MethodPost, testServerURL+"/tasks", tt.AccessToken, map[string]any{
		"projectId": tt.ProjectPublicID,
		"title":     "Deadline follows the date",
		"dueOn":     "2026-01-01",
	}, &task)
	require.NotEmpty(t, task.ID)
	con := addTaskConstraint(t, tt.AccessToken, task.ID, "deadline", `{"op":"time.due_before","arg":"2100-01-01"}`)
	waitConstraintSatisfied(t, con)

	doJSON(t, http.MethodPatch, testServerURL+"/tasks/"+task.ID, tt.AccessToken,
		map[string]any{"dueOn": "2101-01-01"}, nil)
	waitConstraintFailed(t, con)

	doJSON(t, http.MethodPatch, testServerURL+"/tasks/"+task.ID, tt.AccessToken,
		map[string]any{"dueOn": "2000-01-01"}, nil)
	waitConstraintSatisfied(t, con)
}

// TestConstraintOutcomeEventOnlyOnChange pins that a verdict event is
// appended when the verdict changes, the first evaluation included, and
// never for a re-evaluation that leaves it as it was.
func TestConstraintOutcomeEventOnlyOnChange(t *testing.T) {
	bootstrap(t)
	t.Parallel()
	tt := newTenant(t)
	ws := constraintWorkspaceID(t, tt)

	a := createTask(t, tt.AccessToken, tt.ProjectPublicID, "Verdict subject")
	b := createTask(t, tt.AccessToken, tt.ProjectPublicID, "Verdict blocker")
	addBlockingEdge(t, tt.AccessToken, a, b, "blocks")
	con := addAllDoneConstraint(t, tt.AccessToken, a, b)

	helpers.WaitReevalIdle(t, ws)
	requireVerdictEvents(t, a, 0, 1, "the first evaluation of an unmet constraint")
	waitConstraintFailed(t, con)

	completeTaskViaTransitions(t, tt.AccessToken, b)
	helpers.WaitReevalIdle(t, ws)
	requireVerdictEvents(t, a, 1, 1, "completing the blocker")
	waitConstraintSatisfied(t, con)

	doJSON(t, http.MethodPatch, testServerURL+"/tasks/"+a, tt.AccessToken,
		map[string]any{"title": "Verdict subject, renamed"}, nil)
	helpers.WaitReevalIdle(t, ws)
	requireVerdictEvents(t, a, 1, 1, "a re-evaluation that changes nothing")

	doJSON(t, http.MethodPost, testServerURL+"/tasks/"+b+"/transitions", tt.AccessToken,
		map[string]any{"transition": "reopen"}, nil)
	helpers.WaitReevalIdle(t, ws)
	requireVerdictEvents(t, a, 1, 2, "reopening the blocker")
	waitConstraintFailed(t, con)
}

// approvedReviewTask builds a review_required task whose current
// submission is approved and whose completion requires the constraint.
func approvedReviewTask(t *testing.T, owner, reviewer *helpers.TestTenant, title, kind, expression string, setup func(taskID string)) (taskID, constraintID string) {
	t.Helper()
	taskID = seedPublicTask(t, owner, title)
	base := testServerURL + "/tasks/" + taskID
	constraintID = addTaskConstraint(t, owner.AccessToken, taskID, kind, expression)
	if setup != nil {
		setup(taskID)
	}
	doJSON(t, http.MethodPost, base+"/actors", owner.AccessToken,
		map[string]any{"userId": reviewer.UserPublicID, "role": "reviewer"}, nil)
	doJSON(t, http.MethodPost, base+"/completion-policy", owner.AccessToken,
		map[string]any{"policy": "review_required", "requiredConstraintIds": []string{constraintID}}, nil)
	var submission struct {
		ID string `json:"id"`
	}
	doJSON(t, http.MethodPost, base+"/submissions", owner.AccessToken,
		map[string]any{
			"reviewerUserId":     reviewer.UserPublicID,
			"acceptanceCriteria": []string{"Approved and constrained"},
		}, &submission)
	require.NotEmpty(t, submission.ID)
	doJSON(t, http.MethodPost, base+"/submissions/"+submission.ID+"/decision", reviewer.AccessToken,
		map[string]any{"decision": "approve"}, nil)
	return taskID, constraintID
}

func completeTaskStatus(t *testing.T, token, taskID string) (int, []byte) {
	t.Helper()
	return doJSONStatus(t, http.MethodPost, testServerURL+"/tasks/"+taskID+"/transitions", token,
		map[string]any{"transition": "complete"})
}

func requireCompleteAllowed(t *testing.T, token, taskID, msg string) {
	t.Helper()
	var out struct {
		DerivedState string `json:"derivedState"`
	}
	doJSON(t, http.MethodPost, testServerURL+"/tasks/"+taskID+"/transitions", token,
		map[string]any{"transition": "complete"}, &out)
	require.Equal(t, "done", out.DerivedState, msg)
}

func requireCompleteRejected(t *testing.T, token, taskID, msg string) {
	t.Helper()
	status, raw := completeTaskStatus(t, token, taskID)
	requireDenied(t, status, raw, http.StatusUnprocessableEntity, "WS.TASK.TRANSITION_REJECTED", msg)
}

// TestCompleteGateUsesFreshVerdict pins that the completion gate decides on
// the facts as they are when complete is asked, not on the marker the
// asynchronous re-evaluation last wrote. Each half is paired with the
// control that completes under the same setup.
func TestCompleteGateUsesFreshVerdict(t *testing.T) {
	bootstrap(t)
	t.Parallel()

	owner := newTenant(t)
	reviewer := seedProjectRoleMember(t, owner, "viewer")
	approver := seedProjectRoleMember(t, owner, "viewer")

	t.Run("a role removed just before complete is not counted", func(t *testing.T) {
		t.Parallel()
		const expr = `{"op":"actor.has_role","arg":"approver"}`
		addApprover := func(taskID string) string {
			var actor struct {
				ID string `json:"id"`
			}
			doJSON(t, http.MethodPost, testServerURL+"/tasks/"+taskID+"/actors", owner.AccessToken,
				map[string]any{"userId": approver.UserPublicID, "role": "approver"}, &actor)
			require.NotEmpty(t, actor.ID)
			return actor.ID
		}

		control, controlCon := approvedReviewTask(t, owner, reviewer, "Gate control: approver kept", "approval", expr, nil)
		addApprover(control)
		waitConstraintSatisfied(t, controlCon)
		requireCompleteAllowed(t, owner.AccessToken, control, "with the approver in place complete must pass")

		target, targetCon := approvedReviewTask(t, owner, reviewer, "Gate target: approver removed", "approval", expr, nil)
		actorID := addApprover(target)
		waitConstraintSatisfied(t, targetCon)
		removeTaskActorForMutationTest(t, owner.AccessToken, target, actorID)
		requireCompleteRejected(t, owner.AccessToken, target,
			"complete right after the approver left must be refused")

		addApprover(target)
		requireCompleteAllowed(t, owner.AccessToken, target,
			"complete right after the approver returned must pass")
	})

	t.Run("a blocker completed just before complete is counted", func(t *testing.T) {
		t.Parallel()
		blocker := seedPublicTask(t, owner, "Gate blocker")
		dependent, _ := approvedReviewTask(t, owner, reviewer, "Gate dependent", "dependency",
			`{"op":"dependency.all_done","taskIds":["`+blocker+`"]}`,
			func(taskID string) { addBlockingEdge(t, owner.AccessToken, taskID, blocker, "relates") })

		requireCompleteRejected(t, owner.AccessToken, dependent,
			"complete while the blocker is open must be refused")

		completeTaskViaTransitions(t, owner.AccessToken, blocker)
		requireCompleteAllowed(t, owner.AccessToken, dependent,
			"complete right after the blocker finished must pass")
	})
}

// openDependencyExpr is met when no blocking dependency of the task is open.
const openDependencyExpr = `{"op":"dependency.open_at_most","max":0}`

// TestDeletedTaskEmitsNoVerdictButReevaluatesDependents pins that deleting a
// task records no verdict for it while the tasks depending on it are
// re-evaluated.
func TestDeletedTaskEmitsNoVerdictButReevaluatesDependents(t *testing.T) {
	bootstrap(t)
	t.Parallel()
	tt := newTenant(t)
	ws := constraintWorkspaceID(t, tt)

	var blocker struct {
		ID string `json:"id"`
	}
	doJSON(t, http.MethodPost, testServerURL+"/tasks", tt.AccessToken, map[string]any{
		"projectId": tt.ProjectPublicID,
		"title":     "Deleted blocker",
		"dueOn":     "2101-01-01",
	}, &blocker)
	require.NotEmpty(t, blocker.ID)
	blockerCon := addTaskConstraint(t, tt.AccessToken, blocker.ID, "deadline", `{"op":"time.due_before","arg":"2100-01-01"}`)

	dependent := createTask(t, tt.AccessToken, tt.ProjectPublicID, "Waits on the deleted blocker")
	addBlockingEdge(t, tt.AccessToken, dependent, blocker.ID, "blocks")
	depCon := addTaskConstraint(t, tt.AccessToken, dependent, "dependency", openDependencyExpr)

	helpers.WaitReevalIdle(t, ws)
	waitConstraintFailed(t, blockerCon)
	waitConstraintFailed(t, depCon)
	requireVerdictEvents(t, blocker.ID, 0, 1, "fixture: the blocker's own verdict")

	status, raw := doJSONStatus(t, http.MethodDelete, testServerURL+"/tasks/"+blocker.ID, tt.AccessToken, nil)
	require.Equal(t, http.StatusOK, status, string(raw))

	waitConstraintSatisfied(t, depCon)
	helpers.WaitReevalIdle(t, ws)
	requireVerdictEvents(t, blocker.ID, 0, 1, "deleting the task")
	requireVerdictEvents(t, dependent, 1, 1, "the dependent after the delete")
}

// TestCalendarRescheduleReevaluatesConstraints pins that moving a task's
// calendar event, which reschedules the task through itemkit, moves the
// deadline constraint's marker.
func TestCalendarRescheduleReevaluatesConstraints(t *testing.T) {
	bootstrap(t)
	t.Parallel()
	tt := newTenant(t)

	taskID := createTaskWithDue(t, tt, "Rescheduled on the calendar", "2027-06-01")
	con := addTaskConstraint(t, tt.AccessToken, taskID, "deadline", `{"op":"time.due_before","arg":"2027-06-05"}`)
	waitConstraintSatisfied(t, con)

	calID := createCalendarMut(t, tt, "Reschedule projection")
	var projected struct {
		ID string `json:"id"`
	}
	doJSON(t, http.MethodPost,
		testServerURL+"/workspaces/"+tt.WorkspacePublicID+"/calendars/"+calID+"/events/from-task",
		tt.AccessToken, map[string]any{"taskId": taskID, "timezone": "UTC"}, &projected)
	require.NotEmpty(t, projected.ID)

	doJSON(t, http.MethodPatch,
		testServerURL+"/workspaces/"+tt.WorkspacePublicID+"/calendars/"+calID+"/events/"+projected.ID,
		tt.AccessToken, map[string]any{
			"startAt": time.Date(2027, 6, 8, 0, 0, 0, 0, time.UTC).Unix(),
			"endAt":   time.Date(2027, 6, 9, 0, 0, 0, 0, time.UTC).Unix(),
		}, nil)
	require.Equal(t, "2027-06-08", taskDueDay(t, taskID), "fixture: the event move must reach the task")

	waitConstraintFailed(t, con)
}

// TestProjectDisableReevaluatesCrossProjectDependents pins that disabling a
// project re-evaluates dependents in other projects and records no verdict
// for the tasks it disabled.
func TestProjectDisableReevaluatesCrossProjectDependents(t *testing.T) {
	bootstrap(t)
	t.Parallel()
	tt := newTenant(t)
	ws := constraintWorkspaceID(t, tt)

	var other struct {
		ID string `json:"id"`
	}
	doJSON(t, http.MethodPost, testServerURL+"/workspaces/"+tt.WorkspacePublicID+"/projects", tt.AccessToken,
		map[string]any{"slug": "dep-" + randomHex(4), "name": "Dependents"}, &other)
	require.NotEmpty(t, other.ID)

	var blocker struct {
		ID string `json:"id"`
	}
	doJSON(t, http.MethodPost, testServerURL+"/tasks", tt.AccessToken, map[string]any{
		"projectId": tt.ProjectPublicID,
		"title":     "Blocker in the disabled project",
		"dueOn":     "2101-01-01",
	}, &blocker)
	require.NotEmpty(t, blocker.ID)
	blockerCon := addTaskConstraint(t, tt.AccessToken, blocker.ID, "deadline", `{"op":"time.due_before","arg":"2100-01-01"}`)

	dependent := createTask(t, tt.AccessToken, other.ID, "Dependent in another project")
	addBlockingEdge(t, tt.AccessToken, dependent, blocker.ID, "blocks")
	depCon := addTaskConstraint(t, tt.AccessToken, dependent, "dependency", openDependencyExpr)

	helpers.WaitReevalIdle(t, ws)
	waitConstraintFailed(t, blockerCon)
	waitConstraintFailed(t, depCon)
	requireVerdictEvents(t, blocker.ID, 0, 1, "fixture: the blocker's own verdict")

	status, raw := doJSONStatus(t, http.MethodDelete, testServerURL+"/projects/"+tt.ProjectPublicID, tt.AccessToken, nil)
	require.Equal(t, http.StatusOK, status, string(raw))

	waitConstraintSatisfied(t, depCon)
	helpers.WaitReevalIdle(t, ws)
	requireVerdictEvents(t, blocker.ID, 0, 1, "disabling the project")
	requireVerdictEvents(t, dependent, 1, 1, "the dependent after the project was disabled")
}

// TestArchivedSignalFailsSignalReceived pins that archiving a signal bound
// to a task fails that task's signal.received constraint, through both
// archive paths.
func TestArchivedSignalFailsSignalReceived(t *testing.T) {
	bootstrap(t)
	t.Parallel()

	const signalExpr = `{"op":"signal.received","arg":"manual"}`
	evaluate := func(t *testing.T, tt *helpers.TestTenant, taskID string) {
		t.Helper()
		doJSON(t, http.MethodPost, testServerURL+"/tasks/"+taskID+"/constraints/evaluate", tt.AccessToken, nil, nil)
	}

	t.Run("inbox archive", func(t *testing.T) {
		t.Parallel()
		tt := newTenant(t)
		taskID := createTask(t, tt.AccessToken, tt.ProjectPublicID, "Signal bound, archived from the inbox")
		con := addTaskConstraint(t, tt.AccessToken, taskID, "signal", signalExpr)

		var signal struct {
			ID string `json:"id"`
		}
		doJSON(t, http.MethodPost, testServerURL+"/signals", tt.AccessToken, map[string]any{
			"workspaceId": tt.WorkspacePublicID,
			"source":      "manual",
			"kind":        "manual",
			"taskId":      taskID,
		}, &signal)
		require.NotEmpty(t, signal.ID)
		helpers.WaitReevalIdle(t, constraintWorkspaceID(t, tt))
		evaluate(t, tt, taskID)
		waitConstraintSatisfied(t, con)

		status, raw := doJSONStatus(t, http.MethodPost,
			testServerURL+"/inbox/"+signal.ID+"/archive?workspaceId="+tt.WorkspacePublicID, tt.AccessToken, nil)
		require.Equal(t, http.StatusOK, status, string(raw))

		waitConstraintFailed(t, con)
	})

	t.Run("suggestion command archive", func(t *testing.T) {
		t.Parallel()
		tt := newTenant(t)
		taskID := createTaskWithVisibility(t, tt, "Signal bound, archived by suggestion", "public")
		con := addTaskConstraint(t, tt.AccessToken, taskID, "signal", signalExpr)
		// The seed writes events with raw, unretried inserts; let the constraint's own verdict append finish first.
		helpers.WaitReevalIdle(t, constraintWorkspaceID(t, tt))

		archive := seedSuggestionProposal(t, tt, tt, proposalSeed{action: "archive", taskPublicID: taskID, taskReadable: true})
		helpers.WaitReevalIdle(t, constraintWorkspaceID(t, tt))
		evaluate(t, tt, taskID)
		waitConstraintSatisfied(t, con)

		status, raw := executeSuggestion(t, tt, archive, "archive-"+randomHex(8), "archive")
		require.Equal(t, http.StatusOK, status, string(raw))

		waitConstraintFailed(t, con)
	})
}
