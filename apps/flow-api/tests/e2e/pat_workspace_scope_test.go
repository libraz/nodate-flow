package e2e

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/nodate-flow/nodate-flow/packages/go-shared/authn"
)

func mintPatForWorkspace(t *testing.T, tenantAccessToken, userPublicID, workspacePublicID string) string {
	t.Helper()
	token := authn.PrefixPAT + randomHex(32)

	var userID, workspaceID uint32
	err := testDB.QueryRow(
		`SELECT id FROM users WHERE public_id = UUID_TO_BIN(?, 0)`,
		userPublicID,
	).Scan(&userID)
	require.NoError(t, err)
	err = testDB.QueryRow(
		`SELECT id FROM workspaces WHERE public_id = UUID_TO_BIN(?, 0)`,
		workspacePublicID,
	).Scan(&workspaceID)
	require.NoError(t, err)

	_, err = testDB.Exec(
		`INSERT INTO personal_access_tokens
		   (public_id, workspace_id, user_id, name, token_hash, token_prefix, scopes_json)
		 VALUES
		   (UUID_TO_BIN(UUID(), 0), ?, ?, 'e2e PAT', ?, ?, JSON_ARRAY('read:workspace', 'write:workspace'))`,
		workspaceID, userID, authn.HashOpaque(token), token[:8],
	)
	require.NoError(t, err)

	// Prove the user still has a normal JWT session; the PAT itself is
	// returned to the caller and used by the test below.
	require.NotEmpty(t, tenantAccessToken)
	return token
}

// TestPATIsConfinedToBoundWorkspace verifies that a PAT minted for one
// workspace cannot be replayed against another workspace the same user belongs
// to. Without the token workspace check, the owner role in workspace B would
// make this request succeed.
func TestPATIsConfinedToBoundWorkspace(t *testing.T) {
	bootstrap(t)
	t.Parallel()

	tt := newTenant(t)
	var secondWorkspace struct {
		ID string `json:"id"`
	}
	doJSON(t, http.MethodPost, testServerURL+"/workspaces", tt.AccessToken,
		map[string]any{
			"slug": "pat-scope-" + randomHex(6),
			"name": "PAT Scope B",
		}, &secondWorkspace)
	require.NotEmpty(t, secondWorkspace.ID)

	pat := mintPatForWorkspace(t, tt.AccessToken, tt.UserPublicID, tt.WorkspacePublicID)

	var firstProjects struct {
		Projects []struct {
			ID string `json:"id"`
		} `json:"projects"`
	}
	doJSON(t, http.MethodGet,
		testServerURL+"/workspaces/"+tt.WorkspacePublicID+"/projects",
		pat, nil, &firstProjects)

	status, raw := doJSONStatus(t, http.MethodGet,
		testServerURL+"/workspaces/"+secondWorkspace.ID+"/projects",
		pat, nil)
	require.Equal(t, http.StatusForbidden, status,
		"PAT bound to workspace A must be rejected for workspace B, body=%s", string(raw))
	require.Equal(t, "WS.WORKSPACE.ACCESS_DENIED", problemType(t, raw),
		"workspace mismatch must surface WS.WORKSPACE.ACCESS_DENIED, body=%s", string(raw))
}

// TestMCPBearerFallbackIsConfinedToBoundWorkspace verifies that an MCP token
// replayed as a REST bearer keeps the same workspace binding enforced by the
// MCP transport itself.
func TestMCPBearerFallbackIsConfinedToBoundWorkspace(t *testing.T) {
	bootstrap(t)
	t.Parallel()

	tt := newTenant(t)
	var secondWorkspace struct {
		ID string `json:"id"`
	}
	doJSON(t, http.MethodPost, testServerURL+"/workspaces", tt.AccessToken,
		map[string]any{
			"slug": "mcp-rest-scope-" + randomHex(6),
			"name": "MCP REST Scope B",
		}, &secondWorkspace)
	require.NotEmpty(t, secondWorkspace.ID)

	tok := mintMCPToken(t, tt.AccessToken, tt.WorkspacePublicID,
		"rest-fallback-scope", []string{"read:workspace", "write:workspace"})

	var firstProjects struct {
		Projects []struct {
			ID string `json:"id"`
		} `json:"projects"`
	}
	doJSON(t, http.MethodGet,
		testServerURL+"/workspaces/"+tt.WorkspacePublicID+"/projects",
		tok, nil, &firstProjects)

	status, raw := doJSONStatus(t, http.MethodGet,
		testServerURL+"/workspaces/"+secondWorkspace.ID+"/projects",
		tok, nil)
	require.Equal(t, http.StatusForbidden, status,
		"MCP token bound to workspace A must be rejected for REST workspace B, body=%s", string(raw))
	require.Equal(t, "MCP.TOKEN.WORKSPACE_MISMATCH", problemType(t, raw),
		"MCP REST replay mismatch must surface MCP.TOKEN.WORKSPACE_MISMATCH, body=%s", string(raw))
}
