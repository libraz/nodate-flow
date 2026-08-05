package e2e

import (
	"database/sql"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/libraz/nodate-flow/apps/flow-api/internal/auth"
)

// TestMCPTokenUnknownScopeRejected asserts that requesting an MCP token
// with a scope outside the supported allowlist (mcp.SupportedScopes) is
// rejected at issuance with a validation error, instead of silently
// storing a free-text scope that matches no tool. The known scope in the
// same request must not rescue the unknown one.
func TestMCPTokenUnknownScopeRejected(t *testing.T) {
	bootstrap(t)
	t.Parallel()

	tt := newTenant(t)
	status, body := doJSONStatus(t, http.MethodPost,
		testServerURL+"/workspaces/"+tt.WorkspacePublicID+"/me/mcp-tokens",
		tt.AccessToken, map[string]any{
			"name":   "bad-scope-token",
			"scopes": []string{"read:workspace", "delete:everything"},
		})
	require.Equal(t, http.StatusUnprocessableEntity, status,
		"unknown scope must be rejected at issuance, body=%s", string(body))
	require.Equal(t, "VALIDATION.BODY.FIELD_INVALID", decodeErrorCode(t, body),
		"unknown scope must surface VALIDATION.BODY.FIELD_INVALID, body=%s", string(body))
}

// TestMCPTokenAuthStampsLastUsed verifies that a successful MCP bearer
// authentication stamps mcp_tokens.last_used_at. The stamp is best-effort
// and asynchronous (fire-and-forget in authenticate), so the assertion
// polls until it lands rather than reading once.
func TestMCPTokenAuthStampsLastUsed(t *testing.T) {
	bootstrap(t)
	t.Parallel()

	tt := newTenant(t)
	tok := mintMCPToken(t, tt.AccessToken, tt.WorkspacePublicID,
		"stamp-token", []string{"read:workspace"})
	hash := auth.HashOpaque(tok)

	// A freshly minted token has never been used.
	var before sql.NullTime
	require.NoError(t, testDB.QueryRow(
		`SELECT last_used_at FROM mcp_tokens WHERE token_hash = ?`, hash,
	).Scan(&before))
	require.False(t, before.Valid, "new token must have NULL last_used_at")

	// A successful MCP auth (tools/list) must stamp last_used_at.
	status, body := mcpCallRaw(t, tok, "tools/list", nil)
	require.Equal(t, http.StatusOK, status,
		"tools/list must succeed, body=%s", string(body))

	require.Eventually(t, func() bool {
		var after sql.NullTime
		if err := testDB.QueryRow(
			`SELECT last_used_at FROM mcp_tokens WHERE token_hash = ?`, hash,
		).Scan(&after); err != nil {
			return false
		}
		return after.Valid
	}, 3*time.Second, 50*time.Millisecond,
		"successful auth must stamp mcp_tokens.last_used_at")
}
