package e2e

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestRESTApplyStepsRecordsEachChild holds REST apply-steps to the shape
// MCP apply_steps records in: every child gets its own task.created event
// and its own task.apply_steps audit row, both written in the transaction
// that creates it.
func TestRESTApplyStepsRecordsEachChild(t *testing.T) {
	bootstrap(t)
	t.Parallel()

	tt := newTenant(t)
	var parent struct {
		ID string `json:"id"`
	}
	doJSON(t, http.MethodPost, testServerURL+"/tasks", tt.AccessToken, map[string]any{
		"projectId": tt.ProjectPublicID,
		"title":     "parent for apply-steps records",
	}, &parent)
	require.NotEmpty(t, parent.ID)

	var applied struct {
		Created []string `json:"created"`
	}
	doJSON(t, http.MethodPost, testServerURL+"/tasks/"+parent.ID+"/apply-steps", tt.AccessToken,
		map[string]any{"steps": []map[string]any{{"title": "records one"}, {"title": "records two"}}}, &applied)
	require.Len(t, applied.Created, 2)

	for _, child := range applied.Created {
		require.Equal(t, 1, taskEventCount(t, child, "task.created"), "child %s: one creation event", child)
		require.Equal(t, 1, auditCount(t, "task.apply_steps", child), "child %s: one audit row naming it", child)
	}
	require.Equal(t, 0, auditCount(t, "task.apply_steps", parent.ID), "the parent was not created by apply-steps")
}
