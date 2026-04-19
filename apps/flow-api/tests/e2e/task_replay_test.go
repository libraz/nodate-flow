package e2e

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestTaskReplayEquivalence drives a task through several transitions and
// asserts that GET /tasks/{id}/replay recomputes the same derived_state
// as the one stored on the row (replay equivalence).
func TestTaskReplayEquivalence(t *testing.T) {
	bootstrap(t)
	t.Parallel()

	tt := newTenant(t)

	var task struct {
		ID           string `json:"id"`
		DerivedState string `json:"derivedState"`
	}
	doJSON(t, http.MethodPost, testServerURL+"/tasks", tt.AccessToken, map[string]any{
		"projectId": tt.ProjectPublicID,
		"title":     "Replay target",
	}, &task)
	require.NotEmpty(t, task.ID)

	// open -> waiting -> review -> done
	for _, name := range []string{"start", "submit", "complete"} {
		var ack struct {
			DerivedState string `json:"derivedState"`
		}
		doJSON(t, http.MethodPost,
			testServerURL+"/tasks/"+task.ID+"/transitions",
			tt.AccessToken, map[string]any{"transition": name}, &ack)
	}

	var rep struct {
		DerivedState string `json:"derivedState"`
		Stored       string `json:"stored"`
		Equivalent   bool   `json:"equivalent"`
	}
	doJSON(t, http.MethodGet,
		testServerURL+"/tasks/"+task.ID+"/replay",
		tt.AccessToken, nil, &rep)
	require.Equal(t, "done", rep.Stored)
	require.Equal(t, "done", rep.DerivedState)
	require.True(t, rep.Equivalent)
}
