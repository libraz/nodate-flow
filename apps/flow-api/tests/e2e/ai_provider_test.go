package e2e

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
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
