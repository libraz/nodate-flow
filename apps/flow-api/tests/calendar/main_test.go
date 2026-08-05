// Package calendar contains end-to-end tests for calendar features
// (calendars, events, attendees, invites, public shares, task<->event
// sync) running against the merged flow-api router. The suite was
// migrated from apps/time-api/tests/e2e/ as part of R6 Phase 0 when
// time-api was folded into flow-api.
//
// Every test is gated on NF_TEST_INTEGRATION=1 and requires Docker for
// testcontainers. `go test -short` skips them entirely.
package calendar

import (
	"database/sql"
	"fmt"
	"os"
	"testing"

	"github.com/libraz/nodate-flow/apps/flow-api/tests/helpers"
	"github.com/libraz/nodate-flow/packages/go-shared/testhelpers"
)

var (
	testSrv *helpers.TestServer
	testDB  *sql.DB
)

// TestMain bootstraps the shared MySQL container and HTTP server once
// for the whole package so parallel tests share the same harness.
func TestMain(m *testing.M) {
	if os.Getenv("NF_TEST_INTEGRATION") == "" {
		os.Exit(m.Run())
	}
	inst, err := helpers.EnsureShared()
	if err != nil {
		fmt.Fprintln(os.Stderr, "calendar: start shared mysql:", err)
		os.Exit(1)
	}
	srv, cleanup, err := helpers.NewTestServer(inst.DB)
	if err != nil {
		fmt.Fprintln(os.Stderr, "calendar: start test server:", err)
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
	testhelpers.SkipUnlessIntegration(t)
}

func bootstrap(t *testing.T) {
	t.Helper()
	skipIfNoIntegration(t)
}

func newTenant(t *testing.T) *helpers.CalendarTestTenant {
	t.Helper()
	return helpers.CreateCalendarTestTenant(t, testSrv)
}
