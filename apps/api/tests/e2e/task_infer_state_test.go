package e2e

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestTaskInferState creates a fresh task and hits GET
// /tasks/{id}/infer-state. A just-created task is in "open" state with
// no idle time, so the deterministic rule engine (2.AI-1) must return a
// nil proposal. The endpoint must still echo the task id + state so
// the glass dock can distinguish "no suggestion" from "lookup failed".
func TestTaskInferState(t *testing.T) {
	bootstrap(t)
	t.Parallel()

	tt := newTenant(t)

	var created struct {
		ID string `json:"id"`
	}
	doJSON(t, http.MethodPost, testServerURL+"/tasks", tt.AccessToken, map[string]any{
		"projectId":   tt.ProjectPublicID,
		"title":       "Design onboarding flow",
		"description": "Draft the first-run experience.",
		"priority":    1,
	}, &created)
	require.NotEmpty(t, created.ID)

	var out struct {
		TaskID   string `json:"taskId"`
		State    string `json:"state"`
		Proposal *struct {
			Transition string  `json:"transition"`
			Confidence float32 `json:"confidence"`
			Reason     string  `json:"reason"`
		} `json:"proposal"`
	}
	doJSON(t, http.MethodGet,
		testServerURL+"/tasks/"+created.ID+"/infer-state",
		tt.AccessToken, nil, &out)

	require.Equal(t, created.ID, out.TaskID)
	require.Equal(t, "open", out.State)
	require.Nil(t, out.Proposal, "fresh task should not yield a proposal")
}
