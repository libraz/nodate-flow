// Package e2e contains REST API end-to-end tests that drive the full
// nodate-flow HTTP router against a real MySQL testcontainer. Every
// test in this package is gated on NF_TEST_INTEGRATION=1 and requires
// Docker; `go test -short` skips them entirely.
package e2e

import (
	"bytes"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/libraz/nodate-flow/apps/flow-api/tests/helpers"
	"github.com/libraz/nodate-flow/packages/go-shared/testhelpers"
)

var (
	testServerURL string
	testDB        *sql.DB
	testStorage   *helpers.StorageBundle
)

// TestMain bootstraps the shared MySQL testcontainer, MinIO, and the
// HTTP server once for the whole package so parallel tests all talk to
// the same harness. When NF_TEST_INTEGRATION is unset, it simply runs
// m.Run() and every test skips via skipIfNoIntegration.
//
// Once integration mode is on, a missing prerequisite is a failure and
// not a skip: the suite was asked to run, so reporting success after
// quietly running nothing would hide exactly the regressions (cross
// tenant access, IDOR, secret leakage) it exists to catch.
func TestMain(m *testing.M) {
	if os.Getenv("NF_TEST_INTEGRATION") == "" {
		os.Exit(m.Run())
	}
	inst, err := helpers.EnsureShared()
	if err != nil {
		fmt.Fprintln(os.Stderr, "e2e: start shared mysql:", err)
		os.Exit(1)
	}

	// MinIO backs the attachment / avatar dedup tests against the real
	// S3 path; without it those tests have nothing to assert on.
	minioInst, err := helpers.EnsureSharedMinIO()
	if err != nil {
		fmt.Fprintln(os.Stderr, "e2e: start shared minio:", err)
		os.Exit(1)
	}
	testStorage, err = helpers.NewStorageBundle(minioInst)
	if err != nil {
		fmt.Fprintln(os.Stderr, "e2e: build storage bundle:", err)
		os.Exit(1)
	}

	srv, cleanup, err := helpers.NewTestServerWithStorage(inst.DB, testStorage)
	if err != nil {
		fmt.Fprintln(os.Stderr, "e2e: start test server:", err)
		os.Exit(1)
	}
	testServerURL = srv.BaseURL
	testDB = inst.DB
	helpers.RegisterCleanupDB(inst.DB)
	code := m.Run()
	cleanup()
	os.Exit(code)
}

// requireStorage asserts the shared MinIO bundle is wired. Tests that
// exercise attachment / avatar uploads call this at the top; TestMain
// has already aborted the run if the bundle could not be built, so this
// only fires when a test reaches storage outside the bootstrapped path.
func requireStorage(t *testing.T) {
	t.Helper()
	require.NotNil(t, testStorage, "storage bundle not initialised")
}

func mustStartHarness(t *testing.T) {
	t.Helper()
	require.NotEmpty(t, testServerURL, "shared test server failed to start")
}

// skipIfNoIntegration skips the current test unless NF_TEST_INTEGRATION
// is set. Every test in this package calls it at the top so unit-only
// runs stay green without Docker.
func skipIfNoIntegration(t *testing.T) {
	t.Helper()
	testhelpers.SkipUnlessIntegration(t)
}

// requireAIMock skips a test whose assertions only hold when the mock
// orchestrator answers for the LLM. The suite runs twice, once with
// NF_FLOW_AI_MOCK set and once without, and in the second pass a
// workspace with no provider configured correctly refuses the call —
// so a test that needs a deterministic completion has nothing to assert
// on. This is the mirror of the guard the provider-failure tests use to
// exclude themselves from the mock pass.
func requireAIMock(t *testing.T) {
	t.Helper()
	switch os.Getenv("NF_FLOW_AI_MOCK") {
	case "", "0", "false":
		t.Skip("NF_FLOW_AI_MOCK is unset; without the mock orchestrator no provider answers this call")
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

// randomHex returns a hex-encoded random string of n bytes (2n chars).
func randomHex(n int) string {
	b := make([]byte, n)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}
