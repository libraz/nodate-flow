package e2e

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/libraz/nodate-flow/apps/flow-api/internal/eventbus"
)

// TestConstraintAddRemoveRecordOnceAndRefuseWithConstraintCode holds the
// constraint add and remove paths to one timeline row and one audit row
// each, and a remove of a constraint that is not there to the catalog's
// constraint code rather than a task-not-found that names the wrong thing.
func TestConstraintAddRemoveRecordOnceAndRefuseWithConstraintCode(t *testing.T) {
	bootstrap(t)
	t.Parallel()
	tt := newTenant(t)

	taskID := createTask(t, tt.AccessToken, tt.ProjectPublicID, "Constraint record")
	var con struct {
		ID string `json:"id"`
	}
	doJSON(t, http.MethodPost, testServerURL+"/tasks/"+taskID+"/constraints", tt.AccessToken,
		map[string]any{
			"kind":       "deadline",
			"expression": `{"op":"time.due_before","arg":"2030-01-01"}`,
		}, &con)
	require.NotEmpty(t, con.ID)
	require.Equal(t, 1, taskEventCount(t, taskID, string(eventbus.TaskConstraintAdded)))
	require.Equal(t, 1, auditCount(t, "task.constraint.add", con.ID))

	url := testServerURL + "/tasks/" + taskID + "/constraints/" + con.ID
	doJSON(t, http.MethodDelete, url, tt.AccessToken, nil, nil)
	require.Equal(t, 1, taskEventCount(t, taskID, string(eventbus.TaskConstraintRemoved)))
	require.Equal(t, 1, auditCount(t, "task.constraint.remove", con.ID))

	status, raw := doJSONStatus(t, http.MethodDelete, url, tt.AccessToken, nil)
	require.Equal(t, http.StatusNotFound, status, "body=%s", string(raw))
	require.Equal(t, "WS.TASK_CONSTRAINT.NOT_FOUND", problemType(t, raw))
	require.Equal(t, 1, taskEventCount(t, taskID, string(eventbus.TaskConstraintRemoved)),
		"a refused remove must not record a second removal")
	require.Equal(t, 1, auditCount(t, "task.constraint.remove", con.ID))
}
