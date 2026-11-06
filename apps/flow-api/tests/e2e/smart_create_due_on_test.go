package e2e

import (
	"database/sql"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestApplySmartPreservesDueOnAndExistingOutputs covers the full REST
// apply-smart write with a due date, parent assignee, and assigned subtask.
// The date must be persisted and returned through the ordinary task read,
// while the existing task.created event and parent/subtask behavior remain
// unchanged.
func TestApplySmartPreservesDueOnAndExistingOutputs(t *testing.T) {
	bootstrap(t)
	t.Parallel()
	tt := newTenant(t)

	const (
		dueOn       = "2032-04-12"
		parentTitle = "apply-smart parent with a due date"
		childTitle  = "assigned apply-smart child"
	)
	var applied struct {
		TaskID     string   `json:"taskId"`
		SubtaskIDs []string `json:"subtaskIds"`
	}
	status, raw := doJSONStatus(t, http.MethodPost,
		testServerURL+"/workspaces/"+tt.WorkspacePublicID+"/tasks/apply-smart",
		tt.AccessToken, map[string]any{
			"projectId":       tt.ProjectPublicID,
			"title":           parentTitle,
			"priority":        1,
			"dueOn":           dueOn,
			"assigneeUserIds": []string{tt.UserPublicID},
			"subtasks": []map[string]any{
				{"title": childTitle, "priority": 0, "assigneeUserId": tt.UserPublicID},
				{"title": "unassigned apply-smart child", "priority": 2},
			},
		},
	)
	require.Equalf(t, http.StatusOK, status, "apply-smart body=%s", string(raw))
	require.NoError(t, json.Unmarshal(raw, &applied))
	require.NotEmpty(t, applied.TaskID)
	require.Len(t, applied.SubtaskIDs, 2)

	var task struct {
		ID    string `json:"id"`
		Title string `json:"title"`
		DueOn string `json:"dueOn"`
	}
	doJSON(t, http.MethodGet, testServerURL+"/tasks/"+applied.TaskID, tt.AccessToken, nil, &task)
	require.Equal(t, applied.TaskID, task.ID)
	require.Equal(t, parentTitle, task.Title)
	require.Equal(t, dueOn, task.DueOn)

	var storedDueOn sql.NullString
	require.NoError(t, testDB.QueryRow(
		`SELECT DATE_FORMAT(due_on, '%Y-%m-%d')
		   FROM tasks WHERE public_id = UUID_TO_BIN(?, 0)`, applied.TaskID,
	).Scan(&storedDueOn))
	require.True(t, storedDueOn.Valid)
	require.Equal(t, dueOn, storedDueOn.String)

	var actors struct {
		Actors []struct {
			UserID string `json:"userId"`
			Role   string `json:"role"`
		} `json:"actors"`
	}
	doJSON(t, http.MethodGet, testServerURL+"/tasks/"+applied.TaskID+"/actors",
		tt.AccessToken, nil, &actors)
	require.Len(t, actors.Actors, 1)
	require.Equal(t, tt.UserPublicID, actors.Actors[0].UserID)
	require.Equal(t, "assignee", actors.Actors[0].Role)

	var childActors struct {
		Actors []struct {
			UserID string `json:"userId"`
			Role   string `json:"role"`
		} `json:"actors"`
	}
	doJSON(t, http.MethodGet, testServerURL+"/tasks/"+applied.SubtaskIDs[0]+"/actors",
		tt.AccessToken, nil, &childActors)
	require.Len(t, childActors.Actors, 1)
	require.Equal(t, tt.UserPublicID, childActors.Actors[0].UserID)
	require.Equal(t, "assignee", childActors.Actors[0].Role)

	requireOwnCreatedEvent(t, applied.TaskID, parentTitle, "", "api:smart_create", "apply-smart parent")
	requireOwnCreatedEvent(t, applied.SubtaskIDs[0], childTitle, applied.TaskID, "api:smart_create", "apply-smart assigned child")
	requireOwnCreatedEvent(t, applied.SubtaskIDs[1], "unassigned apply-smart child", applied.TaskID, "api:smart_create", "apply-smart unassigned child")
}

// TestApplySmartRejectsMalformedDueOn checks that a bad date is answered
// with the same field validation error as ordinary task creation.
func TestApplySmartRejectsMalformedDueOn(t *testing.T) {
	bootstrap(t)
	t.Parallel()
	tt := newTenant(t)

	status, body := doJSONStatus(t, http.MethodPost,
		testServerURL+"/workspaces/"+tt.WorkspacePublicID+"/tasks/apply-smart",
		tt.AccessToken, map[string]any{
			"projectId": tt.ProjectPublicID,
			"title":     "apply-smart with an invalid date",
			"priority":  0,
			"dueOn":     "2032-02-30",
		},
	)
	require.Equalf(t, http.StatusBadRequest, status, "malformed apply-smart date body=%s", string(body))
	require.Equal(t, "VALIDATION.BODY.DATE_FORMAT_INVALID", decodeErrorCode(t, body))
}
