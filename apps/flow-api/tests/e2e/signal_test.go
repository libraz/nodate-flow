package e2e

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestSignalLifecycle exercises POST /signals for a manual signal
// attached to a task. Webhook HMAC paths are covered by unit tests in
// internal/integrations/ and are intentionally skipped here.
//
// Asserts the ADR 0008 D1 contract that every signals row carries a
// non-empty subject_type: the legacy `taskId` parameter implicitly
// promotes the row to subject_type=task and surfaces the subjectId in
// the response so SDK consumers can render the new addressing shape
// without a follow-up GET.
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

	// Inject a manual signal attached to the task. `manual` is registered
	// in signal_kinds/user.yaml; an unknown kind would short-circuit with
	// WS.SIGNAL.KIND_UNKNOWN before the insert.
	var signal struct {
		ID          string `json:"id"`
		Source      string `json:"source"`
		Kind        string `json:"kind"`
		TaskID      string `json:"taskId"`
		SubjectType string `json:"subjectType"`
		SubjectID   string `json:"subjectId"`
	}
	doJSON(t, http.MethodPost, testServerURL+"/signals", tt.AccessToken, map[string]any{
		"workspaceId": tt.WorkspacePublicID,
		"source":      "manual",
		"kind":        "manual",
		"taskId":      task.ID,
		"payload":     map[string]any{"hello": "world"},
	}, &signal)
	require.NotEmpty(t, signal.ID)
	require.Equal(t, "manual", signal.Source)
	require.Equal(t, "manual", signal.Kind)
	require.Equal(t, task.ID, signal.TaskID)
	// Legacy taskId implicitly satisfies the new (subjectType, subjectId)
	// shape: the handler upgrades subject_type from the kind's default
	// (user) to task because a concrete taskId was supplied.
	require.Equal(t, "task", signal.SubjectType)
}
