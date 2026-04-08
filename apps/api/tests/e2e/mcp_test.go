package e2e

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestMCPTokenAndJSONRPC mints an MCP token via the REST API, then
// exercises the /mcp JSON-RPC transport to list tools and call
// get_task. It also asserts an mcp_invocations row is recorded.
func TestMCPTokenAndJSONRPC(t *testing.T) {
	bootstrap(t)
	t.Parallel()

	tt := newTenant(t)

	// Create an MCP token via the REST API.
	var tokenResp struct {
		ID          string   `json:"id"`
		Name        string   `json:"name"`
		Token       string   `json:"token"`
		TokenPrefix string   `json:"tokenPrefix"`
		Scopes      []string `json:"scopes"`
	}
	doJSON(t, http.MethodPost,
		testServerURL+"/workspaces/"+tt.WorkspacePublicID+"/me/mcp-tokens",
		tt.AccessToken, map[string]any{
			"name":   "test-token",
			"scopes": []string{"read:workspace", "write:workspace"},
		}, &tokenResp)
	require.NotEmpty(t, tokenResp.Token, "plaintext token must be returned once")
	require.True(t, strings.HasPrefix(tokenResp.Token, "mcp_"), "token must have mcp_ prefix")

	// Create a task to read back via MCP.
	var task struct {
		ID string `json:"id"`
	}
	doJSON(t, http.MethodPost, testServerURL+"/tasks", tt.AccessToken, map[string]any{
		"projectId": tt.ProjectPublicID,
		"title":     "MCP target",
	}, &task)

	// tools/list via JSON-RPC.
	listResp := mcpCall(t, tokenResp.Token, "tools/list", nil)
	require.Contains(t, string(listResp), "tools",
		"tools/list must return tools array, got=%s", string(listResp))

	// tools/call get_task.
	callResp := mcpCall(t, tokenResp.Token, "tools/call", map[string]any{
		"name":      "get_task",
		"arguments": map[string]any{"taskId": task.ID},
	})
	require.Contains(t, string(callResp), task.ID,
		"get_task result must reference the task id, got=%s", string(callResp))

	// Verify an mcp_invocations row was recorded for this workspace.
	var invocations int
	err := testDB.QueryRow(
		`SELECT COUNT(*) FROM mcp_invocations
		 WHERE workspace_id = (SELECT id FROM workspaces WHERE public_id = UUID_TO_BIN(?, 0))`,
		tt.WorkspacePublicID).Scan(&invocations)
	require.NoError(t, err)
	require.Greater(t, invocations, 0, "tools/call must record mcp_invocations row")
}

// mcpCall sends a single JSON-RPC 2.0 request frame to /mcp with the
// given MCP bearer token. It returns the raw response body.
func mcpCall(t *testing.T, token, method string, params any) []byte {
	t.Helper()
	frame := map[string]any{
		"jsonrpc": "2.0",
		"id":      1,
		"method":  method,
	}
	if params != nil {
		frame["params"] = params
	}
	buf, err := json.Marshal(frame)
	require.NoError(t, err)
	req, err := http.NewRequest(http.MethodPost, testServerURL+"/mcp", bytes.NewReader(buf))
	require.NoError(t, err)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer func() { _ = resp.Body.Close() }()
	raw, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, resp.StatusCode, "mcp %s -> %d body=%s", method, resp.StatusCode, string(raw))
	return raw
}
