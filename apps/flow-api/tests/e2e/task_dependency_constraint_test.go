package e2e

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"
)

// createTask is a small helper that creates a task in the tenant's
// default project and returns its public id.
func createTask(t *testing.T, accessToken, projectID, title string) string {
	t.Helper()
	var task struct {
		ID string `json:"id"`
	}
	doJSON(t, http.MethodPost, testServerURL+"/tasks", accessToken,
		map[string]any{"projectId": projectID, "title": title}, &task)
	require.NotEmpty(t, task.ID)
	return task.ID
}

// TestDependencyCycleRejected verifies the documented DAG contract: a
// direct (A->B then B->A) and a transitive (A->B->C then C->A) cycle are
// both rejected with WS.TASK.DEPENDENCY_CYCLE, while a parallel same-
// direction edge stays a DAG and is accepted (P2-1).
func TestDependencyCycleRejected(t *testing.T) {
	bootstrap(t)
	t.Parallel()
	tt := newTenant(t)

	a := createTask(t, tt.AccessToken, tt.ProjectPublicID, "Dep A")
	b := createTask(t, tt.AccessToken, tt.ProjectPublicID, "Dep B")
	c := createTask(t, tt.AccessToken, tt.ProjectPublicID, "Dep C")

	// A -> B is fine on an empty graph.
	doJSON(t, http.MethodPost, testServerURL+"/tasks/"+a+"/dependencies", tt.AccessToken,
		map[string]any{"toTaskId": b, "kind": "blocks"}, nil)

	// B -> A would close a direct cycle and must be rejected.
	status, raw := doJSONStatus(t, http.MethodPost,
		testServerURL+"/tasks/"+b+"/dependencies", tt.AccessToken,
		map[string]any{"toTaskId": a, "kind": "blocks"})
	require.Equal(t, http.StatusUnprocessableEntity, status,
		"direct cycle must be rejected: %s", string(raw))
	require.Equal(t, "WS.TASK.DEPENDENCY_CYCLE", problemType(t, raw))

	// B -> C extends the chain A -> B -> C (still a DAG).
	doJSON(t, http.MethodPost, testServerURL+"/tasks/"+b+"/dependencies", tt.AccessToken,
		map[string]any{"toTaskId": c, "kind": "blocks"}, nil)

	// C -> A would close a transitive cycle A -> B -> C -> A.
	status, raw = doJSONStatus(t, http.MethodPost,
		testServerURL+"/tasks/"+c+"/dependencies", tt.AccessToken,
		map[string]any{"toTaskId": a, "kind": "blocks"})
	require.Equal(t, http.StatusUnprocessableEntity, status,
		"transitive cycle must be rejected: %s", string(raw))
	require.Equal(t, "WS.TASK.DEPENDENCY_CYCLE", problemType(t, raw))
}

// TestReopenClearsCompletedAt verifies completed_at is cleared when a
// task leaves the done state via reopen, so non-done tasks never carry a
// stale completion timestamp (P2-2).
func TestReopenClearsCompletedAt(t *testing.T) {
	bootstrap(t)
	t.Parallel()
	tt := newTenant(t)

	taskID := createTask(t, tt.AccessToken, tt.ProjectPublicID, "Reopen completedAt")

	// open -> waiting -> review -> done
	for _, tr := range []string{"start", "submit", "complete"} {
		doJSON(t, http.MethodPost, testServerURL+"/tasks/"+taskID+"/transitions",
			tt.AccessToken, map[string]any{"transition": tr}, nil)
	}

	var done struct {
		DerivedState string `json:"derivedState"`
		CompletedAt  *int64 `json:"completedAt"`
	}
	doJSON(t, http.MethodGet, testServerURL+"/tasks/"+taskID, tt.AccessToken, nil, &done)
	require.Equal(t, "done", done.DerivedState)
	require.NotNil(t, done.CompletedAt, "completedAt must be set while done")

	// done -> waiting (reopen) must clear completedAt.
	doJSON(t, http.MethodPost, testServerURL+"/tasks/"+taskID+"/transitions",
		tt.AccessToken, map[string]any{"transition": "reopen"}, nil)

	var reopened struct {
		DerivedState string `json:"derivedState"`
		CompletedAt  *int64 `json:"completedAt"`
	}
	doJSON(t, http.MethodGet, testServerURL+"/tasks/"+taskID, tt.AccessToken, nil, &reopened)
	require.Equal(t, "waiting", reopened.DerivedState)
	require.Nil(t, reopened.CompletedAt, "completedAt must be cleared after reopen")
}

