package e2e

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/libraz/nodate-flow/apps/flow-api/tests/helpers"
)

// byDiscordHarness boots a dedicated server with the service token
// configured and registers a tenant on it. The internal lookup endpoint
// only admits the service token so it cannot share the package-level
// test server (which has no token configured).
type byDiscordHarness struct {
	baseURL string
	tenant  *helpers.TestTenant
}

func newByDiscordHarness(t *testing.T) *byDiscordHarness {
	t.Helper()
	srv, cleanup, err := helpers.NewTestServerWithServiceToken(testDB, serviceTokenFixture)
	require.NoError(t, err)
	t.Cleanup(cleanup)
	tenant := helpers.CreateTestTenant(t, srv.BaseURL)
	return &byDiscordHarness{baseURL: srv.BaseURL, tenant: tenant}
}

// getByDiscord sends GET /internal/users/by-discord/{snowflake} with
// the supplied bearer and returns the status code plus raw body. The
// caller chooses whether to send the service token, a user JWT, or no
// bearer at all.
func getByDiscord(t *testing.T, baseURL, bearer, snowflake string) (int, []byte) {
	t.Helper()
	req, err := http.NewRequest(http.MethodGet, baseURL+"/internal/users/by-discord/"+snowflake, nil)
	require.NoError(t, err)
	req.Header.Set("Accept", "application/json")
	if bearer != "" {
		req.Header.Set("Authorization", "Bearer "+bearer)
	}
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer func() { _ = resp.Body.Close() }()
	raw, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	return resp.StatusCode, raw
}

// insertDiscordIntegration inserts a user_integrations row binding the
// supplied snowflake to the tenant user. Mirrors what the OAuth
// callback handler would write once the personal Discord binding flow
// is live; the lookup endpoint reads the metadata_json.external_user_id
// key the row carries.
func insertDiscordIntegration(t *testing.T, userPublicID, snowflake string, enabled bool) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	userInternalID := lookupUserInternalID(ctx, t, testDB, userPublicID)

	publicID, err := uuid.NewV7()
	require.NoError(t, err)
	pubBin, err := publicID.MarshalBinary()
	require.NoError(t, err)

	metadata := fmt.Sprintf(`{"external_user_id":%q,"verified_at":"2026-05-18T00:00:00Z"}`, snowflake)

	_, err = testDB.ExecContext(ctx, `
		INSERT INTO user_integrations (
			public_id, user_id, provider,
			external_account_id, external_account_label, scopes,
			access_token_ciphertext, refresh_token_ciphertext, access_token_expires_at,
			metadata_json, enabled
		) VALUES (?, ?, 'discord', ?, ?, '', '', NULL, NULL, ?, ?)
	`, pubBin, userInternalID, "discord-"+snowflake, "Test Discord User", metadata, enabled)
	require.NoError(t, err)
}

// randomSnowflake returns a deterministic-looking 18-digit string. The
// handler rejects non-numeric inputs with 422, so the fixture has to
// satisfy ^[0-9]{1,32}$.
func randomSnowflake() string {
	b := make([]byte, 8)
	_, _ = rand.Read(b)
	// Take the hex, drop alpha chars, pad with zeros to 18 digits.
	h := hex.EncodeToString(b)
	out := make([]byte, 0, 18)
	for i := 0; i < len(h) && len(out) < 18; i++ {
		c := h[i]
		if c >= '0' && c <= '9' {
			out = append(out, c)
		}
	}
	for len(out) < 18 {
		out = append(out, '0')
	}
	// Snowflakes are 17-19 digits in practice; lead with a non-zero
	// digit so the value looks plausible.
	if out[0] == '0' {
		out[0] = '1'
	}
	return string(out)
}

// TestByDiscordResolvesBoundUser asserts that an enabled discord
// integration row whose metadata_json.external_user_id matches the path
// snowflake resolves to (userId, workspaceId) of the tenant that owns
// it. Service-token auth is the only admitted mode on this route.
func TestByDiscordResolvesBoundUser(t *testing.T) {
	bootstrap(t)
	t.Parallel()

	h := newByDiscordHarness(t)
	snowflake := randomSnowflake()
	insertDiscordIntegration(t, h.tenant.UserPublicID, snowflake, true)

	status, raw := getByDiscord(t, h.baseURL, serviceTokenFixture, snowflake)
	require.Equalf(t, http.StatusOK, status, "expected 200, got %d body=%s", status, string(raw))

	var out struct {
		UserID      string `json:"userId"`
		WorkspaceID string `json:"workspaceId"`
	}
	require.NoError(t, json.Unmarshal(raw, &out))
	require.Equal(t, h.tenant.UserPublicID, out.UserID)
	require.Equal(t, h.tenant.WorkspacePublicID, out.WorkspaceID)
}

