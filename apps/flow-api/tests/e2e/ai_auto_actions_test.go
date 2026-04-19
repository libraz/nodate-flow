package e2e

import (
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// TestAiAutoActions creates two tasks — one with a past due date and
// one without — and hits the workspace auto-actions endpoint.
// The overdue task must surface as an "escalate_overdue" action; the
// other task, being fresh and without a due date, must not produce an
// action.
func TestAiAutoActions(t *testing.T) {
	bootstrap(t)
	t.Parallel()

	tt := newTenant(t)

	past := time.Now().UTC().AddDate(0, 0, -2).Format("2006-01-02")

	var overdue struct {
		ID string `json:"id"`
	}
	doJSON(t, http.MethodPost, testServerURL+"/tasks", tt.AccessToken, map[string]any{
		"projectId":   tt.ProjectPublicID,
		"title":       "Escalate the overdue rollout",
		"description": "Past due — should produce an escalate action.",
		"priority":    2,
		"dueOn":       past,
	}, &overdue)
	require.NotEmpty(t, overdue.ID)

	var fresh struct {
		ID string `json:"id"`
	}
	doJSON(t, http.MethodPost, testServerURL+"/tasks", tt.AccessToken, map[string]any{
		"projectId": tt.ProjectPublicID,
		"title":     "A fresh calm task",
		"priority":  1,
	}, &fresh)

	var out struct {
		Total   int `json:"total"`
		Actions []struct {
			TaskID     string  `json:"taskId"`
			Kind       string  `json:"kind"`
			Confidence float32 `json:"confidence"`
			Reason     string  `json:"reason"`
		} `json:"actions"`
	}
	doJSON(t, http.MethodGet,
		testServerURL+"/workspaces/"+tt.WorkspacePublicID+"/ai/auto-actions",
		tt.AccessToken, nil, &out)

	require.GreaterOrEqual(t, out.Total, 2)
	require.NotEmpty(t, out.Actions)
	require.NotNil(t, out.Actions)

	var found bool
	for _, a := range out.Actions {
		if a.TaskID == overdue.ID {
			require.Equal(t, "escalate_overdue", a.Kind)
			require.Greater(t, a.Confidence, float32(0))
			require.NotEmpty(t, a.Reason)
			found = true
		}
		require.NotEqual(t, fresh.ID, a.TaskID, "fresh task should not produce an action")
	}
	require.True(t, found, "overdue task should appear in auto-actions")
}
