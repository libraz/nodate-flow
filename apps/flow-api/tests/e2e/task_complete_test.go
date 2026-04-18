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
		ID          string  `json:"id"`
		CompletedAt *string `json:"completedAt"`
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
		DerivedState string  `json:"derivedState"`
		CompletedAt  *string `json:"completedAt"`
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
		DerivedState string  `json:"derivedState"`
		CompletedAt  *string `json:"completedAt"`
	}
	doJSON(t, http.MethodPost,
		testServerURL+"/tasks/"+task.ID+"/transitions",
		tt.AccessToken, map[string]any{"transition": "complete"}, &result)
	require.Equal(t, "done", result.DerivedState)
	require.NotNil(t, result.CompletedAt,
		"directly completed task must have completedAt")
}

// TestTaskReopenPreservesCompletedAt verifies that reopening a
// completed task does not clear the completedAt timestamp.
func TestTaskReopenPreservesCompletedAt(t *testing.T) {
	bootstrap(t)
	t.Parallel()
	tt := newTenant(t)

	var task struct {
		ID string `json:"id"`
	}
	doJSON(t, http.MethodPost, testServerURL+"/tasks", tt.AccessToken,
		map[string]any{"projectId": tt.ProjectPublicID, "title": "Reopen preserve"}, &task)

	// Complete the task.
	doJSON(t, http.MethodPost,
		testServerURL+"/tasks/"+task.ID+"/transitions",
		tt.AccessToken, map[string]any{"transition": "complete"}, nil)

	// Reopen.
	doJSON(t, http.MethodPost,
		testServerURL+"/tasks/"+task.ID+"/transitions",
		tt.AccessToken, map[string]any{"transition": "reopen"}, nil)

	// completedAt should still be set (not cleared on reopen).
	var reopened struct {
		DerivedState string  `json:"derivedState"`
		CompletedAt  *string `json:"completedAt"`
	}
	doJSON(t, http.MethodGet,
		testServerURL+"/tasks/"+task.ID,
		tt.AccessToken, nil, &reopened)
	require.Equal(t, "waiting", reopened.DerivedState)
	require.NotNil(t, reopened.CompletedAt,
		"completedAt must be preserved after reopen")
}
