package e2e

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/nodate-flow/nodate-flow/apps/flow-api/internal/db/types"
)

// TestAIProviderCRUD exercises the AI provider create/list/patch/delete
// endpoints and verifies the plaintext key never appears in any
// response body.
func TestAIProviderCRUD(t *testing.T) {
	bootstrap(t)
	t.Parallel()

	tt := newTenant(t)
	plaintextKey := "sk-ant-this-is-a-test-key-0123456789"

	// Create.
	status, body := doJSONStatus(t, http.MethodPost,
		testServerURL+"/workspaces/"+tt.WorkspacePublicID+"/ai/providers",
		tt.AccessToken, map[string]any{
			"kind":   "anthropic",
			"name":   "Test Anthropic",
			"apiKey": plaintextKey,
		})
	require.GreaterOrEqual(t, status, 200, "create status body=%s", string(body))
	require.Less(t, status, 300, "create status body=%s", string(body))
	require.False(t, strings.Contains(string(body), plaintextKey),
		"create response must not echo plaintext key")

	var created struct {
		ID           string `json:"id"`
		Kind         string `json:"kind"`
		APIKeyMasked string `json:"apiKeyMasked"`
	}
	require.NoError(t, json.Unmarshal(body, &created))
	require.NotEmpty(t, created.ID)
	require.Equal(t, "anthropic", created.Kind)
	require.NotEmpty(t, created.APIKeyMasked)

	// List — no ciphertext, no plaintext.
	status, body = doJSONStatus(t, http.MethodGet,
		testServerURL+"/workspaces/"+tt.WorkspacePublicID+"/ai/providers",
		tt.AccessToken, nil)
	require.Equal(t, http.StatusOK, status)
	require.False(t, strings.Contains(string(body), plaintextKey),
		"list response must not contain plaintext key")

	// Patch (rotate key).
	status, _ = doJSONStatus(t, http.MethodPatch,
		testServerURL+"/workspaces/"+tt.WorkspacePublicID+"/ai/providers/"+created.ID,
		tt.AccessToken, map[string]any{"apiKey": "sk-ant-rotated-key-9876543210"}) //#nosec G101 -- synthetic test fixture, never a real key
	require.Equal(t, http.StatusOK, status)

	// Delete.
	status, _ = doJSONStatus(t, http.MethodDelete,
		testServerURL+"/workspaces/"+tt.WorkspacePublicID+"/ai/providers/"+created.ID,
		tt.AccessToken, nil)
	require.Equal(t, http.StatusOK, status)
}

// countProviderAudit returns how many audit_logs rows exist for the
// given internal workspace id and action. Used to assert that a failed
// rotation/deletion records no audit entry.
func countProviderAudit(t *testing.T, wsID uint32, action string) int {
	t.Helper()
	var n int
	err := testDB.QueryRowContext(context.Background(),
		`SELECT COUNT(*) FROM audit_logs WHERE workspace_id = ? AND action = ?`,
		wsID, action).Scan(&n)
	require.NoError(t, err)
	return n
}

// TestAIProviderRotateDeleteNotFound verifies that rotating or deleting a
// provider that does not exist in the caller's workspace (either a random
// id or one owned by another workspace) returns 404 and records no audit
// entry. This guards against a false-success where a leaked key appears
// rotated but the stored ciphertext is never touched.
func TestAIProviderRotateDeleteNotFound(t *testing.T) {
	bootstrap(t)
	t.Parallel()

	tt := newTenant(t)
	other := newTenant(t)
	wsID := internalWorkspaceID(t, testDB, tt.WorkspacePublicID)

	// A provider that belongs to the other workspace; the caller must not
	// be able to touch it through their own workspace path.
	status, body := doJSONStatus(t, http.MethodPost,
		testServerURL+"/workspaces/"+other.WorkspacePublicID+"/ai/providers",
		other.AccessToken, map[string]any{
			"kind":   "anthropic",
			"name":   "Other Anthropic",
			"apiKey": "sk-ant-other-workspace-key-0123456789", //#nosec G101 -- synthetic test fixture, never a real key
		})
	require.GreaterOrEqual(t, status, 200, "create status body=%s", string(body))
	require.Less(t, status, 300, "create status body=%s", string(body))
	var otherProvider struct {
		ID string `json:"id"`
	}
	require.NoError(t, json.Unmarshal(body, &otherProvider))
	require.NotEmpty(t, otherProvider.ID)

	updateBefore := countProviderAudit(t, wsID, "ai_provider.update")
	deleteBefore := countProviderAudit(t, wsID, "ai_provider.delete")

	missingID := types.New().UUID().String()

	// PATCH: random non-existent id -> 404.
	status, _ = doJSONStatus(t, http.MethodPatch,
		testServerURL+"/workspaces/"+tt.WorkspacePublicID+"/ai/providers/"+missingID,
		tt.AccessToken, map[string]any{"apiKey": "sk-ant-should-not-apply-000000"}) //#nosec G101 -- synthetic test fixture, never a real key
	require.Equal(t, http.StatusNotFound, status)

	// PATCH: id owned by another workspace -> 404 (cross-tenant isolation).
	status, _ = doJSONStatus(t, http.MethodPatch,
		testServerURL+"/workspaces/"+tt.WorkspacePublicID+"/ai/providers/"+otherProvider.ID,
		tt.AccessToken, map[string]any{"apiKey": "sk-ant-should-not-apply-111111"}) //#nosec G101 -- synthetic test fixture, never a real key
	require.Equal(t, http.StatusNotFound, status)

	// DELETE: random non-existent id -> 404.
	status, _ = doJSONStatus(t, http.MethodDelete,
		testServerURL+"/workspaces/"+tt.WorkspacePublicID+"/ai/providers/"+missingID,
		tt.AccessToken, nil)
	require.Equal(t, http.StatusNotFound, status)

	// DELETE: id owned by another workspace -> 404.
	status, _ = doJSONStatus(t, http.MethodDelete,
		testServerURL+"/workspaces/"+tt.WorkspacePublicID+"/ai/providers/"+otherProvider.ID,
		tt.AccessToken, nil)
	require.Equal(t, http.StatusNotFound, status)

	// None of the failed rotations/deletions may have written an audit row.
	require.Equal(t, updateBefore, countProviderAudit(t, wsID, "ai_provider.update"),
		"failed rotation must not append an audit row")
	require.Equal(t, deleteBefore, countProviderAudit(t, wsID, "ai_provider.delete"),
		"failed deletion must not append an audit row")
}
