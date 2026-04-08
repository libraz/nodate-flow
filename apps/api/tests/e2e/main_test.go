// Package e2e contains REST API end-to-end tests that drive the full
// nodate-flow HTTP router against a real MySQL testcontainer. Every
// test in this package is gated on NF_TEST_INTEGRATION=1 and requires
// Docker; `go test -short` skips them entirely.
package e2e

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/nodate-flow/nodate-flow/apps/api/tests/helpers"
)

// sharedOnce guards lazy initialization of the shared MySQL container
// and TestServer. Tests call mustStartHarness(t) at the top of their
// body to ensure the harness is up before they run.
var (
	sharedOnce sync.Once

	testServerURL string
	testDB        *sql.DB
)

// mustStartHarness lazily boots the shared MySQL testcontainer and
// TestServer the first time it is called, and stores the resulting
// base URL / DB handle in package vars so later tests can reuse them.
//
// It is called from each test body (after skipIfNoIntegration) rather
// than from TestMain so that unit-only `go test -short` runs skip
// cleanly without ever touching Docker.
func mustStartHarness(t *testing.T) {
	t.Helper()
	sharedOnce.Do(func() {
		inst := helpers.StartShared(t)
		srv := helpers.StartTestServer(t, inst.DB)
		testServerURL = srv.BaseURL
		testDB = inst.DB
	})
	require.NotEmpty(t, testServerURL, "shared test server failed to start")
}

// skipIfNoIntegration skips the current test unless NF_TEST_INTEGRATION
// is set. Every test in this package calls it at the top so unit-only
// runs stay green without Docker.
func skipIfNoIntegration(t *testing.T) {
	t.Helper()
	if testing.Short() {
		t.Skip("skipping integration test in -short mode")
	}
	if os.Getenv("NF_TEST_INTEGRATION") == "" {
		t.Skip("set NF_TEST_INTEGRATION=1 to run e2e tests")
	}
}

// ---- Shared HTTP helpers ----------------------------------------------------

// doJSONStatus sends a JSON request and returns the status code plus
// raw response body. Unlike doJSON this does not assert 2xx and is
// therefore the tool of choice for negative-path tests (401, 409, ...).
func doJSONStatus(t *testing.T, method, url, bearer string, body any) (int, []byte) {
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
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer func() { _ = resp.Body.Close() }()
	raw, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	return resp.StatusCode, raw
}

// doJSON sends a JSON request, asserts a 2xx status, and decodes the
// response body into out (when non-nil).
func doJSON(t *testing.T, method, url, bearer string, body any, out any) {
	t.Helper()
	status, raw := doJSONStatus(t, method, url, bearer, body)
	require.GreaterOrEqualf(t, status, 200, "%s %s -> %d body=%s", method, url, status, string(raw))
	require.Lessf(t, status, 300, "%s %s -> %d body=%s", method, url, status, string(raw))
	if out != nil && len(raw) > 0 {
		require.NoError(t, json.Unmarshal(raw, out), "decode %s %s body=%s", method, url, string(raw))
	}
}

// newTenant creates a fresh tenant bound to the shared test server.
func newTenant(t *testing.T) *helpers.TestTenant {
	t.Helper()
	return helpers.CreateTestTenant(t, testServerURL)
}

// bootstrap runs the two setup steps every e2e test needs: it skips
// the test when NF_TEST_INTEGRATION is unset and lazily starts the
// shared harness.
func bootstrap(t *testing.T) {
	t.Helper()
	skipIfNoIntegration(t)
	mustStartHarness(t)
}
