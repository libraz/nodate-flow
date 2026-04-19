package e2e

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestAiStateSuggestionsEmpty exercises the workspace-wide state inference
// batch endpoint. A freshly created task is not idle, so the
// rule engine yields no proposals — the endpoint must return a Total of
// at least 1 with an empty Suggestions slice (never nil).
func TestAiStateSuggestionsEmpty(t *testing.T) {
	bootstrap(t)
	t.Parallel()

	tt := newTenant(t)

	var created struct {
		ID string `json:"id"`
	}
	doJSON(t, http.MethodPost, testServerURL+"/tasks", tt.AccessToken, map[string]any{
		"projectId":   tt.ProjectPublicID,
		"title":       "Refresh dashboard widgets",
		"description": "Bring the v1 widgets up to the new design tokens.",
		"priority":    1,
	}, &created)
	require.NotEmpty(t, created.ID)

	var out struct {
		Total       int `json:"total"`
		Suggestions []struct {
			TaskID     string  `json:"taskId"`
			Title      string  `json:"title"`
			State      string  `json:"state"`
			Transition string  `json:"transition"`
			Confidence float32 `json:"confidence"`
			Reason     string  `json:"reason"`
		} `json:"suggestions"`
	}
	doJSON(t, http.MethodGet,
		testServerURL+"/workspaces/"+tt.WorkspacePublicID+"/ai/state-suggestions",
		tt.AccessToken, nil, &out)

	require.GreaterOrEqual(t, out.Total, 1, "at least the just-created task should be evaluated")
	require.NotNil(t, out.Suggestions, "suggestions slice must not be nil even when empty")
	require.Empty(t, out.Suggestions, "fresh tasks should not yield proposals")
}
