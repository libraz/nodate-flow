package mcp_test

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/libraz/nodate-flow/apps/flow-api/internal/auth"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/generated"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/mcp"
	"github.com/libraz/nodate-flow/apps/flow-api/tests/helpers"
)

// seedMCPToken mints a never-expiring MCP token for an existing
// workspace member and returns the plaintext plus the internal row id.
func seedMCPToken(t *testing.T, db *sql.DB, wsID, userID uint32, scopes []string) (string, uint32) {
	t.Helper()
	return seedMCPTokenAt(t, db, wsID, userID, scopes, sql.NullTime{})
}

// seedMCPTokenAt is [seedMCPToken] with an explicit expiry. An invalid
// sql.NullTime means the token never expires, which is what every token
// issued before expiry was settable had to be.
func seedMCPTokenAt(t *testing.T, db *sql.DB, wsID, userID uint32, scopes []string, expiresAt sql.NullTime) (string, uint32) {
	t.Helper()
	plain, hash, err := auth.GenerateMCP()
	require.NoError(t, err)
	scopesJSON, err := json.Marshal(scopes)
	require.NoError(t, err)
	pub := uuid.Must(uuid.NewV7())
	res, err := db.ExecContext(context.Background(),
		`INSERT INTO mcp_tokens (public_id, workspace_id, user_id, name, token_hash, token_prefix, scopes_json, expires_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		pub[:], wsID, userID, "wiring token", hash, plain[:8], scopesJSON, expiresAt)
	require.NoError(t, err)
	id, err := res.LastInsertId()
	require.NoError(t, err)
	return plain, uint32(id) //#nosec G115 -- LastInsertId in test seed, fits uint32
}

// postToolCall sends one tools/call frame and returns the HTTP status
// and body.
func postToolCall(t *testing.T, baseURL, token, name string, args map[string]any) (int, []byte) {
	t.Helper()
	frame, err := json.Marshal(map[string]any{
		"jsonrpc": "2.0",
		"id":      1,
		"method":  "tools/call",
		"params":  map[string]any{"name": name, "arguments": args},
	})
	require.NoError(t, err)

	req, err := http.NewRequestWithContext(context.Background(),
		http.MethodPost, baseURL+"/mcp", bytes.NewReader(frame))
	require.NoError(t, err)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+token)

	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer func() { _ = resp.Body.Close() }()
	body, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	return resp.StatusCode, body
}

// toolCallErrorCode reads the stable error code out of a JSON-RPC
// envelope, or "" when the call succeeded.
func toolCallErrorCode(t *testing.T, body []byte) string {
	t.Helper()
	var env struct {
		Error *struct {
			Data struct {
				Code string `json:"code"`
			} `json:"data"`
		} `json:"error"`
	}
	require.NoError(t, json.Unmarshal(body, &env), "raw=%s", string(body))
	if env.Error == nil {
		return ""
	}
	return env.Error.Data.Code
}

// mcpWiringFixture is a workspace member with a project, one task, and a
// write-scoped MCP token.
type mcpWiringFixture struct {
	wsID           uint32
	userID         uint32
	projectPub     uuid.UUID
	taskPub        uuid.UUID
	taskInternalID uint32
	token          string
}

func seedMCPWiringFixture(t *testing.T, db *sql.DB) *mcpWiringFixture {
	t.Helper()
	ctx := context.Background()
	suffix := uuid.New().String()[:8]

	userPub := uuid.Must(uuid.NewV7())
	res, err := db.ExecContext(ctx,
		`INSERT INTO users (public_id, email, display_name, locale) VALUES (?, ?, ?, 'en')`,
		userPub[:], "mcpwiring-"+suffix+"@example.test", "MCPWiring "+suffix)
	require.NoError(t, err)
	userID64, err := res.LastInsertId()
	require.NoError(t, err)
	userID := uint32(userID64) //#nosec G115 -- LastInsertId in test seed, fits uint32

	wsPub := uuid.Must(uuid.NewV7())
	res, err = db.ExecContext(ctx,
		`INSERT INTO workspaces (public_id, slug, name) VALUES (?, ?, ?)`,
		wsPub[:], "mcpwiring-ws-"+suffix, "MCPWiring Workspace")
	require.NoError(t, err)
	wsID64, err := res.LastInsertId()
	require.NoError(t, err)
	wsID := uint32(wsID64) //#nosec G115 -- LastInsertId in test seed, fits uint32

	memberPub := uuid.Must(uuid.NewV7())
	_, err = db.ExecContext(ctx,
		`INSERT INTO workspace_members (public_id, workspace_id, user_id, role) VALUES (?, ?, ?, 'owner')`,
		memberPub[:], wsID, userID)
	require.NoError(t, err)

	prjPub := uuid.Must(uuid.NewV7())
	res, err = db.ExecContext(ctx,
		`INSERT INTO projects (public_id, workspace_id, slug, name, identifier) VALUES (?, ?, ?, ?, ?)`,
		prjPub[:], wsID, "mcpwiring-prj-"+suffix, "MCPWiring Project", "MW"+suffix[:3])
	require.NoError(t, err)
	prjID64, err := res.LastInsertId()
	require.NoError(t, err)

	taskPub := uuid.Must(uuid.NewV7())
	res, err = db.ExecContext(ctx,
		`INSERT INTO tasks (public_id, workspace_id, project_id, task_number, title, visibility, created_by_user_id)
		 VALUES (?, ?, ?, 1, ?, 'public', ?)`,
		taskPub[:], wsID, prjID64, "Wiring task", userID)
	require.NoError(t, err)
	taskID64, err := res.LastInsertId()
	require.NoError(t, err)

	token, _ := seedMCPToken(t, db, wsID, userID, []string{"write:workspace"})

	return &mcpWiringFixture{
		wsID:           wsID,
		userID:         userID,
		projectPub:     prjPub,
		taskPub:        taskPub,
		taskInternalID: uint32(taskID64), //#nosec G115 -- LastInsertId in test seed, fits uint32
		token:          token,
	}
}

// TestToolCallEnforcesSchemaOverTheWire proves the schema check is
// wired into the dispatch path, not merely available to be called.
//
// The unit tests around validateArgsAgainstSchema prove the rule; they
// would all still pass if handleToolCall never invoked it, which is
// exactly the state the audit found — a schema that described the tool
// and constrained nothing. This test goes through the transport.
func TestToolCallEnforcesSchemaOverTheWire(t *testing.T) {
	requireMCPIntegration(t)
	inst := helpers.StartShared(t)
	db := inst.DB
	fx := seedMCPWiringFixture(t, db)

	handler := mcp.NewHandler(mcp.Deps{DB: db, Queries: generated.New(db)})
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)

	t.Run("priority_out_of_range/refused", func(t *testing.T) {
		status, body := postToolCall(t, srv.URL, fx.token, "create_task", map[string]any{
			"projectId": fx.projectPub.String(),
			"title":     "Out of range",
			"priority":  999,
		})
		require.Equal(t, http.StatusOK, status,
			"an application-layer rejection stays inside the JSON-RPC envelope, body=%s", string(body))
		require.Equal(t, "MCP.TOOL.ARGUMENTS_INVALID", toolCallErrorCode(t, body),
			"priority 999 is outside the advertised 0..4 and must not reach the tool, body=%s", string(body))

		// The task must not exist. A rejection that still writes the row
		// would satisfy the assertion above and fix nothing.
		var n int
		require.NoError(t, db.QueryRow(
			`SELECT COUNT(*) FROM tasks WHERE workspace_id = ? AND title = 'Out of range'`,
			fx.wsID).Scan(&n))
		require.Zero(t, n, "a refused create_task must not have created anything")
	})

	t.Run("priority_in_range/reaches_the_tool", func(t *testing.T) {
		// The positive control. Without it, a validator that refused every
		// call would pass the case above.
		status, body := postToolCall(t, srv.URL, fx.token, "create_task", map[string]any{
			"projectId": fx.projectPub.String(),
			"title":     "In range",
			"priority":  2,
		})
		require.Equal(t, http.StatusOK, status)
		require.Empty(t, toolCallErrorCode(t, body),
			"priority 2 is inside the advertised range and must be accepted, body=%s", string(body))

		var n int
		require.NoError(t, db.QueryRow(
			`SELECT COUNT(*) FROM tasks WHERE workspace_id = ? AND title = 'In range' AND priority = 2`,
			fx.wsID).Scan(&n))
		require.Equal(t, 1, n, "the accepted call must have created the task it described")
	})

	t.Run("hyphenated_public_id/reaches_the_tool", func(t *testing.T) {
		// The form every API response carries. The advertised pattern used
		// to describe a form the server never emits, and enforcing it
		// refused every real id until the pattern was corrected.
		status, body := postToolCall(t, srv.URL, fx.token, "get_task", map[string]any{
			"taskId": fx.taskPub.String(),
		})
		require.Equal(t, http.StatusOK, status)
		require.Empty(t, toolCallErrorCode(t, body),
			"a canonical public id must not be refused by its own schema, body=%s", string(body))
	})
}

// TestExpiredTokenIsRefused proves the MCP token expiry check actually
// fires.
//
// The check has been in acl.go the whole time and was unreachable in
// practice: nothing could set mcp_tokens.expires_at, so every token ever
// issued was perpetual and the branch guarding expiry had no way to be
// taken. Now that the create route accepts an expiry, the branch is
// live, and this is what says so — an expiry that is written but never
// enforced is the same defect wearing the opposite face.
func TestExpiredTokenIsRefused(t *testing.T) {
	requireMCPIntegration(t)
	inst := helpers.StartShared(t)
	db := inst.DB
	fx := seedMCPWiringFixture(t, db)

	handler := mcp.NewHandler(mcp.Deps{DB: db, Queries: generated.New(db)})
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)

	call := func(t *testing.T, token string) (int, []byte) {
		t.Helper()
		return postToolCall(t, srv.URL, token, "get_task", map[string]any{
			"taskId": fx.taskPub.String(),
		})
	}

	t.Run("past_expiry/refused", func(t *testing.T) {
		token, _ := seedMCPTokenAt(t, db, fx.wsID, fx.userID, []string{"read:workspace"},
			sql.NullTime{Time: time.Now().Add(-time.Hour).UTC(), Valid: true})
		status, body := call(t, token)
		require.Equal(t, http.StatusUnauthorized, status,
			"an expired credential is a transport-layer rejection, body=%s", string(body))
		require.Equal(t, "MCP.TOKEN.EXPIRED", toolCallErrorCode(t, body), "body=%s", string(body))
	})

	t.Run("future_expiry/accepted", func(t *testing.T) {
		// The control that separates "expiry is enforced" from "any token
		// carrying an expiry is refused".
		token, _ := seedMCPTokenAt(t, db, fx.wsID, fx.userID, []string{"read:workspace"},
			sql.NullTime{Time: time.Now().Add(24 * time.Hour).UTC(), Valid: true})
		status, body := call(t, token)
		require.Equal(t, http.StatusOK, status, "body=%s", string(body))
		require.Empty(t, toolCallErrorCode(t, body),
			"a token that has not expired must still work, body=%s", string(body))
	})

	t.Run("no_expiry/accepted", func(t *testing.T) {
		token, _ := seedMCPToken(t, db, fx.wsID, fx.userID, []string{"read:workspace"})
		status, body := call(t, token)
		require.Equal(t, http.StatusOK, status, "body=%s", string(body))
		require.Empty(t, toolCallErrorCode(t, body),
			"omitting an expiry must keep the old perpetual behaviour, body=%s", string(body))
	})
}

// TestToolCallAttributesInvocationToItsTask proves mcp_invocations.task_id
// carries the task a call acted on.
//
// The column existed and was written as NULL on every row, so the audit
// trail could say an agent called get_task and not which task it read.
// "What did the agent do to this task" is the question an AI-native
// product has to answer, and a column that is always NULL cannot.
func TestToolCallAttributesInvocationToItsTask(t *testing.T) {
	requireMCPIntegration(t)
	inst := helpers.StartShared(t)
	db := inst.DB
	fx := seedMCPWiringFixture(t, db)

	handler := mcp.NewHandler(mcp.Deps{DB: db, Queries: generated.New(db)})
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)

	status, body := postToolCall(t, srv.URL, fx.token, "get_task", map[string]any{
		"taskId": fx.taskPub.String(),
	})
	require.Equal(t, http.StatusOK, status)
	require.Empty(t, toolCallErrorCode(t, body), "body=%s", string(body))

	var attributed int
	require.NoError(t, db.QueryRow(
		`SELECT COUNT(*) FROM mcp_invocations
		  WHERE workspace_id = ? AND tool_name = 'get_task' AND task_id = ?`,
		fx.wsID, fx.taskInternalID).Scan(&attributed))
	require.Equal(t, 1, attributed,
		"the invocation row must name the task the call resolved, not NULL")

	// A tool that touches no task must leave the column NULL rather than
	// borrowing whatever id happened to be nearby.
	status, body = postToolCall(t, srv.URL, fx.token, "list_projects", map[string]any{})
	require.Equal(t, http.StatusOK, status)
	require.Empty(t, toolCallErrorCode(t, body), "body=%s", string(body))

	var unattributed int
	require.NoError(t, db.QueryRow(
		`SELECT COUNT(*) FROM mcp_invocations
		  WHERE workspace_id = ? AND tool_name = 'list_projects' AND task_id IS NULL`,
		fx.wsID).Scan(&unattributed))
	require.Equal(t, 1, unattributed,
		"a call that resolved no task must record NULL, or the attribution means nothing")
}
