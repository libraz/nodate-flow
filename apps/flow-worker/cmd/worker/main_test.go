package main

import (
	"database/sql"
	"io"
	"log/slog"
	"testing"

	"github.com/libraz/nodate-flow/apps/flow-worker/internal/config"
	"github.com/libraz/nodate-flow/apps/flow-worker/internal/jobs"
)

// openIdlePool returns a *sql.DB that is never dialled. sql.Open does
// no I/O, and job construction only stores the handle.
func openIdlePool(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("mysql", "nodate:nodate@tcp(127.0.0.1:1)/nodate_flow")
	if err != nil {
		t.Fatalf("open pool: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

// An unset service token disables the only job this binary has. That is
// a deliberate choice — posting unauthenticated signals would just log
// 401s — but it has to be visible: the deploy that hits it produces no
// calendar.event_day_arrived events at all, and everything downstream
// goes quiet with nothing failing anywhere.
//
// This half asserts the input to that visibility: nothing is
// registered. What the boot then reports is
// lifecycle.reportRunnerHealth's half.
func TestRegisterProductionJobsRegistersNothingWithoutAServiceToken(t *testing.T) {
	t.Parallel()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))

	// A usable pool is passed in so the token is the only thing that
	// can stop registration.
	db := openIdlePool(t)
	for _, token := range []string{"", "   "} {
		runner := &jobs.Runner{}
		cfg := &config.Config{
			ServiceToken:   token,
			FlowAPIBaseURL: "http://127.0.0.1:8080",
		}
		registerProductionJobs(cfg, logger)(runner, db)
		if got := runner.Registered(); got != 0 {
			t.Fatalf("jobs registered with token %q = %d, want 0", token, got)
		}
	}
}

// The mirror case: with a token, the job is registered. Without this
// the test above would also pass on a binary that registers nothing
// ever.
func TestRegisterProductionJobsRegistersTheJobWithAServiceToken(t *testing.T) {
	t.Parallel()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))

	// A pool handle is enough; nothing dials until the first tick.
	db := openIdlePool(t)
	runner := &jobs.Runner{}
	cfg := &config.Config{
		ServiceToken:   "svc-token",
		FlowAPIBaseURL: "http://127.0.0.1:8080",
	}
	registerProductionJobs(cfg, logger)(runner, db)
	if got := runner.Registered(); got != 1 {
		t.Fatalf("jobs registered = %d, want 1", got)
	}
}
