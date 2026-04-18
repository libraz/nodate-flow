package e2e

import (
	"net/http"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/nodate-flow/nodate-flow/apps/flow-api/internal/ai"
)

// TestSecretLeakMeta hits a representative set of endpoints and
// asserts that no response body contains any of the literal secret
// prefixes tracked by internal/ai.SecretPrefixes, nor any plaintext
// value we pushed into the AI provider CRUD. This is the cheap
// meta-check that catches regressions where a handler accidentally
// echoes a ciphertext or a plaintext key back to the client.
func TestSecretLeakMeta(t *testing.T) {
	bootstrap(t)
	t.Parallel()

	tt := newTenant(t)
	plaintextAPIKey := "sk-ant-secret-leak-test-value-0123456789"

	// Seed a provider so list/get responses could potentially leak
	// the plaintext if the handler were buggy.
	var provider struct {
		ID string `json:"id"`
	}
	doJSON(t, http.MethodPost,
		testServerURL+"/workspaces/"+tt.WorkspacePublicID+"/ai/providers",
		tt.AccessToken, map[string]any{
			"kind":   "anthropic",
			"name":   "Leak Probe",
			"apiKey": plaintextAPIKey,
		}, &provider)

	// Mint an MCP token so the token-list response must not echo it
	// back on subsequent reads.
	var mcpTok struct {
		ID    string `json:"id"`
		Token string `json:"token"`
	}
	doJSON(t, http.MethodPost,
		testServerURL+"/workspaces/"+tt.WorkspacePublicID+"/me/mcp-tokens",
		tt.AccessToken, map[string]any{
			"name":   "leak-probe",
			"scopes": []string{"tasks.read"},
		}, &mcpTok)

	// Create a task + comment so the task/timeline responses have
	// realistic payload to scan.
	var task struct {
		ID string `json:"id"`
	}
	doJSON(t, http.MethodPost, testServerURL+"/tasks", tt.AccessToken, map[string]any{
		"projectId": tt.ProjectPublicID,
		"title":     "Leak probe task",
	}, &task)
	doJSON(t, http.MethodPost, testServerURL+"/tasks/"+task.ID+"/comments",
		tt.AccessToken, map[string]any{"body": "nothing to see here"}, nil)

	// Endpoints to scan.
	gets := []string{
		"/me",
		"/workspaces",
		"/workspaces/" + tt.WorkspacePublicID,
		"/workspaces/" + tt.WorkspacePublicID + "/members",
		"/workspaces/" + tt.WorkspacePublicID + "/projects",
		"/projects/" + tt.ProjectPublicID,
		"/tasks?projectId=" + tt.ProjectPublicID,
		"/tasks/" + task.ID,
		"/tasks/" + task.ID + "/comments",
		"/tasks/" + task.ID + "/timeline",
		"/workspaces/" + tt.WorkspacePublicID + "/timeline",
		"/workspaces/" + tt.WorkspacePublicID + "/ai/providers",
		"/workspaces/" + tt.WorkspacePublicID + "/me/mcp-tokens",
		"/inbox?workspaceId=" + tt.WorkspacePublicID,
	}
	for _, path := range gets {
		status, body := doJSONStatus(t, http.MethodGet, testServerURL+path, tt.AccessToken, nil)
		require.GreaterOrEqualf(t, status, 200, "GET %s -> %d", path, status)
		require.Lessf(t, status, 300, "GET %s -> %d body=%s", path, status, string(body))
		assertNoSecretLeak(t, path, body, plaintextAPIKey, mcpTok.Token)
	}
}

// assertNoSecretLeak fails the test if body contains any tracked
// secret prefix or a specific known plaintext value. The prefix list
// comes from internal/ai.SecretPrefixes; we also check the exact
// plaintext API key and the plaintext MCP token we pushed in. We
// explicitly tolerate the "mcp_" prefix appearing in a tokenPrefix
// field, which is the intended masked display.
func assertNoSecretLeak(t *testing.T, path string, body []byte, plaintextKey, plaintextToken string) {
	t.Helper()
	s := string(body)
	require.False(t, strings.Contains(s, plaintextKey),
		"plaintext API key leaked in %s: %s", path, s)
	if plaintextToken != "" {
		require.False(t, strings.Contains(s, plaintextToken),
			"plaintext MCP token leaked in %s: %s", path, s)
	}

	for _, prefix := range ai.SecretPrefixes {
		if prefix == "mcp_" {
			// mcp_ is allowed to appear inside `tokenPrefix` fields in the
			// mcp-tokens list response, which is the intended masked
			// display. Skip this prefix for that path only.
			if strings.Contains(path, "/me/mcp-tokens") {
				continue
			}
		}
		require.Falsef(t, strings.Contains(s, prefix),
			"secret prefix %q leaked in %s: %s", prefix, path, s)
	}
}
