package e2e

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestTaskCompleteSetCompletedAt verifies that completing a task
// populates the completedAt timestamp.
func TestTaskCompleteSetCompletedAt(t *testing.T) {
	bootstrap(t)
	t.Parallel()
	tt := newTenant(t)

	var task struct {
		ID          string `json:"id"`
		CompletedAt *int64 `json:"completedAt"`
	}
	doJSON(t, http.MethodPost, testServerURL+"/tasks", tt.AccessToken,
		map[string]any{"projectId": tt.ProjectPublicID, "title": "Complete test"}, &task)
	require.Nil(t, task.CompletedAt, "new task must not have completedAt")

	// open → waiting → review → done
	for _, tr := range []string{"start", "submit", "complete"} {
		doJSON(t, http.MethodPost,
			testServerURL+"/tasks/"+task.ID+"/transitions",
			tt.AccessToken, map[string]any{"transition": tr}, nil)
	}

	// Verify completedAt is now set.
	var done struct {
		DerivedState string `json:"derivedState"`
		CompletedAt  *int64 `json:"completedAt"`
	}
	doJSON(t, http.MethodGet,
		testServerURL+"/tasks/"+task.ID,
		tt.AccessToken, nil, &done)
	require.Equal(t, "done", done.DerivedState)
	require.NotNil(t, done.CompletedAt,
		"completed task must have completedAt timestamp")
}

// TestTaskDirectCompleteFromOpen verifies that using the
// "complete" transition directly from open state works and
// sets completedAt.
func TestTaskDirectCompleteFromOpen(t *testing.T) {
	bootstrap(t)
	t.Parallel()
	tt := newTenant(t)

	var task struct {
		ID string `json:"id"`
	}
	doJSON(t, http.MethodPost, testServerURL+"/tasks", tt.AccessToken,
		map[string]any{"projectId": tt.ProjectPublicID, "title": "Direct complete"}, &task)

	var result struct {
		DerivedState string `json:"derivedState"`
		CompletedAt  *int64 `json:"completedAt"`
	}
	doJSON(t, http.MethodPost,
		testServerURL+"/tasks/"+task.ID+"/transitions",
		tt.AccessToken, map[string]any{"transition": "complete"}, &result)
	require.Equal(t, "done", result.DerivedState)
	require.NotNil(t, result.CompletedAt,
		"directly completed task must have completedAt")
}

// TestTaskReopenClearsCompletedAt verifies that reopening a completed
// task clears the completedAt timestamp, so a non-done task never
// carries a stale completion time (H-4 / P2-2).
func TestTaskReopenClearsCompletedAt(t *testing.T) {
	bootstrap(t)
	t.Parallel()
	tt := newTenant(t)

	var task struct {
		ID string `json:"id"`
	}
	doJSON(t, http.MethodPost, testServerURL+"/tasks", tt.AccessToken,
		map[string]any{"projectId": tt.ProjectPublicID, "title": "Reopen clears"}, &task)

	// Complete the task.
	doJSON(t, http.MethodPost,
		testServerURL+"/tasks/"+task.ID+"/transitions",
		tt.AccessToken, map[string]any{"transition": "complete"}, nil)

	// Reopen.
	doJSON(t, http.MethodPost,
		testServerURL+"/tasks/"+task.ID+"/transitions",
		tt.AccessToken, map[string]any{"transition": "reopen"}, nil)

	// completedAt must be cleared once the task is no longer done.
	var reopened struct {
		DerivedState string `json:"derivedState"`
		CompletedAt  *int64 `json:"completedAt"`
	}
	doJSON(t, http.MethodGet,
		testServerURL+"/tasks/"+task.ID,
		tt.AccessToken, nil, &reopened)
	require.Equal(t, "waiting", reopened.DerivedState)
	require.Nil(t, reopened.CompletedAt,
		"completedAt must be cleared after reopen")
}
