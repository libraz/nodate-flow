package lifecycle

import (
	"io"
	"log/slog"
	"testing"

	"github.com/prometheus/client_golang/prometheus/testutil"

	"github.com/libraz/nodate-flow/apps/flow-worker/internal/obs"
)

// The gauges are process-wide, so these cases run in sequence inside one
// test and nothing else in this package touches them. Asserting a
// process-wide value from a test that runs alongside others would be
// asserting somebody else's state.
func TestReportRunnerHealth(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))

	t.Run("a worker with no jobs is not up", func(t *testing.T) {
		reportRunnerHealth(logger, 0)
		if got := testutil.ToFloat64(obs.UpGauge); got != 0 {
			t.Errorf("nf_flow_worker_up = %v, want 0: a worker that registered nothing is not working", got)
		}
		if got := testutil.ToFloat64(obs.JobsRegisteredGauge); got != 0 {
			t.Errorf("nf_flow_worker_jobs_registered = %v, want 0", got)
		}
	})

	t.Run("a worker with jobs is up", func(t *testing.T) {
		reportRunnerHealth(logger, 2)
		if got := testutil.ToFloat64(obs.UpGauge); got != 1 {
			t.Errorf("nf_flow_worker_up = %v, want 1", got)
		}
		if got := testutil.ToFloat64(obs.JobsRegisteredGauge); got != 2 {
			t.Errorf("nf_flow_worker_jobs_registered = %v, want 2", got)
		}
	})

	// Leave the process-wide gauges where a fresh process has them.
	obs.UpGauge.Set(0)
	obs.JobsRegisteredGauge.Set(0)
}
