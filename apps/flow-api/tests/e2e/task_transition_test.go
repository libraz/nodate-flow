package e2e

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestTaskStateTransitionHappyPath walks a task through the full
// state machine: open → waiting → review → done, verifying
// derivedState at each step.
func TestTaskStateTransitionHappyPath(t *testing.T) {
	bootstrap(t)
	t.Parallel()
	tt := newTenant(t)

	var task struct {
		ID           string `json:"id"`
		DerivedState string `json:"derivedState"`
	}
	doJSON(t, http.MethodPost, testServerURL+"/tasks", tt.AccessToken,
		map[string]any{"projectId": tt.ProjectPublicID, "title": "State machine task"}, &task)
	require.Equal(t, "open", task.DerivedState)

	transitions := []struct {
		action string
		expect string
	}{
		{"start", "waiting"},
		{"submit", "review"},
		{"complete", "done"},
	}

	for _, tr := range transitions {
		var result struct {
			DerivedState string `json:"derivedState"`
		}
		doJSON(t, http.MethodPost,
			testServerURL+"/tasks/"+task.ID+"/transitions",
			tt.AccessToken, map[string]any{"transition": tr.action}, &result)
		require.Equal(t, tr.expect, result.DerivedState,
			"after %q transition", tr.action)
	}
}

// TestTaskReopenFromDone verifies that a completed task can be
// reopened back to "waiting".
func TestTaskReopenFromDone(t *testing.T) {
	bootstrap(t)
	t.Parallel()
	tt := newTenant(t)

	var task struct {
		ID string `json:"id"`
	}
	doJSON(t, http.MethodPost, testServerURL+"/tasks", tt.AccessToken,
		map[string]any{"projectId": tt.ProjectPublicID, "title": "Reopen test"}, &task)

	// open → waiting → review → done
	for _, tr := range []string{"start", "submit", "complete"} {
		doJSON(t, http.MethodPost,
			testServerURL+"/tasks/"+task.ID+"/transitions",
			tt.AccessToken, map[string]any{"transition": tr}, nil)
	}

	// done → waiting (reopen)
	var result struct {
		DerivedState string `json:"derivedState"`
	}
	doJSON(t, http.MethodPost,
		testServerURL+"/tasks/"+task.ID+"/transitions",
		tt.AccessToken, map[string]any{"transition": "reopen"}, &result)
	require.Equal(t, "waiting", result.DerivedState)
}

// TestTaskCancelFromOpen verifies that cancelling an open task works.
func TestTaskCancelFromOpen(t *testing.T) {
	bootstrap(t)
	t.Parallel()
	tt := newTenant(t)

	var task struct {
		ID string `json:"id"`
	}
	doJSON(t, http.MethodPost, testServerURL+"/tasks", tt.AccessToken,
		map[string]any{"projectId": tt.ProjectPublicID, "title": "Cancel test"}, &task)

	var result struct {
		DerivedState string `json:"derivedState"`
	}
	doJSON(t, http.MethodPost,
		testServerURL+"/tasks/"+task.ID+"/transitions",
		tt.AccessToken, map[string]any{"transition": "cancel"}, &result)
	require.Equal(t, "cancelled", result.DerivedState)
}

// TestTaskReopenFromCancelled verifies that a cancelled task
// reopens back to "open".
func TestTaskReopenFromCancelled(t *testing.T) {
	bootstrap(t)
	t.Parallel()
	tt := newTenant(t)

	var task struct {
		ID string `json:"id"`
	}
	doJSON(t, http.MethodPost, testServerURL+"/tasks", tt.AccessToken,
		map[string]any{"projectId": tt.ProjectPublicID, "title": "Reopen cancelled"}, &task)

	doJSON(t, http.MethodPost,
		testServerURL+"/tasks/"+task.ID+"/transitions",
		tt.AccessToken, map[string]any{"transition": "cancel"}, nil)

	var result struct {
		DerivedState string `json:"derivedState"`
	}
	doJSON(t, http.MethodPost,
		testServerURL+"/tasks/"+task.ID+"/transitions",
		tt.AccessToken, map[string]any{"transition": "reopen"}, &result)
	require.Equal(t, "open", result.DerivedState)
}

// TestTaskInvalidTransitionRejected verifies that the API rejects
// transitions not allowed by the state machine (e.g., open → review).
func TestTaskInvalidTransitionRejected(t *testing.T) {
	bootstrap(t)
	t.Parallel()
	tt := newTenant(t)

	var task struct {
		ID string `json:"id"`
	}
	doJSON(t, http.MethodPost, testServerURL+"/tasks", tt.AccessToken,
		map[string]any{"projectId": tt.ProjectPublicID, "title": "Invalid transition"}, &task)

	// "submit" is only valid from "waiting", not from "open".
	status, _ := doJSONStatus(t, http.MethodPost,
		testServerURL+"/tasks/"+task.ID+"/transitions",
		tt.AccessToken, map[string]any{"transition": "submit"})
	require.GreaterOrEqual(t, status, 400,
		"submit from open must be rejected")
	require.Less(t, status, 500,
		"invalid transition must be a client error, not 5xx")
}

// TestTaskUnknownTransitionRejected verifies that a made-up
// transition name is rejected.
func TestTaskUnknownTransitionRejected(t *testing.T) {
	bootstrap(t)
	t.Parallel()
	tt := newTenant(t)

	var task struct {
		ID string `json:"id"`
	}
	doJSON(t, http.MethodPost, testServerURL+"/tasks", tt.AccessToken,
		map[string]any{"projectId": tt.ProjectPublicID, "title": "Unknown transition"}, &task)

	status, _ := doJSONStatus(t, http.MethodPost,
		testServerURL+"/tasks/"+task.ID+"/transitions",
		tt.AccessToken, map[string]any{"transition": "teleport"})
	require.GreaterOrEqual(t, status, 400,
		"unknown transition must be rejected")
	require.Less(t, status, 500)
}

// TestTaskTransitionWithReason verifies that a transition can
// include an optional reason string.
func TestTaskTransitionWithReason(t *testing.T) {
	bootstrap(t)
	t.Parallel()
	tt := newTenant(t)

	var task struct {
		ID string `json:"id"`
	}
	doJSON(t, http.MethodPost, testServerURL+"/tasks", tt.AccessToken,
		map[string]any{"projectId": tt.ProjectPublicID, "title": "Reason test"}, &task)

	var result struct {
		DerivedState string `json:"derivedState"`
	}
	doJSON(t, http.MethodPost,
		testServerURL+"/tasks/"+task.ID+"/transitions",
		tt.AccessToken, map[string]any{
			"transition": "start",
			"reason":     "Beginning implementation",
		}, &result)
	require.Equal(t, "waiting", result.DerivedState)
}