// TestByDiscordReturns404OnMissingBinding asserts that a snowflake
// that does not appear in any user_integrations row returns 404 with
// the INTEGRATION.DISCORD.USER_NOT_FOUND code. The presence-discord
// gateway interprets this status as drop_no_user (expected, noisy).
func TestByDiscordReturns404OnMissingBinding(t *testing.T) {
	bootstrap(t)
	t.Parallel()

	h := newByDiscordHarness(t)
	// Snowflake that is well-formed but never inserted.
	status, raw := getByDiscord(t, h.baseURL, serviceTokenFixture, "987654321098765432")
	require.Equalf(t, http.StatusNotFound, status, "expected 404, got %d body=%s", status, string(raw))

	var envelope struct {
		Type   string `json:"type"`
		Status int    `json:"status"`
	}
	require.NoError(t, json.Unmarshal(raw, &envelope))
	require.Equal(t, "INTEGRATION.DISCORD.USER_NOT_FOUND", envelope.Type)
	require.Equal(t, http.StatusNotFound, envelope.Status)
}

// TestByDiscordReturns404OnDisabledIntegration asserts that a binding
// whose enabled=FALSE row matches the snowflake is treated identically
// to "no binding at all" — the 404 envelope hides soft-disable from
// callers so cross-tenant existence cannot be probed by toggling the
// flag.
func TestByDiscordReturns404OnDisabledIntegration(t *testing.T) {
	bootstrap(t)
	t.Parallel()

	h := newByDiscordHarness(t)
	snowflake := randomSnowflake()
	insertDiscordIntegration(t, h.tenant.UserPublicID, snowflake, false)

	status, _ := getByDiscord(t, h.baseURL, serviceTokenFixture, snowflake)
	require.Equal(t, http.StatusNotFound, status)
}

// TestByDiscordRejectsJWT asserts that a real-user JWT cannot reach the
// /internal/* endpoint even when the same user owns the integration
// being queried. The middleware admits the service token only —
// presenting any other bearer must 401.
func TestByDiscordRejectsJWT(t *testing.T) {
	bootstrap(t)
	t.Parallel()

	h := newByDiscordHarness(t)
	snowflake := randomSnowflake()
	insertDiscordIntegration(t, h.tenant.UserPublicID, snowflake, true)

	status, raw := getByDiscord(t, h.baseURL, h.tenant.AccessToken, snowflake)
	require.Equalf(t, http.StatusUnauthorized, status,
		"JWT must be rejected on /internal/*: got %d body=%s", status, string(raw))
}

// TestByDiscordRejectsMissingBearer asserts that requests with no
// Authorization header at all also 401. Empty enabled=true config
// means RequireServiceTokenOnly must short-circuit on missing input.
func TestByDiscordRejectsMissingBearer(t *testing.T) {
	bootstrap(t)
	t.Parallel()

	h := newByDiscordHarness(t)
	snowflake := randomSnowflake()
	insertDiscordIntegration(t, h.tenant.UserPublicID, snowflake, true)

	status, _ := getByDiscord(t, h.baseURL, "", snowflake)
	require.Equal(t, http.StatusUnauthorized, status)
}

