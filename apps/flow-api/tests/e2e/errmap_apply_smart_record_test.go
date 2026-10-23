package e2e

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestRESTApplySmartRecordsEventAndAuditPerTask holds apply-smart to the
// shared recorder: every task it creates has exactly one creation event
// and exactly one audit row, so neither table answers for a task the
// other has never heard of.
func TestRESTApplySmartRecordsEventAndAuditPerTask(t *testing.T) {
	bootstrap(t)
	t.Parallel()
	tt := newTenant(t)

	var applied struct {
		TaskID     string   `json:"taskId"`
		SubtaskIDs []string `json:"subtaskIds"`
	}
	doJSON(t, http.MethodPost,
		testServerURL+"/workspaces/"+tt.WorkspacePublicID+"/tasks/apply-smart",
		tt.AccessToken, map[string]any{
			"projectId": tt.ProjectPublicID,
			"title":     "apply-smart parent, recorded",
			"priority":  0,
			"subtasks": []map[string]any{
				{"title": "apply-smart child one, recorded", "priority": 0},
				{"title": "apply-smart child two, recorded", "priority": 0},
			},
		}, &applied)
	require.NotEmpty(t, applied.TaskID)
	require.Len(t, applied.SubtaskIDs, 2)

	require.Len(t, taskCreatedEvents(t, applied.TaskID), 1)
	require.Equal(t, 1, auditCount(t, "task.smart_create", applied.TaskID))
	for _, id := range applied.SubtaskIDs {
		require.Len(t, taskCreatedEvents(t, id), 1)
		require.Equal(t, 1, auditCount(t, "task.create", id))
	}
}
