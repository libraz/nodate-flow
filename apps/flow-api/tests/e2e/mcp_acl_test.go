package e2e

import (
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/nodate-flow/nodate-flow/apps/flow-api/internal/auth"
)

// mintMCPToken creates an MCP token via the REST API for the supplied
// tenant with the requested scopes and returns the plaintext token.
// The scopes slice is forwarded verbatim so callers control the exact
// granted set, including read-only and read+write combinations.
func mintMCPToken(t *testing.T, accessToken, workspacePublicID, name string, scopes []string) string {
	t.Helper()
	var resp struct {
		Token string `json:"token"`
	}
	doJSON(t, http.MethodPost,
		testServerURL+"/workspaces/"+workspacePublicID+"/me/mcp-tokens",
		accessToken, map[string]any{
			"name":   name,
			"scopes": scopes,
		}, &resp)
	require.True(t, strings.HasPrefix(resp.Token, "mcp_"), "token must have mcp_ prefix")
	return resp.Token
}

// TestMCPTokenUnknown asserts that a fabricated bearer token is
// rejected with HTTP 401 and the stable error code MCP.TOKEN.UNKNOWN.
// The MCP transport runs the bearer through HashOpaque before looking
// it up in mcp_tokens, so a token that does not match any row hits the
// sql.ErrNoRows branch in authenticate() and surfaces as UNKNOWN.
func TestMCPTokenUnknown(t *testing.T) {
	bootstrap(t)
	t.Parallel()

	status, body := mcpCallRaw(t, "mcp_doesnotexist_"+randomHex(8), "tools/list", nil)
	require.Equal(t, http.StatusUnauthorized, status,
		"unknown token must be rejected with 401, got body=%s", string(body))
	require.Equal(t, "MCP.TOKEN.UNKNOWN", mcpErrorCode(t, body),
		"unknown token must surface MCP.TOKEN.UNKNOWN, body=%s", string(body))
}

// TestMCPTokenExpired mints a real token, then directly back-dates its
// expires_at column via SQL. The next /mcp call must come back with
// MCP.TOKEN.EXPIRED at HTTP 401 because authenticate() compares
// expires_at against time.Now and rejects past-due rows.
func TestMCPTokenExpired(t *testing.T) {
	bootstrap(t)
	t.Parallel()

	tt := newTenant(t)
	tok := mintMCPToken(t, tt.AccessToken, tt.WorkspacePublicID,
		"expired-token", []string{"read:workspace"})

	// Back-date expires_at on the freshly-minted row. The plaintext token
	// is hashed with auth.HashOpaque before storage, mirroring what the
	// MCP authenticate() codepath uses for lookup.
	res, err := testDB.Exec(
		`UPDATE mcp_tokens SET expires_at = ? WHERE token_hash = ?`,
		time.Now().Add(-time.Hour).UTC(),
		auth.HashOpaque(tok),
	)
	require.NoError(t, err)
	rows, err := res.RowsAffected()
	require.NoError(t, err)
	require.Equal(t, int64(1), rows, "expected to update exactly the freshly-minted token row")

	status, body := mcpCallRaw(t, tok, "tools/list", nil)
	require.Equal(t, http.StatusUnauthorized, status,
		"expired token must be rejected with 401, got body=%s", string(body))
	require.Equal(t, "MCP.TOKEN.EXPIRED", mcpErrorCode(t, body),
		"expired token must surface MCP.TOKEN.EXPIRED, body=%s", string(body))
}