// repoRootFromTest walks up from this test file's directory until it
// finds the monorepo root (the directory holding the top-level go.work).
// Used to locate generated artifacts under packages/.
func repoRootFromTest(t *testing.T) string {
	t.Helper()
	_, thisFile, _, ok := runtime.Caller(0)
	require.True(t, ok, "runtime.Caller failed")
	dir := filepath.Dir(thisFile)
	for i := 0; i < 12; i++ {
		if _, err := os.Stat(filepath.Join(dir, "go.work")); err == nil {
			if _, err := os.Stat(filepath.Join(dir, "packages")); err == nil {
				return dir
			}
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}
	t.Fatalf("could not locate repo root from %s", filepath.Dir(thisFile))
	return ""
}

// TestByDiscordHiddenFromPublicSDK guards the hidden-operation rule: the internal service-
// token-only operation must be marked Hidden so it never appears in the
// generated public OpenAPI document or the TypeScript SDK, even though
// it remains routable for the service-token caller (asserted by the
// other tests in this file). A regression here would re-leak the
// internal resolution mechanism to SDK/CLI consumers.
func TestByDiscordHiddenFromPublicSDK(t *testing.T) {
	root := repoRootFromTest(t)

	artifacts := []string{
		filepath.Join(root, "packages", "sdk", "src", "openapi.ts"),
		filepath.Join(root, "packages", "sdk", "openapi.json"),
	}
	for _, path := range artifacts {
		data, err := os.ReadFile(path) //#nosec G304 -- path derived from repo root, test-only
		require.NoErrorf(t, err, "read generated artifact %s", path)
		content := string(data)
		require.NotContainsf(t, content, "by-discord",
			"%s still contains the internal by-discord path; mark the operation Hidden", path)
		require.NotContainsf(t, content, "internal-users-by-discord",
			"%s still contains the internal-users-by-discord operationId", path)
		require.NotContainsf(t, content, "/internal/users/",
			"%s still exposes an /internal/users/* path", path)
	}
}

// TestByDiscordRejectsNonNumericSnowflake asserts that the path-level
// pattern constraint rejects non-numeric input with a 422 envelope
// before the handler runs. The token is supplied so the failure
// definitively reflects validation, not auth.
func TestByDiscordRejectsNonNumericSnowflake(t *testing.T) {
	bootstrap(t)
	t.Parallel()

	h := newByDiscordHarness(t)
	status, raw := getByDiscord(t, h.baseURL, serviceTokenFixture, "not-a-snowflake")
	require.GreaterOrEqualf(t, status, 400, "expected 4xx, got %d body=%s", status, string(raw))
	require.Less(t, status, 500, "expected 4xx, got %d body=%s", status, string(raw))
}

// TestByDiscordPicksEarliestWorkspace asserts the v1.0 default-
// workspace rule: when the bound user belongs to multiple workspaces,
// the earliest-joined enabled membership wins. The SQL ORDER BY
// wm.created_at ASC, wm.id ASC drives this and the test pins the
// behaviour so a future schema change (e.g. an explicit
// users.default_workspace_id column) cannot silently regress it.
func TestByDiscordPicksEarliestWorkspace(t *testing.T) {
	bootstrap(t)
	t.Parallel()

	h := newByDiscordHarness(t)
	snowflake := randomSnowflake()
	insertDiscordIntegration(t, h.tenant.UserPublicID, snowflake, true)

	// Manufacture a second workspace + workspace_members row owned by the
	// same user. The tenant's first workspace was created at register
	// time so its created_at is strictly less than NOW(); the second row
	// inserted below sits later in the ordering and must NOT be returned.
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	userInternalID := lookupUserInternalID(ctx, t, testDB, h.tenant.UserPublicID)

	secondWsPubID, err := uuid.NewV7()
	require.NoError(t, err)
	secondWsBin, err := secondWsPubID.MarshalBinary()
	require.NoError(t, err)
	res, err := testDB.ExecContext(ctx, `
		INSERT INTO workspaces (public_id, slug, name)
		VALUES (?, ?, ?)
	`, secondWsBin, "second-"+randomHex(6), "Second workspace")
	require.NoError(t, err)
	secondWsID, err := res.LastInsertId()
	require.NoError(t, err)

	memberPubID, err := uuid.NewV7()
	require.NoError(t, err)
	memberBin, err := memberPubID.MarshalBinary()
	require.NoError(t, err)
	_, err = testDB.ExecContext(ctx, `
		INSERT INTO workspace_members (public_id, workspace_id, user_id, role)
		VALUES (?, ?, ?, 'owner')
	`, memberBin, secondWsID, userInternalID)
	require.NoError(t, err)

	// PurgeWorkspace is registered for the tenant's primary workspace;
	// the second one needs its own teardown so the test does not leak.
	t.Cleanup(func() {
		_, _ = testDB.Exec(`DELETE FROM workspace_members WHERE workspace_id = ?`, secondWsID)
		_, _ = testDB.Exec(`DELETE FROM workspaces WHERE id = ?`, secondWsID)
	})

	status, raw := getByDiscord(t, h.baseURL, serviceTokenFixture, snowflake)
	require.Equalf(t, http.StatusOK, status, "expected 200, got %d body=%s", status, string(raw))

	var out struct {
		WorkspaceID string `json:"workspaceId"`
	}
	require.NoError(t, json.Unmarshal(raw, &out))
	require.Equalf(t, h.tenant.WorkspacePublicID, out.WorkspaceID,
		"expected earliest-joined workspace (%s), got %s", h.tenant.WorkspacePublicID, out.WorkspaceID)
}
