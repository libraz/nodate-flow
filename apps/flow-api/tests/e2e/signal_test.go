package e2e

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestSignalLifecycle exercises POST /signals for a manual signal
// attached to a task. Webhook HMAC paths are covered by unit tests in
// internal/integrations/ and are intentionally skipped here.
func TestSignalLifecycle(t *testing.T) {
	bootstrap(t)
	t.Parallel()

	tt := newTenant(t)

	// Create a task the signal will attach to.
	var task struct {
		ID string `json:"id"`
	}
	doJSON(t, http.MethodPost, testServerURL+"/tasks", tt.AccessToken, map[string]any{
		"projectId": tt.ProjectPublicID,
		"title":     "Signal target",
	}, &task)
	require.NotEmpty(t, task.ID)

	// Inject a manual signal attached to the task.
	var signal struct {
		ID     string `json:"id"`
		Source string `json:"source"`
		Kind   string `json:"kind"`
		TaskID string `json:"taskId"`
	}
	doJSON(t, http.MethodPost, testServerURL+"/signals", tt.AccessToken, map[string]any{
		"workspaceId": tt.WorkspacePublicID,
		"source":      "manual",
		"kind":        "test.signal",
		"taskId":      task.ID,
		"payload":     map[string]any{"hello": "world"},
	}, &signal)
	require.NotEmpty(t, signal.ID)
	require.Equal(t, "manual", signal.Source)
	require.Equal(t, "test.signal", signal.Kind)
	require.Equal(t, task.ID, signal.TaskID)
}
