package e2e

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/libraz/nodate-flow/apps/flow-api/tests/helpers"
)

func taskMutationRequest(t *testing.T, method, url, token, key string, body any) (int, []byte) {
	t.Helper()
	encoded, err := json.Marshal(body)
	require.NoError(t, err)
	req, err := http.NewRequest(method, url, bytes.NewReader(encoded))
	require.NoError(t, err)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Authorization", "Bearer "+token)
	if key != "" {
		req.Header.Set("Idempotency-Key", key)
	}
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer func() { _ = resp.Body.Close() }()
	raw, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	return resp.StatusCode, raw
}

func postTaskTransition(t *testing.T, url, token, key string, body any) (int, []byte) {
	t.Helper()
	return taskMutationRequest(t, http.MethodPost, url, token, key, body)
}

func taskTransitionEventCount(t *testing.T, workspaceID, taskID uint32) int {
	t.Helper()
	var count int
	require.NoError(t, testDB.QueryRowContext(context.Background(), `
		SELECT COUNT(*) FROM events
		WHERE workspace_id = ? AND task_id = ? AND type LIKE 'task.transition.%'
	`, workspaceID, taskID).Scan(&count))
	return count
}

func taskUpdateEventCount(t *testing.T, workspaceID, taskID uint32) int {
	t.Helper()
	var count int
	require.NoError(t, testDB.QueryRowContext(context.Background(), `
		SELECT COUNT(*) FROM events
		WHERE workspace_id = ? AND task_id = ? AND type = 'task.updated'
	`, workspaceID, taskID).Scan(&count))
	return count
}

func TestTaskUpdateIdempotency(t *testing.T) {
	bootstrap(t)
	t.Parallel()

	tenant := newTenant(t)
	var task struct {
		ID string `json:"id"`
	}
	doJSON(t, http.MethodPost, testServerURL+"/tasks", tenant.AccessToken, map[string]any{
		"projectId": tenant.ProjectPublicID,
		"title":     "Conditional task transition " + randomHex(5),
	}, &task)

	url := testServerURL + "/tasks/" + task.ID + "/transitions"
	body := map[string]any{
		"transition":    "start",
		"expectedState": "open",
	}
	key := "task-transition-" + randomHex(12)

	status, first := postTaskTransition(t, url, tenant.AccessToken, key, body)
	require.Equalf(t, http.StatusOK, status, "body=%s", first)
	var created struct {
		ID           string `json:"id"`
		DerivedState string `json:"derivedState"`
	}
	require.NoError(t, json.Unmarshal(first, &created))
	require.Equal(t, task.ID, created.ID)
	require.Equal(t, "waiting", created.DerivedState)

	status, replay := postTaskTransition(t, url, tenant.AccessToken, key, body)
	require.Equalf(t, http.StatusOK, status, "body=%s", replay)
	require.JSONEq(t, string(first), string(replay), "a replay returns the original task response")

	workspaceID, taskID := helpers.AgentTaskInternalIDs(t, testDB, tenant.WorkspacePublicID, task.ID)
	require.Equal(t, 1, taskTransitionEventCount(t, workspaceID, taskID), "a replay must not add another transition event")

	status, conflict := postTaskTransition(t, url, tenant.AccessToken, key, map[string]any{
		"transition":    "block",
		"expectedState": "waiting",
	})
	require.Equalf(t, http.StatusConflict, status, "body=%s", conflict)
	require.Equal(t, "WS.TASK.IDEMPOTENCY_KEY_CONFLICT", problemType(t, conflict))
	require.Equal(t, 1, taskTransitionEventCount(t, workspaceID, taskID))

	status, stale := postTaskTransition(t, url, tenant.AccessToken, "stale-"+randomHex(6), map[string]any{
		"transition":    "submit",
		"expectedState": "open",
	})
	require.Equalf(t, http.StatusConflict, status, "body=%s", stale)
	require.Equal(t, "WS.TASK.UPDATE_CONFLICT", problemType(t, stale))
	require.Equal(t, 1, taskTransitionEventCount(t, workspaceID, taskID), "a stale state check must not append an event")

	status, fresh := postTaskTransition(t, url, tenant.AccessToken, "", map[string]any{
		"transition":    "submit",
		"expectedState": "waiting",
	})
	require.Equalf(t, http.StatusOK, status, "body=%s", fresh)
	var submitted struct {
		DerivedState string `json:"derivedState"`
	}
	require.NoError(t, json.Unmarshal(fresh, &submitted))
	require.Equal(t, "review", submitted.DerivedState)
	require.Equal(t, 2, taskTransitionEventCount(t, workspaceID, taskID))

	var datedTask struct {
		ID string `json:"id"`
	}
	doJSON(t, http.MethodPost, testServerURL+"/tasks", tenant.AccessToken, map[string]any{
		"projectId": tenant.ProjectPublicID,
		"title":     "Conditional task due date " + randomHex(5),
		"dueOn":     "2035-01-12",
	}, &datedTask)
	patchURL := testServerURL + "/tasks/" + datedTask.ID
	patchBody := map[string]any{
		"dueOn":         "2035-01-15",
		"expectedDueOn": "2035-01-12",
	}
	patchKey := "task-patch-" + randomHex(12)
	status, firstPatch := taskMutationRequest(t, http.MethodPatch, patchURL, tenant.AccessToken, patchKey, patchBody)
	require.Equalf(t, http.StatusOK, status, "body=%s", firstPatch)
	var patched struct {
		ID    string `json:"id"`
		DueOn string `json:"dueOn"`
	}
	require.NoError(t, json.Unmarshal(firstPatch, &patched))
	require.Equal(t, datedTask.ID, patched.ID)
	require.Equal(t, "2035-01-15", patched.DueOn)
	dateWorkspaceID, dateTaskID := helpers.AgentTaskInternalIDs(t, testDB, tenant.WorkspacePublicID, datedTask.ID)

	status, replayPatch := taskMutationRequest(t, http.MethodPatch, patchURL, tenant.AccessToken, patchKey, patchBody)
	require.Equalf(t, http.StatusOK, status, "body=%s", replayPatch)
	require.JSONEq(t, string(firstPatch), string(replayPatch), "a patch replay returns the original task response")
	require.Equal(t, 1, taskUpdateEventCount(t, dateWorkspaceID, dateTaskID), "a patch replay must not add another update event")

	status, staleDue := taskMutationRequest(t, http.MethodPatch, patchURL, tenant.AccessToken, "stale-due-"+randomHex(6), map[string]any{
		"dueOn":         "2035-01-16",
		"expectedDueOn": "2035-01-12",
	})
	require.Equalf(t, http.StatusConflict, status, "body=%s", staleDue)
	require.Equal(t, "WS.TASK.UPDATE_CONFLICT", problemType(t, staleDue))
	require.Equal(t, 1, taskUpdateEventCount(t, dateWorkspaceID, dateTaskID), "a stale date precondition must not add an update event")

	var current struct {
		DueOn string `json:"dueOn"`
	}
	doJSON(t, http.MethodGet, patchURL, tenant.AccessToken, nil, &current)
	require.Equal(t, "2035-01-15", current.DueOn, "a stale due date precondition must leave the task unchanged")

	status, freshDue := taskMutationRequest(t, http.MethodPatch, patchURL, tenant.AccessToken, "", map[string]any{
		"dueOn":         "2035-01-17",
		"expectedDueOn": "2035-01-15",
	})
	require.Equalf(t, http.StatusOK, status, "body=%s", freshDue)
	require.Equal(t, 2, taskUpdateEventCount(t, dateWorkspaceID, dateTaskID))
}
