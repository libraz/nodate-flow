package e2e

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestWorkspaceEmbeddingSettingsAuthorization(t *testing.T) {
	bootstrap(t)
	t.Parallel()

	owner := newTenant(t)
	member := seedProjectRoleMember(t, owner, "editor")
	ownerURL := testServerURL + "/workspaces/" + owner.WorkspacePublicID + "/ai/embedding-settings"
	memberURL := testServerURL + "/workspaces/" + member.WorkspacePublicID + "/ai/embedding-settings"

	type settings struct {
		Mode   string `json:"mode"`
		Status string `json:"status"`
	}

	var initial settings
	doJSON(t, http.MethodGet, ownerURL, owner.AccessToken, nil, &initial)
	require.Equal(t, "inherit", initial.Mode)
	if initial.Status == "unconfigured" {
		assertSmartEmbeddingUnavailable(t, owner.WorkspacePublicID, owner.ProjectPublicID, owner.AccessToken, "AI.EMBEDDING.NOT_CONFIGURED")
	}

	status, body := doJSONStatus(t, http.MethodGet, ownerURL, member.AccessToken, nil)
	require.Equalf(t, http.StatusForbidden, status, "member read response: %s", body)
	status, body = doJSONStatus(t, http.MethodPatch, ownerURL, member.AccessToken, map[string]string{"mode": "disabled"})
	require.Equalf(t, http.StatusForbidden, status, "member write response: %s", body)

	var disabled settings
	doJSON(t, http.MethodPatch, ownerURL, owner.AccessToken, map[string]string{"mode": "disabled"}, &disabled)
	require.Equal(t, settings{Mode: "disabled", Status: "disabled"}, disabled)
	doJSON(t, http.MethodGet, ownerURL, owner.AccessToken, nil, &disabled)
	require.Equal(t, settings{Mode: "disabled", Status: "disabled"}, disabled)
	assertSmartEmbeddingUnavailable(t, owner.WorkspacePublicID, owner.ProjectPublicID, owner.AccessToken, "AI.EMBEDDING.DISABLED")

	var other settings
	doJSON(t, http.MethodGet, memberURL, member.AccessToken, nil, &other)
	require.Equal(t, initial, other)

	var restored settings
	doJSON(t, http.MethodPatch, ownerURL, owner.AccessToken, map[string]string{"mode": "inherit"}, &restored)
	require.Equal(t, initial, restored)
}

func assertSmartEmbeddingUnavailable(t *testing.T, workspaceID, projectID, token, code string) {
	t.Helper()
	status, body := doJSONStatus(t, http.MethodPost, testServerURL+"/workspaces/"+workspaceID+"/tasks/propose-smart",
		token, map[string]string{"projectId": projectID, "title": "Check the session flow"})
	requireDenied(t, status, body, http.StatusPreconditionFailed, code, "smart create embedding prerequisite")
}

func TestWorkspaceEmbeddingOptOutStopsVectorWritesAndReads(t *testing.T) {
	bootstrap(t)
	requireAIMock(t)
	t.Parallel()

	tenant := newTenant(t)
	workspaceURL := testServerURL + "/workspaces/" + tenant.WorkspacePublicID
	settingsURL := workspaceURL + "/ai/embedding-settings"
	project := &tenantForRestore{token: tenant.AccessToken, projectID: tenant.ProjectPublicID}
	sourceID := createTaskWithDescription(t, project, "Intermittent sign-in errors", "Check the session flow.")
	peerID := createTaskWithDescription(t, project, "Intermittent sign-in errors", "Check the session flow.")
	peerHash := taskEmbeddingContentHash(t, peerID)
	duplicatesURL := testServerURL + "/tasks/" + sourceID + "/duplicates"
	var duplicates struct {
		Model      string            `json:"model"`
		Candidates []json.RawMessage `json:"candidates"`
	}
	doJSON(t, http.MethodGet, duplicatesURL, tenant.AccessToken, nil, &duplicates)
	require.NotEmpty(t, duplicates.Candidates, "the identical task must be returned before opting out")

	doJSON(t, http.MethodPatch, settingsURL, tenant.AccessToken, map[string]string{"mode": "disabled"}, nil)

	doJSON(t, http.MethodGet, duplicatesURL, tenant.AccessToken, nil, &duplicates)
	require.Empty(t, duplicates.Candidates, "stored vectors must be ignored after opting out")
	require.Empty(t, duplicates.Model)

	disabledTaskID := createTaskWithDescription(t, project, "Created after opting out", "Keep this task out of embeddings.")
	doJSON(t, http.MethodPatch, testServerURL+"/tasks/"+sourceID, tenant.AccessToken,
		map[string]string{"description": "Updated while embeddings were disabled."}, nil)
	for _, taskID := range []string{disabledTaskID, sourceID} {
		var vectorCount int
		require.NoError(t, testDB.QueryRow(
			`SELECT COUNT(*) FROM task_embeddings WHERE task_id = (SELECT id FROM tasks WHERE public_id = UUID_TO_BIN(?, 0))`,
			taskID,
		).Scan(&vectorCount))
		require.Zero(t, vectorCount, "created or changed task %s has a stored vector", taskID)
	}
	require.Equal(t, peerHash, taskEmbeddingContentHash(t, peerID))

	doJSON(t, http.MethodPatch, settingsURL, tenant.AccessToken, map[string]string{"mode": "inherit"}, nil)
	enabledTaskID := createTaskWithDescription(t, project, "Created after opting in", "This task is eligible for embeddings.")
	taskEmbeddingContentHash(t, enabledTaskID)
}
