// Package signaljudgetests hosts the cross-stack signaljudge tests
// that need either deterministic in-memory fakes (applier_test.go) or a
// real MySQL via testcontainers (prompt_render_test.go).
//
// The package is mixed-mode by design:
//
//   - The applier tests in applier_test.go use only in-process fakes and
//     run unconditionally (fast, no Docker).
//   - The prompt-render integration tests in prompt_render_test.go
//     require NF_TEST_INTEGRATION=1 and Docker for the testcontainer
//     MySQL; they skip in -short mode and when the env gate is unset.
//
// TestMain bootstraps the shared MySQL container + HTTP server only
// when integration mode is on; otherwise it returns immediately so the
// fake-only tests keep running on machines without Docker.
package signaljudgetests

import (
	"database/sql"
	"fmt"
	"os"
	"testing"

	"github.com/libraz/nodate-flow/apps/flow-api/tests/helpers"
)

var (
	testSrv *helpers.TestServer
	testDB  *sql.DB
)

// TestMain bootstraps the shared MySQL testcontainer plus the
// auth+flow composite test server once for the whole package so
// parallel integration tests share the same harness. When
// NF_TEST_INTEGRATION is unset the bootstrap is skipped entirely and
// the integration tests self-skip via bootstrap(t).
func TestMain(m *testing.M) {
	if os.Getenv("NF_TEST_INTEGRATION") == "" {
		os.Exit(m.Run())
	}
	inst, err := helpers.EnsureShared()
	if err != nil {
		fmt.Fprintln(os.Stderr, "signaljudge: start shared mysql:", err)
		os.Exit(1)
	}
	srv, cleanup, err := helpers.NewTestServer(inst.DB)
	if err != nil {
		fmt.Fprintln(os.Stderr, "signaljudge: start test server:", err)
		os.Exit(1)
	}
	testSrv = srv
	testDB = inst.DB
	helpers.RegisterCleanupDB(inst.DB)
	code := m.Run()
	cleanup()
	os.Exit(code)
}

// bootstrap is the integration-only setup gate. Tests that talk to the
// real DB must call it at the top of the function body so they skip
// cleanly when Docker / NF_TEST_INTEGRATION are unavailable.
func bootstrap(t *testing.T) {
	t.Helper()
	if testing.Short() {
		t.Skip("skipping integration test in -short mode")
	}
	if os.Getenv("NF_TEST_INTEGRATION") == "" {
		t.Skip("set NF_TEST_INTEGRATION=1 to run signaljudge integration tests")
	}
	if testSrv == nil || testDB == nil {
		t.Skip("shared test server failed to start")
	}
}
