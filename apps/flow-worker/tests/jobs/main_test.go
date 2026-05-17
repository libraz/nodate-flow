// Package jobs hosts the cross-binary integration tests for the
// flow-worker job set (Phase 5 / W2 of
// docs/plan/release-8-signals-and-judge-loop.md). Unlike the unit tests
// next to each job in apps/flow-worker/internal/jobs/<job>/, these tests:
//
//   - boot a real MySQL testcontainer with the full nodate-flow schema,
//   - boot the flow-api router in-process with the service-token middleware
//     configured (via the shared helpers in apps/flow-api/tests/helpers),
//   - construct the worker Job pointing at both, and
//   - drive a single Tick over real HTTP, asserting against the
//     post-tick database rows and the per-job Prometheus counters.
//
// The suite is gated on NF_TEST_INTEGRATION=1 to match
// apps/flow-worker/tests/lifecycle_test.go and the wider repo convention.
// Without the flag the package compiles and runs but every test calls
// t.Skip up-front, so go test -short stays fast on machines without
// Docker.
package jobs

import (
	"database/sql"
	"fmt"
	"os"
	"sync"
	"testing"

	"github.com/nodate-flow/nodate-flow/apps/flow-api/tests/helpers"
)

// sharedHarness is the (MySQL container, flow-api test server) pair that
// every test in this package reuses. The container is process-wide so
// the four tests collectively boot only one MySQL instance even when run
// with -count=N; per-test isolation is provided by the per-tenant
// CreateCalendarTestTenant helper plus its auto-registered
// PurgeWorkspace cleanup.
type sharedHarness struct {
	db      *sql.DB
	srv     *helpers.TestServer
	cleanup func()
}

var (
	harnessOnce sync.Once
	harness     *sharedHarness
	harnessErr  error
)

// serviceTokenFixture is the bearer the service-token middleware
// compares against. Arbitrary but deterministic — flow-worker normally
// reads NF_FLOW_API_SIGNAL_TOKEN, the test mints the same value on
// both sides so the in-process round-trip succeeds.
const serviceTokenFixture = "test-worker-service-token-0123456789abcdef0123456789abcdef0123"

// getHarness lazily boots the shared MySQL container and flow-api
// httptest.Server. Subsequent callers receive the same handles. The
// container and server are leaked to the process; testcontainers-ryuk
// reaps the container on exit.
func getHarness(t *testing.T) *sharedHarness {
	t.Helper()
	skipIfNoIntegration(t)
	harnessOnce.Do(func() {
		inst, err := helpers.EnsureShared()
		if err != nil {
			harnessErr = fmt.Errorf("ensure shared mysql: %w", err)
			return
		}
		srv, cleanup, err := helpers.NewTestServerWithServiceToken(inst.DB, serviceTokenFixture)
		if err != nil {
			harnessErr = fmt.Errorf("start service-token test server: %w", err)
			return
		}
		// Plug the shared DB into the helper's auto-cleanup hook so each
		// per-tenant Cleanup registered by CreateCalendarTestTenant is
		// able to PurgeWorkspace without the caller threading the *sql.DB
		// through every test.
		helpers.RegisterCleanupDB(inst.DB)
		harness = &sharedHarness{
			db:      inst.DB,
			srv:     srv,
			cleanup: cleanup,
		}
	})
	if harnessErr != nil {
		t.Fatalf("shared harness failed to start: %v", harnessErr)
	}
	if harness == nil {
		t.Fatalf("shared harness is nil (no error captured)")
	}
	return harness
}

// skipIfNoIntegration mirrors the gate every other integration suite in
// this repo uses. NF_TEST_INTEGRATION=1 is the opt-in flag; -short
// short-circuits earlier.
func skipIfNoIntegration(t *testing.T) {
	t.Helper()
	if testing.Short() {
		t.Skip("skipping integration test in -short mode")
	}
	if os.Getenv("NF_TEST_INTEGRATION") == "" {
		t.Skip("set NF_TEST_INTEGRATION=1 to run flow-worker job integration tests (Docker required)")
	}
}

// TestMain is intentionally minimal: harness boot is deferred to the
// first getHarness call so a package run without NF_TEST_INTEGRATION
// pays zero docker / MySQL cost.
func TestMain(m *testing.M) {
	code := m.Run()
	if harness != nil && harness.cleanup != nil {
		harness.cleanup()
	}
	os.Exit(code)
}