// TestMCPTokenWorkspaceMismatch verifies that an MCP token bound to
// workspace A cannot operate on a project that belongs to workspace B.
// The cross-workspace fence lives in resolveProject (apps/flow-api/
// internal/mcp/acl.go) which compares the resolved project's
// workspace_id against the session's workspace_id and emits
// MCP.TOKEN.WORKSPACE_MISMATCH on divergence.
//
// We pick create_task because runGetTask scopes its FindTaskByPublicId
// query by session workspace and surfaces WS.TASK.NOT_FOUND for cross-
// workspace ids; only resolveProject emits the dedicated mismatch code.
//
// Application-layer rejection: auth + frame parse have already
// succeeded, so writeRPCAppError returns HTTP 200 with the JSON-RPC
// envelope carrying error.data.code = "MCP.TOKEN.WORKSPACE_MISMATCH".
func TestMCPTokenWorkspaceMismatch(t *testing.T) {
	bootstrap(t)
	t.Parallel()

	tt1 := newTenant(t)
	tt2 := newTenant(t)

	tok := mintMCPToken(t, tt1.AccessToken, tt1.WorkspacePublicID,
		"mismatch-token", []string{"read:workspace", "write:workspace"})

	status, body := mcpCallRaw(t, tok, "tools/call", map[string]any{
		"name": "create_task",
		"arguments": map[string]any{
			"projectId": tt2.ProjectPublicID,
			"title":     "cross-tenant create attempt",
		},
	})
	require.Equal(t, http.StatusOK, status,
		"app-layer rejection must use HTTP 200 + envelope, body=%s", string(body))
	require.Equal(t, "MCP.TOKEN.WORKSPACE_MISMATCH", mcpErrorCode(t, body),
		"cross-workspace projectId must surface MCP.TOKEN.WORKSPACE_MISMATCH, body=%s", string(body))
}

// TestMCPScopeInsufficient mints a read-only MCP token and exercises a
// write-scoped tool (create_task, requiredScope "write:workspace"). The
// session's hasScope check in handleToolCall must reject the call with
// MCP.SCOPE.INSUFFICIENT.
//
// Application-layer rejection: auth + frame parse have already
// succeeded, so writeRPCAppError returns HTTP 200 with the JSON-RPC
// envelope carrying error.data.code = "MCP.SCOPE.INSUFFICIENT".
func TestMCPScopeInsufficient(t *testing.T) {
	bootstrap(t)
	t.Parallel()

	tt := newTenant(t)
	tok := mintMCPToken(t, tt.AccessToken, tt.WorkspacePublicID,
		"read-only-token", []string{"read:workspace"})

	status, body := mcpCallRaw(t, tok, "tools/call", map[string]any{
		"name": "create_task",
		"arguments": map[string]any{
			"projectId": tt.ProjectPublicID,
			"title":     "insufficient scope attempt",
		},
	})
	require.Equal(t, http.StatusOK, status,
		"app-layer rejection must use HTTP 200 + envelope, body=%s", string(body))
	require.Equal(t, "MCP.SCOPE.INSUFFICIENT", mcpErrorCode(t, body),
		"read-only token must surface MCP.SCOPE.INSUFFICIENT, body=%s", string(body))
}

// TestMCPSearchTasksAppliesTaskVisibilityFilter locks in that search_tasks
// applies the same Layer-4 task visibility rules as list_tasks and REST task
// list/search surfaces. A workspace member without a project_members row may
// find public tasks, but must not enumerate project/private tasks by title.
func TestMCPSearchTasksAppliesTaskVisibilityFilter(t *testing.T) {
	bootstrap(t)
	t.Parallel()

	owner := newTenant(t)
	member := seedWorkspaceMemberWithoutProjectRole(t, owner)
	tok := mintMCPToken(t, member.AccessToken, owner.WorkspacePublicID,
		"search-visibility-token", []string{"read:workspace"})

	var publicTask, projectTask, privateTask struct {
		ID string `json:"id"`
	}
	doJSON(t, http.MethodPost, testServerURL+"/tasks", owner.AccessToken, map[string]any{
		"projectId":  owner.ProjectPublicID,
		"title":      "mcp visibility needle public",
		"visibility": "public",
	}, &publicTask)
	doJSON(t, http.MethodPost, testServerURL+"/tasks", owner.AccessToken, map[string]any{
		"projectId":  owner.ProjectPublicID,
		"title":      "mcp visibility needle project",
		"visibility": "project",
	}, &projectTask)
	doJSON(t, http.MethodPost, testServerURL+"/tasks", owner.AccessToken, map[string]any{
		"projectId":  owner.ProjectPublicID,
		"title":      "mcp visibility needle private",
		"visibility": "private",
	}, &privateTask)
	require.NotEmpty(t, publicTask.ID)
	require.NotEmpty(t, projectTask.ID)
	require.NotEmpty(t, privateTask.ID)

	body := mcpCall(t, tok, "tools/call", map[string]any{
		"name": "search_tasks",
		"arguments": map[string]any{
			"query": "mcp visibility needle",
			"limit": 10,
		},
	})
	result := mcpToolTextJSON[struct {
		Tasks []struct {
			ID    string `json:"id"`
			Title string `json:"title"`
		} `json:"tasks"`
	}](t, body)
	seenTasks := map[string]string{}
	for _, task := range result.Tasks {
		seenTasks[task.ID] = task.Title
	}
	require.Equal(t, "mcp visibility needle public", seenTasks[publicTask.ID],
		"public task must be searchable by workspace member")
	require.NotContains(t, seenTasks, projectTask.ID,
		"project-visibility task must not be searchable without project membership")
	require.NotContains(t, seenTasks, privateTask.ID,
		"private task must not be searchable without actor/creator access")
}