// TestDependencyDeleteScopedToPathTask verifies that a dependency can
// only be deleted through the task path that owns it. Deleting via a
// sibling task's path returns NOT_FOUND, and a repeated delete on the
// owning path also returns NOT_FOUND so a no-op is distinguishable from
// a real delete (P2-3).
func TestDependencyDeleteScopedToPathTask(t *testing.T) {
	bootstrap(t)
	t.Parallel()
	tt := newTenant(t)

	a := createTask(t, tt.AccessToken, tt.ProjectPublicID, "Owner A")
	b := createTask(t, tt.AccessToken, tt.ProjectPublicID, "Target B")
	sibling := createTask(t, tt.AccessToken, tt.ProjectPublicID, "Sibling S")

	// Edge belongs to task A (from_task = A).
	var dep struct {
		ID string `json:"id"`
	}
	doJSON(t, http.MethodPost, testServerURL+"/tasks/"+a+"/dependencies", tt.AccessToken,
		map[string]any{"toTaskId": b, "kind": "blocks"}, &dep)
	require.NotEmpty(t, dep.ID)

	// Deleting A's edge through the sibling's path must NOT touch it.
	status, raw := doJSONStatus(t, http.MethodDelete,
		testServerURL+"/tasks/"+sibling+"/dependencies/"+dep.ID, tt.AccessToken, nil)
	require.Equal(t, http.StatusNotFound, status,
		"sibling path must not delete another task's edge: %s", string(raw))

	// The edge still exists on A's path; delete it for real.
	doJSON(t, http.MethodDelete,
		testServerURL+"/tasks/"+a+"/dependencies/"+dep.ID, tt.AccessToken, nil, nil)

	// A second delete is a no-op and must report NOT_FOUND.
	status, raw = doJSONStatus(t, http.MethodDelete,
		testServerURL+"/tasks/"+a+"/dependencies/"+dep.ID, tt.AccessToken, nil)
	require.Equal(t, http.StatusNotFound, status,
		"repeated delete must report NOT_FOUND, not a silent ok: %s", string(raw))
}

// TestConstraintDeleteScopedToPathTask mirrors the dependency case for
// task constraints: a sibling task's path cannot delete a constraint and
// a repeated delete reports NOT_FOUND (P2-3).
func TestConstraintDeleteScopedToPathTask(t *testing.T) {
	bootstrap(t)
	t.Parallel()
	tt := newTenant(t)

	owner := createTask(t, tt.AccessToken, tt.ProjectPublicID, "Constraint owner")
	sibling := createTask(t, tt.AccessToken, tt.ProjectPublicID, "Constraint sibling")

	var con struct {
		ID string `json:"id"`
	}
	doJSON(t, http.MethodPost, testServerURL+"/tasks/"+owner+"/constraints", tt.AccessToken,
		map[string]any{
			"kind":       "deadline",
			"expression": `{"op":"time.due_before","arg":"2030-01-01"}`,
		}, &con)
	require.NotEmpty(t, con.ID)

	status, raw := doJSONStatus(t, http.MethodDelete,
		testServerURL+"/tasks/"+sibling+"/constraints/"+con.ID, tt.AccessToken, nil)
	require.Equal(t, http.StatusNotFound, status,
		"sibling path must not delete another task's constraint: %s", string(raw))

	doJSON(t, http.MethodDelete,
		testServerURL+"/tasks/"+owner+"/constraints/"+con.ID, tt.AccessToken, nil, nil)

	status, raw = doJSONStatus(t, http.MethodDelete,
		testServerURL+"/tasks/"+owner+"/constraints/"+con.ID, tt.AccessToken, nil)
	require.Equal(t, http.StatusNotFound, status,
		"repeated constraint delete must report NOT_FOUND: %s", string(raw))
}

// TestAddConstraintRejectsUnparseableExpression verifies an invalid DSL
// expression is rejected at add time with a CONSTRAINT.PARSE.* code
// instead of saving with HTTP 200 and being silently inert (P2-4). A
// well-formed expression on the same task still succeeds.
func TestAddConstraintRejectsUnparseableExpression(t *testing.T) {
	bootstrap(t)
	t.Parallel()
	tt := newTenant(t)

	taskID := createTask(t, tt.AccessToken, tt.ProjectPublicID, "Constraint parse")

	// Unknown operator -> UNSUPPORTED_OPERATOR.
	status, raw := doJSONStatus(t, http.MethodPost,
		testServerURL+"/tasks/"+taskID+"/constraints", tt.AccessToken,
		map[string]any{"kind": "custom", "expression": `{"op":"totally.bogus"}`})
	require.Equal(t, http.StatusUnprocessableEntity, status,
		"unparseable constraint must be rejected: %s", string(raw))
	require.Equal(t, "CONSTRAINT.PARSE.UNSUPPORTED_OPERATOR", problemType(t, raw))

	// Malformed JSON -> INVALID_JSON.
	status, raw = doJSONStatus(t, http.MethodPost,
		testServerURL+"/tasks/"+taskID+"/constraints", tt.AccessToken,
		map[string]any{"kind": "custom", "expression": `{not valid json`})
	require.Equal(t, http.StatusUnprocessableEntity, status,
		"malformed JSON constraint must be rejected: %s", string(raw))
	require.Equal(t, "CONSTRAINT.PARSE.INVALID_JSON", problemType(t, raw))

	// A valid expression still persists.
	var con struct {
		ID string `json:"id"`
	}
	doJSON(t, http.MethodPost, testServerURL+"/tasks/"+taskID+"/constraints", tt.AccessToken,
		map[string]any{
			"kind":       "deadline",
			"expression": `{"op":"time.due_before","arg":"2030-01-01"}`,
		}, &con)
	require.NotEmpty(t, con.ID, "valid constraint must still be accepted")
}
