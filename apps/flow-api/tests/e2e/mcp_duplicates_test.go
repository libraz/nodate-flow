package e2e

import (
	"encoding/json"
	"net/http"
	"os"
	"strings"
	"testing"

	"github.com/libraz/nodate-flow/apps/flow-api/internal/ai/embed"
	"github.com/stretchr/testify/require"
)

// TestMCPProposeDuplicates exercises the propose_duplicates MCP tool
// against two identical-text tasks; the twin must rank first with
// classification "duplicate".
func TestMCPProposeDuplicates(t *testing.T) {
	bootstrap(t)
	if os.Getenv("NF_FLOW_AI_MOCK") == "" {
		t.Skip("set NF_AI_MOCK=1 to run duplicate-detection MCP e2e tests")
	}
	t.Parallel()

	tt := newTenant(t)

	// Mint MCP token.
	var tokenResp struct {
		Token string `json:"token"`
	}
	doJSON(t, http.MethodPost,
		testServerURL+"/workspaces/"+tt.WorkspacePublicID+"/me/mcp-tokens",
		tt.AccessToken, map[string]any{
			"name":   "dup-token",
			"scopes": []string{"read:workspace", "write:workspace"},
		}, &tokenResp)
	require.True(t, strings.HasPrefix(tokenResp.Token, "mcp_"))

	create := func(title, desc string) string {
		var r struct {
			ID string `json:"id"`
		}
		doJSON(t, http.MethodPost, testServerURL+"/tasks", tt.AccessToken, map[string]any{
			"projectId":   tt.ProjectPublicID,
			"title":       title,
			"description": desc,
		}, &r)
		return r.ID
	}
	srcID := create("Investigate login bug", "Users cannot log in after password reset.")
	twinID := create("Investigate login bug", "Users cannot log in after password reset.")
	_ = create("Update billing invoice PDF template", "Quarterly redesign.")

	resp := mcpCall(t, tokenResp.Token, "tools/call", map[string]any{
		"name":      "propose_duplicates",
		"arguments": map[string]any{"taskId": srcID},
	})

	var frame struct {
		Result struct {
			Content []struct {
				Type string `json:"type"`
				Text string `json:"text"`
			} `json:"content"`
		} `json:"result"`
	}
	require.NoError(t, json.Unmarshal(resp, &frame), "raw=%s", string(resp))
	require.NotEmpty(t, frame.Result.Content, "raw=%s", string(resp))

	var payload struct {
		Model      string `json:"model"`
		Candidates []struct {
			TaskID         string  `json:"taskId"`
			Score          float64 `json:"score"`
			Classification string  `json:"classification"`
		} `json:"candidates"`
	}
	require.NoError(t, json.Unmarshal([]byte(frame.Result.Content[0].Text), &payload),
		"inner=%s", frame.Result.Content[0].Text)
	// See the note in task_duplicates_test.go: the expected key comes
	// from the embedder that wrote the rows, not from its current name.
	require.Equal(t, embed.NewMockProvider().Model(), payload.Model)
	require.NotEmpty(t, payload.Candidates)
	require.Equal(t, twinID, payload.Candidates[0].TaskID)
	require.Equal(t, "duplicate", payload.Candidates[0].Classification)
	require.InDelta(t, 1.0, payload.Candidates[0].Score, 1e-4)
}