// TestMCPMemberRemoved disables the workspace_members row for the
// session user after the token was minted. The next /mcp call hits
// requireWorkspaceMember -> acl.CheckWorkspaceMember which returns
// WS.WORKSPACE.ACCESS_DENIED when the member row is absent or disabled.
//
// Application-layer rejection: auth + frame parse have already
// succeeded, so writeRPCAppError returns HTTP 200 with the JSON-RPC
// envelope carrying error.data.code = "WS.WORKSPACE.ACCESS_DENIED".
func TestMCPMemberRemoved(t *testing.T) {
	bootstrap(t)
	t.Parallel()

	tt := newTenant(t)
	tok := mintMCPToken(t, tt.AccessToken, tt.WorkspacePublicID,
		"to-be-removed", []string{"read:workspace", "write:workspace"})

	// Create a task while still a member so we have a valid target id
	// for the get_task call. The test exercises the ACL gate, not the
	// 404 path.
	var task struct {
		ID string `json:"id"`
	}
	doJSON(t, http.MethodPost, testServerURL+"/tasks", tt.AccessToken, map[string]any{
		"projectId": tt.ProjectPublicID,
		"title":     "target before removal",
	}, &task)
	require.NotEmpty(t, task.ID)

	// Disable the membership row for this user in this workspace. The
	// membership lookup uses internal numeric ids; resolve them via
	// public_id so the test never hard-codes an internal connector id.
	res, err := testDB.Exec(
		`UPDATE workspace_members
		   SET enabled = FALSE
		 WHERE workspace_id = (SELECT id FROM workspaces WHERE public_id = UUID_TO_BIN(?, 0))
		   AND user_id      = (SELECT id FROM users      WHERE public_id = UUID_TO_BIN(?, 0))`,
		tt.WorkspacePublicID, tt.UserPublicID,
	)
	require.NoError(t, err)
	rows, err := res.RowsAffected()
	require.NoError(t, err)
	require.Equal(t, int64(1), rows, "expected exactly one workspace_members row to flip")

	status, body := mcpCallRaw(t, tok, "tools/call", map[string]any{
		"name":      "get_task",
		"arguments": map[string]any{"taskId": task.ID},
	})
	require.Equal(t, http.StatusOK, status,
		"app-layer rejection must use HTTP 200 + envelope, body=%s", string(body))
	require.Equal(t, "WS.WORKSPACE.ACCESS_DENIED", mcpErrorCode(t, body),
		"disabled member must surface WS.WORKSPACE.ACCESS_DENIED, body=%s", string(body))
}

// TestMCPUnknownToolReturns200WithError locks in the JSON-RPC 2.0 /
// MCP convention that application-layer errors (here: an unknown tool
// name dispatched after auth + frame parse already succeeded) are
// returned as HTTP 200 with the JSON-RPC envelope carrying the stable
// error code in error.data.code. This is a regression guard against
// future drift back to HTTP-status-bearing app errors.
func TestMCPUnknownToolReturns200WithError(t *testing.T) {
	bootstrap(t)
	t.Parallel()

	tt := newTenant(t)
	tok := mintMCPToken(t, tt.AccessToken, tt.WorkspacePublicID,
		"unknown-tool-token", []string{"read:workspace", "write:workspace"})

	status, body := mcpCallRaw(t, tok, "tools/call", map[string]any{
		"name":      "nonexistent_tool_xyz",
		"arguments": map[string]any{},
	})
	require.Equal(t, http.StatusOK, status,
		"unknown tool must use HTTP 200 + envelope, body=%s", string(body))
	require.Equal(t, "MCP.TOOL.NOT_FOUND", mcpErrorCode(t, body),
		"unknown tool must surface MCP.TOOL.NOT_FOUND, body=%s", string(body))
}
