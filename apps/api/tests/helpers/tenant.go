package helpers

import (
	"bytes"
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// TestTenant is the bundle of identifiers and tokens that a single
// integration test needs in order to act as an isolated user. Each
// helper test creates its own TestTenant so that parallel tests cannot
// observe each other's state.
//
// Every tenant owns a dedicated workspace (where the tenant user is the
// owner) and a single default project inside it. Tests that need
// additional projects or cross-tenant scenarios can create them via the
// API using the AccessToken.
type TestTenant struct {
	BaseURL           string
	Email             string
	Password          string
	DisplayName       string
	UserPublicID      string
	WorkspacePublicID string
	WorkspaceSlug     string
	ProjectPublicID   string
	ProjectSlug       string
	AccessToken       string
	RefreshToken      string
}

// CreateTestTenant registers a fresh user via POST /auth/register and
// stores the returned tokens. The email is randomized so multiple
// parallel calls within the same test binary cannot collide.
func CreateTestTenant(t *testing.T, baseURL string) *TestTenant {
	t.Helper()

	suffix := randomHex(8)
	tt := &TestTenant{
		BaseURL:     baseURL,
		Email:       fmt.Sprintf("test+%s@example.test", suffix),
		Password:    "correct horse battery staple",
		DisplayName: "Test User " + suffix,
	}

	body := map[string]any{
		"email":       tt.Email,
		"password":    tt.Password,
		"displayName": tt.DisplayName,
		"locale":      "en",
	}

	var resp struct {
		AccessToken string `json:"accessToken"`
		ExpiresAt   int64  `json:"expiresAt"`
		UserID      string `json:"userId"`
	}
	refresh := doJSONCapturingRefreshCookie(t, http.MethodPost, baseURL+"/auth/register", "", "", body, &resp)
	require.NotEmpty(t, resp.AccessToken, "register did not return access token")
	require.NotEmpty(t, refresh, "register did not set nf_rt refresh cookie")
	require.NotEmpty(t, resp.UserID, "register did not return user id")

	tt.AccessToken = resp.AccessToken
	tt.RefreshToken = refresh
	tt.UserPublicID = resp.UserID

	// Create a workspace owned by this tenant.
	tt.WorkspaceSlug = "ws-" + suffix
	wsBody := map[string]any{
		"slug": tt.WorkspaceSlug,
		"name": "Test Workspace " + suffix,
	}
	var wsResp struct {
		ID   string `json:"id"`
		Slug string `json:"slug"`
	}
	doJSON(t, http.MethodPost, baseURL+"/workspaces", tt.AccessToken, wsBody, &wsResp)
	require.NotEmpty(t, wsResp.ID, "workspace create did not return id")
	tt.WorkspacePublicID = wsResp.ID

	// Create a default project inside that workspace.
	tt.ProjectSlug = "prj-" + suffix
	prjBody := map[string]any{
		"slug": tt.ProjectSlug,
		"name": "Test Project " + suffix,
	}
	var prjResp struct {
		ID   string `json:"id"`
		Slug string `json:"slug"`
	}
	doJSON(t, http.MethodPost,
		baseURL+"/workspaces/"+tt.WorkspacePublicID+"/projects",
		tt.AccessToken, prjBody, &prjResp)
	require.NotEmpty(t, prjResp.ID, "project create did not return id")
	tt.ProjectPublicID = prjResp.ID

	return tt
}

// CleanupTenant revokes the tenant's session via POST /auth/logout. It
// is best-effort: a logout failure must not mask the underlying test
// failure, so errors are logged but not asserted.
//
// DB-level cleanup of the tenant's workspace data must be done via
// PurgeWorkspace, which is the single sanctioned direct-SQL exception
// for tests until the workspace API exposes deletion.
func CleanupTenant(t *testing.T, tt *TestTenant) {
	t.Helper()
	if tt == nil || tt.BaseURL == "" {
		return
	}
	req := newJSONRequest(t, http.MethodPost, tt.BaseURL+"/auth/logout", tt.AccessToken, nil)
	if tt.RefreshToken != "" {
		req.AddCookie(&http.Cookie{Name: "nf_rt", Value: tt.RefreshToken})
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Logf("logout request failed: %v", err)
		return
	}
	_, _ = io.Copy(io.Discard, resp.Body)
	_ = resp.Body.Close()
}

// PurgeWorkspace removes every row that belongs to the supplied
// workspace public id, in foreign-key order, using direct SQL.
//
// IMPORTANT: this is the ONLY direct-SQL exception in the test suite.
// It exists because the workspace API does not yet expose a delete
// operation. Once 1.API-2 lands, callers should switch to that route
// and PurgeWorkspace should be deleted.
//
// The implementation toggles FOREIGN_KEY_CHECKS off so deletion order
// does not need to follow the dependency graph by hand. The list of
// tables intentionally enumerates every workspace-scoped table; tables
// that are workspace-scoped indirectly (e.g. comments via tasks) are
// pruned by the parent CASCADE.
func PurgeWorkspace(t *testing.T, db *sql.DB, workspacePublicID string) {
	t.Helper()
	if workspacePublicID == "" {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	tx, err := db.BeginTx(ctx, nil)
	require.NoError(t, err)
	defer func() { _ = tx.Rollback() }()

	if _, err := tx.ExecContext(ctx, "SET FOREIGN_KEY_CHECKS = 0"); err != nil {
		require.NoError(t, err)
	}

	// Lookup the internal id once.
	var wsID uint32
	err = tx.QueryRowContext(ctx,
		`SELECT id FROM workspaces WHERE public_id = UUID_TO_BIN(?, 1) LIMIT 1`,
		workspacePublicID).Scan(&wsID)
	if err == sql.ErrNoRows {
		// Nothing to purge; restore checks and return.
		_, _ = tx.ExecContext(ctx, "SET FOREIGN_KEY_CHECKS = 1")
		require.NoError(t, tx.Commit())
		return
	}
	require.NoError(t, err)

	// FK order: leaf rows first, parents last. Tables not directly
	// keyed on workspace_id are deleted via their parent (tasks,
	// projects) explicitly to avoid leaving orphans.
	stmts := []string{
		`DELETE FROM signals          WHERE workspace_id = ?`,
		`DELETE FROM events           WHERE workspace_id = ?`,
		`DELETE FROM ai_invocations   WHERE workspace_id = ?`,
		`DELETE FROM mcp_invocations  WHERE workspace_id = ?`,
		`DELETE FROM mcp_tokens       WHERE workspace_id = ?`,
		`DELETE FROM ai_agents        WHERE workspace_id = ?`,
		`DELETE FROM ai_models        WHERE workspace_id = ?`,
		`DELETE FROM ai_providers     WHERE workspace_id = ?`,
		`DELETE FROM embeddings       WHERE workspace_id = ?`,
		`DELETE FROM attachments      WHERE workspace_id = ?`,
		`DELETE FROM comments         WHERE workspace_id = ?`,
		`DELETE FROM task_constraints WHERE workspace_id = ?`,
		`DELETE FROM task_dependencies WHERE workspace_id = ?`,
		`DELETE FROM task_actors      WHERE workspace_id = ?`,
		`DELETE FROM tasks            WHERE workspace_id = ?`,
		`DELETE FROM project_members  WHERE workspace_id = ?`,
		`DELETE FROM projects         WHERE workspace_id = ?`,
		`DELETE FROM audit_logs       WHERE workspace_id = ?`,
		`DELETE FROM workspace_members WHERE workspace_id = ?`,
		`DELETE FROM workspaces       WHERE id = ?`,
	}
	for _, q := range stmts {
		if _, err := tx.ExecContext(ctx, q, wsID); err != nil {
			t.Logf("PurgeWorkspace: %q: %v", q, err)
		}
	}

	if _, err := tx.ExecContext(ctx, "SET FOREIGN_KEY_CHECKS = 1"); err != nil {
		require.NoError(t, err)
	}
	require.NoError(t, tx.Commit())
}

// doJSON sends a JSON request and decodes the JSON response body into
// out. It fails the test on transport errors or non-2xx responses.
func doJSON(t *testing.T, method, url, bearer string, body any, out any) {
	t.Helper()
	req := newJSONRequest(t, method, url, bearer, body)
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err, "%s %s", method, url)
	defer func() { _ = resp.Body.Close() }()

	raw, err := io.ReadAll(resp.Body)
	require.NoError(t, err)

	require.GreaterOrEqualf(t, resp.StatusCode, 200, "%s %s -> %d body=%s", method, url, resp.StatusCode, string(raw))
	require.Lessf(t, resp.StatusCode, 300, "%s %s -> %d body=%s", method, url, resp.StatusCode, string(raw))

	if out != nil && len(raw) > 0 {
		require.NoError(t, json.Unmarshal(raw, out), "decode %s %s body=%s", method, url, string(raw))
	}
}

// newJSONRequest builds an *http.Request with the given JSON body and
// optional bearer authorization header.
func newJSONRequest(t *testing.T, method, url, bearer string, body any) *http.Request {
	t.Helper()
	var rdr io.Reader
	if body != nil {
		buf, err := json.Marshal(body)
		require.NoError(t, err)
		rdr = bytes.NewReader(buf)
	}
	req, err := http.NewRequest(method, url, rdr)
	require.NoError(t, err)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	req.Header.Set("Accept", "application/json")
	if bearer != "" {
		req.Header.Set("Authorization", "Bearer "+bearer)
	}
	return req
}

// doJSONCapturingRefreshCookie sends a JSON request and additionally
// extracts the value of the nf_rt Set-Cookie header from the response,
// returning it to the caller. If refreshCookie is non-empty it is
// attached to the outbound request so callers can drive /auth/refresh
// and /auth/logout without plumbing a full cookie jar.
func doJSONCapturingRefreshCookie(t *testing.T, method, url, bearer, refreshCookie string, body any, out any) string {
	t.Helper()
	req := newJSONRequest(t, method, url, bearer, body)
	if refreshCookie != "" {
		req.AddCookie(&http.Cookie{Name: "nf_rt", Value: refreshCookie})
	}
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err, "%s %s", method, url)
	defer func() { _ = resp.Body.Close() }()

	raw, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	require.GreaterOrEqualf(t, resp.StatusCode, 200, "%s %s -> %d body=%s", method, url, resp.StatusCode, string(raw))
	require.Lessf(t, resp.StatusCode, 300, "%s %s -> %d body=%s", method, url, resp.StatusCode, string(raw))

	if out != nil && len(raw) > 0 {
		require.NoError(t, json.Unmarshal(raw, out), "decode %s %s body=%s", method, url, string(raw))
	}

	for _, c := range resp.Cookies() {
		if c.Name == "nf_rt" {
			return c.Value
		}
	}
	return ""
}

// randomHex returns 2*n hex characters from crypto/rand.
func randomHex(n int) string {
	b := make([]byte, n)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}
