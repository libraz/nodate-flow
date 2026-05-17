package e2e

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/nodate-flow/nodate-flow/apps/flow-api/internal/db/types"
	"github.com/nodate-flow/nodate-flow/apps/flow-api/tests/helpers"
)

// serviceTokenFixture is the 32-byte hex secret presented as the
// flow-worker service token in the tests below. The value is arbitrary
// but deterministic so the test does not depend on entropy at runtime.
const serviceTokenFixture = "test-service-token-0123456789abcdef0123456789abcdef0123456789ab"

// signalsServiceTokenHarness wraps a dedicated test server that has the
// flow-worker service token configured plus a freshly-registered
// tenant that owns a workspace. The shared package-level test server
// has no service token, so each service-token test spins up its own
// to exercise the middleware end to end.
type signalsServiceTokenHarness struct {
	baseURL string
	tenant  *helpers.TestTenant
}

// newSignalsServiceTokenHarness boots a dedicated server with the
// service token configured and registers a tenant on it. The cleanup
// closure must be invoked via t.Cleanup.
func newSignalsServiceTokenHarness(t *testing.T) *signalsServiceTokenHarness {
	t.Helper()
	srv, cleanup, err := helpers.NewTestServerWithServiceToken(testDB, serviceTokenFixture)
	require.NoError(t, err)
	t.Cleanup(cleanup)
	tenant := helpers.CreateTestTenant(t, srv.BaseURL)
	return &signalsServiceTokenHarness{baseURL: srv.BaseURL, tenant: tenant}
}

// postSignal sends POST /signals with the supplied bearer and body and
// returns the status code plus raw response body. The caller is
// responsible for choosing the bearer (service token, user JWT, or a
// deliberate mismatch).
func postSignal(t *testing.T, baseURL, bearer string, body any) (int, []byte) {
	t.Helper()
	buf, err := json.Marshal(body)
	require.NoError(t, err)
	req, err := http.NewRequest(http.MethodPost, baseURL+"/signals", bytes.NewReader(buf))
	require.NoError(t, err)
	req.Header.Set("Content-Type", "application/json")
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

// TestSignalsAcceptsServiceToken asserts that a request bearing the
// configured NF_FLOW_API_SIGNAL_TOKEN is admitted on POST /signals and
// that the signal row lands in the workspace named by the request
// body. The handler runs without an actor user so the request body
// alone must drive the workspace scoping.
func TestSignalsAcceptsServiceToken(t *testing.T) {
	bootstrap(t)
	t.Parallel()

	h := newSignalsServiceTokenHarness(t)

	status, raw := postSignal(t, h.baseURL, serviceTokenFixture, map[string]any{
		"workspaceId": h.tenant.WorkspacePublicID,
		"source":      "manual",
		"kind":        "manual",
		"payload":     map[string]any{"origin": "flow-worker"},
	})
	require.Equalf(t, http.StatusOK, status, "expected 200, got %d body=%s", status, string(raw))

	var signal struct {
		ID          string `json:"id"`
		Source      string `json:"source"`
		Kind        string `json:"kind"`
		SubjectType string `json:"subjectType"`
	}
	require.NoError(t, json.Unmarshal(raw, &signal))
	require.NotEmpty(t, signal.ID, "service-token call produced no signal id")
	require.Equal(t, "manual", signal.Source)
	require.Equal(t, "manual", signal.Kind)

	// Verify the row is actually in the database and bound to the
	// expected workspace. The internal numeric id never leaks out via
	// the response so the test goes through the public id.
	wsPub, err := types.Parse(h.tenant.WorkspacePublicID)
	require.NoError(t, err)
	sigPub, err := types.Parse(signal.ID)
	require.NoError(t, err)
	var count int
	row := testDB.QueryRow(
		`SELECT COUNT(*) FROM signals s
		   INNER JOIN workspaces w ON w.id = s.workspace_id
		  WHERE w.public_id = ? AND s.public_id = ?`,
		wsPub,
		sigPub,
	)
	require.NoError(t, row.Scan(&count))
	require.Equal(t, 1, count, "signal row was not persisted in the tenant workspace")
}

// TestSignalsRejectsWrongServiceToken asserts that a bearer that
// neither equals the configured service token nor decodes as a valid
// JWT / PAT / MCP token is rejected with 401. The error envelope must
// look identical to the standard auth-failure envelope so the service-
// token path is not detectable by an attacker probing the endpoint.
func TestSignalsRejectsWrongServiceToken(t *testing.T) {
	bootstrap(t)
	t.Parallel()

	h := newSignalsServiceTokenHarness(t)

	status, raw := postSignal(t, h.baseURL, "definitely-not-the-right-token", map[string]any{
		"workspaceId": h.tenant.WorkspacePublicID,
		"source":      "manual",
		"kind":        "manual",
	})
	require.Equalf(t, http.StatusUnauthorized, status, "expected 401, got %d body=%s", status, string(raw))

	// The envelope must not reveal which auth mode was attempted.
	require.NotContains(t, string(raw), "service_token", "401 body leaks the service-token auth mode")
}

// TestSignalsRejectsServiceTokenOnOtherEndpoint asserts that the
// service token is scoped to POST /signals only. Presenting it to a
// non-signals endpoint (POST /tasks) must reject with 401, proving
// the middleware was not attached as a global authn fallback.
func TestSignalsRejectsServiceTokenOnOtherEndpoint(t *testing.T) {
	bootstrap(t)
	t.Parallel()

	h := newSignalsServiceTokenHarness(t)

	// POST /tasks with the service token must fail. The body is
	// well-formed so the rejection is purely about auth, not
	// validation.
	body, err := json.Marshal(map[string]any{
		"projectId": h.tenant.ProjectPublicID,
		"title":     "should-not-be-created",
	})
	require.NoError(t, err)
	req, err := http.NewRequest(http.MethodPost, h.baseURL+"/tasks", bytes.NewReader(body))
	require.NoError(t, err)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Authorization", "Bearer "+serviceTokenFixture)

	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer func() { _ = resp.Body.Close() }()
	raw, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	require.Equalf(t, http.StatusUnauthorized, resp.StatusCode,
		"service token must be rejected outside /signals; got %d body=%s", resp.StatusCode, string(raw))
}

// TestSignalsAcceptsJWTWhenServiceTokenConfigured asserts that
// enabling the service token does not regress the existing user-bearer
// path. A standard tenant JWT must still be admitted on POST /signals
// even when the service-token middleware is in front of the route.
func TestSignalsAcceptsJWTWhenServiceTokenConfigured(t *testing.T) {
	bootstrap(t)
	t.Parallel()

	h := newSignalsServiceTokenHarness(t)

	status, raw := postSignal(t, h.baseURL, h.tenant.AccessToken, map[string]any{
		"workspaceId": h.tenant.WorkspacePublicID,
		"source":      "manual",
		"kind":        "manual",
	})
	require.Equalf(t, http.StatusOK, status,
		"user-bearer path regressed when service token is configured: got %d body=%s",
		status, string(raw))

	var signal struct {
		ID string `json:"id"`
	}
	require.NoError(t, json.Unmarshal(raw, &signal))
	require.NotEmpty(t, signal.ID)
}
