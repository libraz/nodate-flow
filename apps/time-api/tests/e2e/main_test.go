package e2e

import (
	"database/sql"
	"fmt"
	"os"
	"testing"

	"github.com/nodate-flow/nodate-flow/apps/time-api/tests/helpers"
)

var (
	testSrv *helpers.TestServer
	testDB  *sql.DB
)

// TestMain bootstraps the shared MySQL container and HTTP server once
// for the whole package. When NT_TEST_INTEGRATION is unset, tests skip
// via skipIfNoIntegration.
func TestMain(m *testing.M) {
	if os.Getenv("ND_TEST_INTEGRATION") == "" {
		os.Exit(m.Run())
	}
	inst, err := helpers.EnsureShared()
	if err != nil {
		fmt.Fprintln(os.Stderr, "e2e: start shared mysql:", err)
		os.Exit(1)
	}
	srv, cleanup, err := helpers.NewTestServer(inst.DB)
	if err != nil {
		fmt.Fprintln(os.Stderr, "e2e: start test server:", err)
		os.Exit(1)
	}
	testSrv = srv
	testDB = inst.DB
	code := m.Run()
	cleanup()
	os.Exit(code)
}

func skipIfNoIntegration(t *testing.T) {
	t.Helper()
	if testing.Short() {
		t.Skip("skipping integration test in -short mode")
	}
	if os.Getenv("ND_TEST_INTEGRATION") == "" {
		t.Skip("set NT_TEST_INTEGRATION=1 to run e2e tests")
	}
}

func bootstrap(t *testing.T) {
	t.Helper()
	skipIfNoIntegration(t)
}

func newTenant(t *testing.T) *helpers.TestTenant {
	t.Helper()
	return helpers.CreateTestTenant(t, testSrv)
}
