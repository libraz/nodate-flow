package e2e

import (
	"net/http"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// Bodies used by both restore tests. They differ enough that the text
// each one composes with a title hashes to a distinct value, which is
// what makes the embedding assertions readable.
const (
	descriptionOriginalBody = "The reporter cannot sign in after completing a password reset."
	descriptionRevisedBody  = "The reporter is bounced back to the sign-in form in a loop."
)

// taskEmbeddingContentHash reads the SHA-256 hex of the text a task's
// stored embedding was generated from. task_embeddings holds one row per
// (task_id, model) and the suite runs a single embedding model, so the
// task alone identifies the row.
func taskEmbeddingContentHash(t *testing.T, taskPublicID string) string {
	t.Helper()
	var hash string
	require.NoError(t, testDB.QueryRow(
		`SELECT te.content_hash
		   FROM task_embeddings te
		   JOIN tasks t ON t.id = te.task_id
		  WHERE t.public_id = UUID_TO_BIN(?, 0)`,
		taskPublicID).Scan(&hash),
		"task %s has no stored embedding", taskPublicID)
	require.Len(t, hash, 64, "content_hash must be a sha-256 hex digest")
	return hash
}

// createTaskWithDescription creates a task through the REST API and
// returns its public id.
func createTaskWithDescription(t *testing.T, tt *tenantForRestore, title, description string) string {
	t.Helper()
	var created struct {
		ID string `json:"id"`
	}
	doJSON(t, http.MethodPost, testServerURL+"/tasks", tt.token, map[string]any{
		"projectId":   tt.projectID,
		"title":       title,
		"description": description,
	}, &created)
	require.NotEmpty(t, created.ID)
	return created.ID
}

// tenantForRestore is the slice of a test tenant the helpers above need.
type tenantForRestore struct {
	token     string
	projectID string
}

// descriptionVersionBody reads a stored version's body through the REST
// read route. The MCP surface exposes no per-version read tool, so both
// tests use this route to inspect a body; the entrance under test is the
// one that performs the restore, not the one that reads it back.
func descriptionVersionBody(t *testing.T, token, taskID, versionID string) string {
	t.Helper()
	var version struct {
		Body          string `json:"body"`
		VersionNumber int    `json:"versionNumber"`
	}
	doJSON(t, http.MethodGet,
		testServerURL+"/tasks/"+taskID+"/description-history/"+versionID,
		token, nil, &version)
	return version.Body
}

// descriptionVersion is one entry of a task's description history as both
// the REST route and the MCP tool report it.
type descriptionVersion struct {
	ID            string `json:"id"`
	VersionNumber int    `json:"versionNumber"`
	BodyLength    int    `json:"bodyLength"`
}

// TestRestoreDescriptionVersionREST drives
// POST /tasks/{id}/description-history/{versionId}/restore and asserts
// the three things a restore owes its caller: the task carries the
// restored body, the history gained a version rather than losing one,
// and the stored embedding was regenerated from the restored text.
func TestRestoreDescriptionVersionREST(t *testing.T) {
	bootstrap(t)
	requireAIMock(t)
	t.Parallel()

	tenant := newTenant(t)
	tt := &tenantForRestore{token: tenant.AccessToken, projectID: tenant.ProjectPublicID}

	taskID := createTaskWithDescription(t, tt, "Sign-in fails after reset", descriptionOriginalBody)
	originalHash := taskEmbeddingContentHash(t, taskID)

	listVersions := func() []descriptionVersion {
		var out struct {
			Versions []descriptionVersion `json:"versions"`
		}
		doJSON(t, http.MethodGet,
			testServerURL+"/tasks/"+taskID+"/description-history", tt.token, nil, &out)
		return out.Versions
	}

	// Creating the task is what puts the original body in the history;
	// without this version there is nothing for a restore to return to.
	created := listVersions()
	require.Len(t, created, 1, "creating a task snapshots the description it was created with")
	require.Equal(t, 1, created[0].VersionNumber)
	require.Equal(t, descriptionOriginalBody,
		descriptionVersionBody(t, tt.token, taskID, created[0].ID),
		"version 1 must hold the body the task was created with")
	originalVersionID := created[0].ID

	doJSON(t, http.MethodPatch, testServerURL+"/tasks/"+taskID, tt.token,
		map[string]any{"description": descriptionRevisedBody}, nil)
	revisedHash := taskEmbeddingContentHash(t, taskID)
	require.NotEqual(t, originalHash, revisedHash,
		"editing the description must re-embed the task under a new hash")

	before := listVersions()
	require.Len(t, before, 2, "editing the description appends a version")

	var restored struct {
		Ok bool `json:"ok"`
	}
	doJSON(t, http.MethodPost,
		testServerURL+"/tasks/"+taskID+"/description-history/"+originalVersionID+"/restore",
		tt.token, nil, &restored)
	require.True(t, restored.Ok)

	var task struct {
		Description string `json:"description"`
	}
	doJSON(t, http.MethodGet, testServerURL+"/tasks/"+taskID, tt.token, nil, &task)
	require.Equal(t, descriptionOriginalBody, task.Description,
		"the task must carry the restored body")

	after := listVersions()
	require.Len(t, after, len(before)+1,
		"a restore appends a version; it must not rewind or rewrite the history")
	// The list is ordered newest first.
	newest := after[0]
	require.Greater(t, newest.VersionNumber, before[0].VersionNumber,
		"the appended version must sit above the one it restored")
	require.Equal(t, descriptionOriginalBody,
		descriptionVersionBody(t, tt.token, taskID, newest.ID),
		"the appended version must snapshot the restored body")

	require.Equal(t, originalHash, taskEmbeddingContentHash(t, taskID),
		"the stored embedding must be regenerated from the restored text")
}

// TestRestoreDescriptionVersionMCP drives the restore_description_version
// MCP tool and asserts the same three properties as the REST test. The
// tool is a second implementation of the restore rather than a wrapper
// over the handler, so it needs its own coverage.
func TestRestoreDescriptionVersionMCP(t *testing.T) {
	bootstrap(t)
	requireAIMock(t)
	t.Parallel()

	tenant := newTenant(t)
	tt := &tenantForRestore{token: tenant.AccessToken, projectID: tenant.ProjectPublicID}

	var tokenResp struct {
		Token string `json:"token"`
	}
	doJSON(t, http.MethodPost,
		testServerURL+"/workspaces/"+tenant.WorkspacePublicID+"/me/mcp-tokens",
		tt.token, map[string]any{
			"name":   "description-restore",
			"scopes": []string{"read:workspace", "write:workspace"},
		}, &tokenResp)
	require.True(t, strings.HasPrefix(tokenResp.Token, "mcp_"))

	taskID := createTaskWithDescription(t, tt, "Sign-in loops after reset", descriptionOriginalBody)
	originalHash := taskEmbeddingContentHash(t, taskID)

	callTool := func(name string, args map[string]any) []byte {
		return mcpCall(t, tokenResp.Token, "tools/call", map[string]any{
			"name":      name,
			"arguments": args,
		})
	}
	type versionList struct {
		Versions []descriptionVersion `json:"versions"`
	}
	listVersions := func() []descriptionVersion {
		return mcpToolTextJSON[versionList](t, callTool("list_description_versions",
			map[string]any{"taskId": taskID})).Versions
	}

	// Creating the task is what puts the original body in the history;
	// without this version there is nothing for a restore to return to.
	created := listVersions()
	require.Len(t, created, 1, "creating a task snapshots the description it was created with")
	require.Equal(t, 1, created[0].VersionNumber)
	require.Equal(t, descriptionOriginalBody,
		descriptionVersionBody(t, tt.token, taskID, created[0].ID),
		"version 1 must hold the body the task was created with")
	originalVersionID := created[0].ID

	callTool("update_task", map[string]any{
		"taskId":      taskID,
		"description": descriptionRevisedBody,
	})
	revisedHash := taskEmbeddingContentHash(t, taskID)
	require.NotEqual(t, originalHash, revisedHash,
		"editing the description must re-embed the task under a new hash")

	before := listVersions()
	require.Len(t, before, 2, "editing the description appends a version")

	restored := mcpToolTextJSON[struct {
		Ok           bool   `json:"ok"`
		NewVersionID string `json:"newVersionId"`
	}](t, callTool("restore_description_version", map[string]any{
		"taskId":    taskID,
		"versionId": originalVersionID,
	}))
	require.True(t, restored.Ok)
	require.NotEmpty(t, restored.NewVersionID)

	task := mcpToolTextJSON[struct {
		Description string `json:"description"`
	}](t, callTool("get_task", map[string]any{"taskId": taskID}))
	require.Equal(t, descriptionOriginalBody, task.Description,
		"the task must carry the restored body")

	after := listVersions()
	require.Len(t, after, len(before)+1,
		"a restore appends a version; it must not rewind or rewrite the history")
	newest := after[0]
	require.Equal(t, restored.NewVersionID, newest.ID,
		"the appended version must be the one the tool reported")
	require.Greater(t, newest.VersionNumber, before[0].VersionNumber,
		"the appended version must sit above the one it restored")
	require.Equal(t, descriptionOriginalBody,
		descriptionVersionBody(t, tt.token, taskID, newest.ID),
		"the appended version must snapshot the restored body")

	require.Equal(t, originalHash, taskEmbeddingContentHash(t, taskID),
		"the stored embedding must be regenerated from the restored text")
}
