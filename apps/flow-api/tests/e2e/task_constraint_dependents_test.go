package e2e

import (
	"encoding/json"
	"net/http"
	"net/url"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func addAllDoneConstraint(t *testing.T, token, taskID string, blockers ...string) string {
	t.Helper()
	expr, err := json.Marshal(map[string]any{"op": "dependency.all_done", "taskIds": blockers})
	require.NoError(t, err)
	var con struct {
		ID string `json:"id"`
	}
	doJSON(t, http.MethodPost, testServerURL+"/tasks/"+taskID+"/constraints", token,
		map[string]any{"kind": "dependency", "expression": string(expr)}, &con)
	require.NotEmpty(t, con.ID)
	return con.ID
}

// TestCompletingBlockerReevaluatesDependent pins that a blocker's state
// change reaches the constraint markers of the task that depends on it,
// in both directions.
func TestCompletingBlockerReevaluatesDependent(t *testing.T) {
	bootstrap(t)
	t.Parallel()
	tt := newTenant(t)

	dependent := createTask(t, tt.AccessToken, tt.ProjectPublicID, "Dependent")
	blocker := createTask(t, tt.AccessToken, tt.ProjectPublicID, "Blocker")
	doJSON(t, http.MethodPost, testServerURL+"/tasks/"+dependent+"/dependencies", tt.AccessToken,
		map[string]any{"toTaskId": blocker, "kind": "blocks"}, nil)
	con := addAllDoneConstraint(t, tt.AccessToken, dependent, blocker)

	satisfiedAt, failedAt := readConstraintMarkers(t, con)
	require.False(t, satisfiedAt.Valid, "fixture: an open blocker leaves the constraint unmet")
	require.True(t, failedAt.Valid)

	for _, tr := range []string{"start", "submit", "complete"} {
		doJSON(t, http.MethodPost, testServerURL+"/tasks/"+blocker+"/transitions",
			tt.AccessToken, map[string]any{"transition": tr}, nil)
	}
	satisfiedAt, failedAt = readConstraintMarkers(t, con)
	require.True(t, satisfiedAt.Valid, "completing the blocker must satisfy the dependent's constraint")
	require.False(t, failedAt.Valid)

	doJSON(t, http.MethodPost, testServerURL+"/tasks/"+blocker+"/transitions",
		tt.AccessToken, map[string]any{"transition": "reopen"}, nil)
	satisfiedAt, failedAt = readConstraintMarkers(t, con)
	require.False(t, satisfiedAt.Valid, "reopening the blocker must clear the stale satisfied marker")
	require.True(t, failedAt.Valid)
}

// TestReevaluationKeepsSatisfiedAt pins that satisfied_at records when a
// constraint first became satisfied.
func TestReevaluationKeepsSatisfiedAt(t *testing.T) {
	bootstrap(t)
	t.Parallel()
	tt := newTenant(t)

	task := createTask(t, tt.AccessToken, tt.ProjectPublicID, "Re-evaluated")
	var con struct {
		ID string `json:"id"`
	}
	doJSON(t, http.MethodPost, testServerURL+"/tasks/"+task+"/constraints", tt.AccessToken,
		map[string]any{"kind": "deadline", "expression": `{"op":"time.due_after","arg":"1990-01-01"}`}, &con)
	// A task with no due date fails the deadline builtins, so give it one.
	doJSON(t, http.MethodPatch, testServerURL+"/tasks/"+task, tt.AccessToken,
		map[string]any{"dueOn": "2100-01-01"}, nil)
	doJSON(t, http.MethodPost, testServerURL+"/tasks/"+task+"/constraints/evaluate", tt.AccessToken, nil, nil)

	first, _ := readConstraintMarkers(t, con.ID)
	require.True(t, first.Valid, "fixture: the constraint must be satisfied")

	time.Sleep(1100 * time.Millisecond)
	doJSON(t, http.MethodPost, testServerURL+"/tasks/"+task+"/constraints/evaluate", tt.AccessToken, nil, nil)

	second, _ := readConstraintMarkers(t, con.ID)
	require.True(t, first.Time.Equal(second.Time),
		"re-evaluating a satisfied constraint moved satisfied_at from %s to %s", first.Time, second.Time)
}

// TestDeletedTaskEdgesLeaveCycleCheck pins that an edge to a deleted task
// no longer takes part in the cycle check.
func TestDeletedTaskEdgesLeaveCycleCheck(t *testing.T) {
	bootstrap(t)
	t.Parallel()
	tt := newTenant(t)

	a := createTask(t, tt.AccessToken, tt.ProjectPublicID, "Cycle A")
	b := createTask(t, tt.AccessToken, tt.ProjectPublicID, "Cycle B")
	c := createTask(t, tt.AccessToken, tt.ProjectPublicID, "Cycle C")
	doJSON(t, http.MethodPost, testServerURL+"/tasks/"+a+"/dependencies", tt.AccessToken,
		map[string]any{"toTaskId": c, "kind": "blocks"}, nil)
	doJSON(t, http.MethodPost, testServerURL+"/tasks/"+c+"/dependencies", tt.AccessToken,
		map[string]any{"toTaskId": b, "kind": "blocks"}, nil)

	status, _ := doJSONStatus(t, http.MethodDelete, testServerURL+"/tasks/"+c, tt.AccessToken, nil)
	require.Equal(t, http.StatusOK, status)

	doJSON(t, http.MethodPost, testServerURL+"/tasks/"+b+"/dependencies", tt.AccessToken,
		map[string]any{"toTaskId": a, "kind": "blocks"}, nil)
}

// TestInformationalKindsDoNotCloseCycles pins that only blocking kinds
// take part in cycle rejection.
func TestInformationalKindsDoNotCloseCycles(t *testing.T) {
	bootstrap(t)
	t.Parallel()
	tt := newTenant(t)

	a := createTask(t, tt.AccessToken, tt.ProjectPublicID, "Kind A")
	b := createTask(t, tt.AccessToken, tt.ProjectPublicID, "Kind B")
	doJSON(t, http.MethodPost, testServerURL+"/tasks/"+a+"/dependencies", tt.AccessToken,
		map[string]any{"toTaskId": b, "kind": "relates"}, nil)
	doJSON(t, http.MethodPost, testServerURL+"/tasks/"+b+"/dependencies", tt.AccessToken,
		map[string]any{"toTaskId": a, "kind": "blocks"}, nil)

	status, raw := doJSONStatus(t, http.MethodPost, testServerURL+"/tasks/"+a+"/dependencies", tt.AccessToken,
		map[string]any{"toTaskId": b, "kind": "blocks"})
	require.Equal(t, http.StatusUnprocessableEntity, status, string(raw))
	require.Equal(t, "WS.TASK.DEPENDENCY_CYCLE", problemType(t, raw))
}

// TestUnknownStateFilterMatchesNothing pins that a state filter with no
// recognised value never widens to the unfiltered list.
func TestUnknownStateFilterMatchesNothing(t *testing.T) {
	bootstrap(t)
	t.Parallel()
	tt := newTenant(t)

	createTask(t, tt.AccessToken, tt.ProjectPublicID, "Filtered out")
	page := fetchTaskListPage(t, tt.AccessToken,
		"projectId="+url.QueryEscape(tt.ProjectPublicID)+"&state=in_progress,blocked")
	require.Empty(t, page.Tasks)
	require.Zero(t, page.Total)
}
