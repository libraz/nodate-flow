package main

import (
	"database/sql"
	"io"
	"log/slog"
	"testing"

	"github.com/prometheus/client_golang/prometheus/testutil"

	"github.com/libraz/nodate-flow/apps/flow-worker/internal/config"
	"github.com/libraz/nodate-flow/apps/flow-worker/internal/jobs"
	"github.com/libraz/nodate-flow/apps/flow-worker/internal/lifecycle"
	"github.com/libraz/nodate-flow/apps/flow-worker/internal/obs"
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

// contains reports whether names holds want.
func contains(names []string, want string) bool {
	for _, n := range names {
		if n == want {
			return true
		}
	}
	return false
}

// An unset service token disables the job that talks to flow-api. That is
// a deliberate choice — posting unauthenticated signals would just log
// 401s — but it has to be visible: the deploy that hits it produces no
// calendar.event_day_arrived events at all, and everything downstream
// goes quiet with nothing failing anywhere.
//
// This half asserts the input to that visibility: the calendar job is not
// registered. What the boot then reports is lifecycle.ReportRunnerHealth's
// half.
func TestRegisterProductionJobsSkipsTheCalendarJobWithoutAServiceToken(t *testing.T) {
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
		if names := runner.Names(); contains(names, "calendar_event_day") {
			t.Fatalf("calendar_event_day registered with token %q; registered = %v", token, names)
		}
	}
}

// The mirror case: with a token, the job is registered. Without this
// the test above would also pass on a binary that registers nothing
// ever.
func TestRegisterProductionJobsRegistersTheCalendarJobWithAServiceToken(t *testing.T) {
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
	if names := runner.Names(); !contains(names, "calendar_event_day") {
		t.Fatalf("calendar_event_day not registered; registered = %v", names)
	}
}

// The chain the up gauge exists to report, driven end to end: the token
// decides what registerProductionJobs registers, and what is registered
// decides the gauge. Both halves live in this binary but in different
// packages, and the seam between them is where "up" quietly stopped
// meaning anything — the always-on gauge job made the runner non-empty,
// so a count-based verdict called a tokenless deploy healthy.
//
// The gauges are process-wide, so the cases run in sequence and the
// gauge is left where a fresh process has it.
func TestUpGaugeReportsWhetherTheConfiguredJobsAreRegistered(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	db := openIdlePool(t)

	t.Run("a tokenless worker is not up", func(t *testing.T) {
		runner := &jobs.Runner{}
		cfg := &config.Config{FlowAPIBaseURL: "http://127.0.0.1:8080"}

		registerProductionJobs(cfg, logger)(runner, db)
		lifecycle.ReportRunnerHealth(logger, runner.Names())

		if got := testutil.ToFloat64(obs.UpGauge); got != 0 {
			t.Errorf("nf_flow_worker_up = %v, want 0: the calendar job the token would enable is not registered", got)
		}
	})

	t.Run("a worker with a token is up", func(t *testing.T) {
		runner := &jobs.Runner{}
		cfg := &config.Config{
			ServiceToken:   "svc-token",
			FlowAPIBaseURL: "http://127.0.0.1:8080",
		}

		registerProductionJobs(cfg, logger)(runner, db)
		lifecycle.ReportRunnerHealth(logger, runner.Names())

		if got := testutil.ToFloat64(obs.UpGauge); got != 1 {
			t.Errorf("nf_flow_worker_up = %v, want 1: every configured job is registered", got)
		}
	})

	obs.UpGauge.Set(0)
	obs.JobsRegisteredGauge.Set(0)
}

// The gauge-refresh job reads the database directly and needs no flow-api
// credential, so it must survive the token check that disables the
// calendar job — a deploy waiting on the token still reports how many
// tasks the instance holds.
func TestRegisterProductionJobsRegistersBusinessMetricsWithoutAServiceToken(t *testing.T) {
	t.Parallel()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))

	db := openIdlePool(t)
	runner := &jobs.Runner{}
	cfg := &config.Config{FlowAPIBaseURL: "http://127.0.0.1:8080"}

	registerProductionJobs(cfg, logger)(runner, db)
	if names := runner.Names(); !contains(names, "business_metrics") {
		t.Fatalf("business_metrics not registered; registered = %v", names)
	}
}
