package lifecycle

import (
	"io"
	"log/slog"
	"testing"

	"github.com/prometheus/client_golang/prometheus/testutil"

	"github.com/libraz/nodate-flow/apps/flow-worker/internal/jobs/calendar_event_day"
	"github.com/libraz/nodate-flow/apps/flow-worker/internal/obs"
)

// The gauges are process-wide, so these cases run in sequence inside one
// test and nothing else in this package touches them. Asserting a
// process-wide value from a test that runs alongside others would be
// asserting somebody else's state.
func TestReportRunnerHealth(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))

	t.Run("a worker with no jobs is not up", func(t *testing.T) {
		ReportRunnerHealth(logger, nil)
		if got := testutil.ToFloat64(obs.UpGauge); got != 0 {
			t.Errorf("nf_flow_worker_up = %v, want 0: a worker that registered nothing is not working", got)
		}
		if got := testutil.ToFloat64(obs.JobsRegisteredGauge); got != 0 {
			t.Errorf("nf_flow_worker_jobs_registered = %v, want 0", got)
		}
	})

	t.Run("a worker with every expected job is up", func(t *testing.T) {
		ReportRunnerHealth(logger, []string{"calendar_event_day", "business_metrics"})
		if got := testutil.ToFloat64(obs.UpGauge); got != 1 {
			t.Errorf("nf_flow_worker_up = %v, want 1", got)
		}
		if got := testutil.ToFloat64(obs.JobsRegisteredGauge); got != 2 {
			t.Errorf("nf_flow_worker_jobs_registered = %v, want 2", got)
		}
	})

	// The case the gauge exists for. Registering something is not the
	// same as registering what was expected: the unconditional gauge job
	// makes the count non-zero while the job that needs configuration is
	// missing, and a verdict based on the count alone calls that healthy.
	t.Run("a worker missing an expected job is not up", func(t *testing.T) {
		ReportRunnerHealth(logger, []string{"business_metrics"})
		if got := testutil.ToFloat64(obs.UpGauge); got != 0 {
			t.Errorf("nf_flow_worker_up = %v, want 0: calendar_event_day is expected and is not registered", got)
		}
		if got := testutil.ToFloat64(obs.JobsRegisteredGauge); got != 1 {
			t.Errorf("nf_flow_worker_jobs_registered = %v, want 1", got)
		}
	})

	// Leave the process-wide gauges where a fresh process has them.
	obs.UpGauge.Set(0)
	obs.JobsRegisteredGauge.Set(0)
}

// The expectation is a written-down list, so what keeps it from drifting
// is that it names a job this binary actually has.
func TestExpectedJobsNamesTheCalendarJob(t *testing.T) {
	t.Parallel()

	if len(expectedJobs) != 1 || expectedJobs[0] != calendar_event_day.JobName {
		t.Errorf("expectedJobs = %v, want [%s]", expectedJobs, calendar_event_day.JobName)
	}
}
