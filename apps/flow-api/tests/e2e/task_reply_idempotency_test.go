package e2e

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"
)

type taskMutationRequestResult struct {
	status int
	body   []byte
	err    error
}

func postTaskMutationWithKey(url, token, key string, body any) (int, []byte, error) {
	encoded, err := json.Marshal(body)
	if err != nil {
		return 0, nil, err
	}
	req, err := http.NewRequest(http.MethodPost, url, bytes.NewReader(encoded))
	if err != nil {
		return 0, nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Idempotency-Key", key)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return 0, nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	raw, err := io.ReadAll(resp.Body)
	return resp.StatusCode, raw, err
}

func concurrentTaskMutationRequests(url, token, key string, body any, count int) []taskMutationRequestResult {
	results := make([]taskMutationRequestResult, count)
	start := make(chan struct{})
	var wait sync.WaitGroup
	for i := range results {
		wait.Add(1)
		go func(i int) {
			defer wait.Done()
			<-start
			results[i].status, results[i].body, results[i].err = postTaskMutationWithKey(url, token, key, body)
		}(i)
	}
	close(start)
	wait.Wait()
	return results
}

func TestTaskReplyIdempotency(t *testing.T) {
	bootstrap(t)
	t.Parallel()

	tenant := newTenant(t)
	var commentTask struct {
		ID string `json:"id"`
	}
	doJSON(t, http.MethodPost, testServerURL+"/tasks", tenant.AccessToken, map[string]any{
		"projectId": tenant.ProjectPublicID,
		"title":     "Comment receipt parent " + randomHex(6),
	}, &commentTask)
	commentURL := testServerURL + "/tasks/" + commentTask.ID + "/comments"
	commentKey := "comment-" + randomHex(12)
	commentBody := map[string]any{"body": "The work is done."}

	commentResults := concurrentTaskMutationRequests(commentURL, tenant.AccessToken, commentKey, commentBody, 4)
	var commentID string
	var commentResponse []byte
	for i, result := range commentResults {
		require.NoErrorf(t, result.err, "comment request %d", i)
		require.Equalf(t, http.StatusOK, result.status, "comment request %d body=%s", i, result.body)
		var response struct {
			ID   string `json:"id"`
			Body string `json:"body"`
		}
		require.NoError(t, json.Unmarshal(result.body, &response))
		require.NotEmpty(t, response.ID)
		require.Equal(t, "The work is done.", response.Body)
		if i == 0 {
			commentID = response.ID
			commentResponse = result.body
		} else {
			require.Equal(t, commentID, response.ID)
			require.JSONEq(t, string(commentResponse), string(result.body))
		}
	}
	var commentCount int
	require.NoError(t, testDB.QueryRow(
		`SELECT COUNT(*) FROM comments
WHERE workspace_id = (SELECT id FROM workspaces WHERE public_id = UUID_TO_BIN(?, 0))
  AND task_id = (SELECT id FROM tasks WHERE public_id = UUID_TO_BIN(?, 0))
  AND body = ? AND enabled = TRUE`,
		tenant.WorkspacePublicID, commentTask.ID, "The work is done.",
	).Scan(&commentCount))
	require.Equal(t, 1, commentCount)

	status, raw, err := postTaskMutationWithKey(commentURL, tenant.AccessToken, commentKey,
		map[string]any{"body": "A different reply."})
	require.NoError(t, err)
	require.Equalf(t, http.StatusConflict, status, "same key with changed comment body: %s", raw)
	require.Equal(t, "WS.TASK.IDEMPOTENCY_KEY_CONFLICT", problemType(t, raw))

	status, raw = doJSONStatus(t, http.MethodDelete,
		commentURL+"/"+commentID, tenant.AccessToken, nil)
	require.Equalf(t, http.StatusOK, status, "delete created comment: %s", raw)
	status, raw, err = postTaskMutationWithKey(commentURL, tenant.AccessToken, commentKey, commentBody)
	require.NoError(t, err)
	require.Equalf(t, http.StatusNotFound, status, "replay after comment deletion: %s", raw)
	require.Equal(t, "WS.COMMENT.NOT_FOUND", problemType(t, raw))

	var stepsParent struct {
		ID string `json:"id"`
	}
	doJSON(t, http.MethodPost, testServerURL+"/tasks", tenant.AccessToken, map[string]any{
		"projectId": tenant.ProjectPublicID,
		"title":     "Apply receipt parent " + randomHex(6),
	}, &stepsParent)
	applyURL := testServerURL + "/tasks/" + stepsParent.ID + "/apply-steps"
	botToken := mintPatForWorkspace(t, tenant.AccessToken, tenant.UserPublicID, tenant.WorkspacePublicID)
	applyKey := "steps-" + randomHex(12)
	stepsBody := map[string]any{
		"steps": []map[string]any{
			{"title": "Confirm result"},
			{"title": "Notify the team"},
		},
	}
	applyResults := concurrentTaskMutationRequests(applyURL, botToken, applyKey, stepsBody, 4)
	var created []string
	var applyResponse []byte
	for i, result := range applyResults {
		require.NoErrorf(t, result.err, "apply-steps request %d", i)
		require.Equalf(t, http.StatusOK, result.status, "apply-steps request %d body=%s", i, result.body)
		var response struct {
			Created []string `json:"created"`
		}
		require.NoError(t, json.Unmarshal(result.body, &response))
		require.Len(t, response.Created, 2)
		if i == 0 {
			created = response.Created
			applyResponse = result.body
		} else {
			require.Equal(t, created, response.Created)
			require.JSONEq(t, string(applyResponse), string(result.body))
		}
	}
	var childCount int
	require.NoError(t, testDB.QueryRow(
		`SELECT COUNT(*) FROM tasks
WHERE parent_task_id = (SELECT id FROM tasks WHERE public_id = UUID_TO_BIN(?, 0))
  AND enabled = TRUE`,
		stepsParent.ID,
	).Scan(&childCount))
	require.Equal(t, 2, childCount)

	conflictingSteps := map[string]any{
		"steps": []map[string]any{{"title": "A different step"}},
	}
	status, raw, err = postTaskMutationWithKey(applyURL, botToken, applyKey, conflictingSteps)
	require.NoError(t, err)
	require.Equalf(t, http.StatusConflict, status, "same key with changed steps: %s", raw)
	require.Equal(t, "WS.TASK.IDEMPOTENCY_KEY_CONFLICT", problemType(t, raw))
	require.NoError(t, testDB.QueryRow(
		`SELECT COUNT(*) FROM tasks
WHERE parent_task_id = (SELECT id FROM tasks WHERE public_id = UUID_TO_BIN(?, 0))
  AND enabled = TRUE`,
		stepsParent.ID,
	).Scan(&childCount))
	require.Equal(t, 2, childCount, "conflicting request must not create children")
}
