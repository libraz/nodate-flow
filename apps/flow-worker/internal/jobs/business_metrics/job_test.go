package business_metrics

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus/testutil"
	"github.com/stretchr/testify/require"

	"github.com/libraz/nodate-flow/apps/flow-worker/internal/db/generated"
	"github.com/libraz/nodate-flow/apps/flow-worker/internal/obs"
)

// stubCounts stands in for the generated queries. It records the window
// bound it was asked for so the test can assert what "recent" meant.
type stubCounts struct {
	states    []generated.CountTasksByDerivedStateRow
	statesErr error
	active    int64
	activeErr error

	since time.Time
	calls int
}

func (s *stubCounts) CountTasksByDerivedState(context.Context) ([]generated.CountTasksByDerivedStateRow, error) {
	s.calls++
	if s.statesErr != nil {
		return nil, s.statesErr
	}
	return s.states, nil
}

func (s *stubCounts) CountWorkspacesWithRecentActivity(_ context.Context, since time.Time) (int64, error) {
	s.since = since
	if s.activeErr != nil {
		return 0, s.activeErr
	}
	return s.active, nil
}

// row builds one aggregate row. The state arrives as the literal the
// column holds, not as a Go constant, so the test still fails if the
// label the gauge carries stops being the value in the database.
func row(state string, total int64) generated.CountTasksByDerivedStateRow {
	return generated.CountTasksByDerivedStateRow{
		DerivedState: generated.TasksDerivedState(state),
		Total:        total,
	}
}

// newJob returns a job wired to the stub, with the gauges cleared. The
// gauges are process-global, so every test starts from a known floor
// rather than from whatever the previous one published.
func newJob(t *testing.T, counts *stubCounts) *Job {
	t.Helper()
	obs.TasksByStateGauge.Reset()
	obs.TasksTotalGauge.Set(0)
	obs.ActiveWorkspacesGauge.Set(0)
	return &Job{
		Counts:          counts,
		Logger:          slog.New(slog.NewTextHandler(io.Discard, nil)),
		RefreshInterval: time.Minute,
		ActiveWindow:    5 * time.Minute,
	}
}

func TestTickPublishesTheCountsTheQueryReturned(t *testing.T) {
	counts := &stubCounts{
		states: []generated.CountTasksByDerivedStateRow{
			row("open", 12),
			row("waiting", 3),
			row("review", 7),
			row("done", 40),
			row("cancelled", 1),
		},
		active: 4,
	}
	job := newJob(t, counts)
	now := time.Date(2026, time.July, 4, 12, 0, 0, 0, time.UTC)

	require.NoError(t, job.Tick(context.Background(), now))

	require.InDelta(t, 12, testutil.ToFloat64(obs.TasksByStateGauge.WithLabelValues("open")), 0)
	require.InDelta(t, 3, testutil.ToFloat64(obs.TasksByStateGauge.WithLabelValues("waiting")), 0)
	require.InDelta(t, 7, testutil.ToFloat64(obs.TasksByStateGauge.WithLabelValues("review")), 0)
	require.InDelta(t, 40, testutil.ToFloat64(obs.TasksByStateGauge.WithLabelValues("done")), 0)
	require.InDelta(t, 1, testutil.ToFloat64(obs.TasksByStateGauge.WithLabelValues("cancelled")), 0)

	require.InDelta(t, 63, testutil.ToFloat64(obs.TasksTotalGauge), 0,
		"the total is summed from the same rows as the breakdown")
	require.InDelta(t, 4, testutil.ToFloat64(obs.ActiveWorkspacesGauge), 0)

	require.Equal(t, now.Add(-5*time.Minute).UTC(), counts.since,
		"active workspaces are counted over the trailing window ending at the tick")
}

// A state that empties out has to fall to zero. The query returns no row
// for it at all, so nothing overwrites the count it had, and the gauge
// would keep reporting tasks that are no longer in that state.
func TestTickZeroesAStateThatNoLongerHasRows(t *testing.T) {
	counts := &stubCounts{
		states: []generated.CountTasksByDerivedStateRow{
			row("open", 5),
			row("review", 7),
		},
		active: 2,
	}
	job := newJob(t, counts)
	now := time.Date(2026, time.July, 4, 12, 0, 0, 0, time.UTC)
	require.NoError(t, job.Tick(context.Background(), now))
	require.InDelta(t, 7, testutil.ToFloat64(obs.TasksByStateGauge.WithLabelValues("review")), 0)

	counts.states = []generated.CountTasksByDerivedStateRow{row("open", 6)}
	require.NoError(t, job.Tick(context.Background(), now.Add(time.Minute)))

	require.InDelta(t, 0, testutil.ToFloat64(obs.TasksByStateGauge.WithLabelValues("review")), 0,
		"a state with no rows must read 0, not the count from the previous refresh")
	require.InDelta(t, 6, testutil.ToFloat64(obs.TasksByStateGauge.WithLabelValues("open")), 0)
	require.InDelta(t, 6, testutil.ToFloat64(obs.TasksTotalGauge), 0)
}

// A failed read must leave the last good numbers alone. Zero is a legal
// reading of these gauges, so resetting on failure would publish "this
// instance has no tasks" every time the database hiccups.
func TestTickLeavesTheLastGoodValuesWhenTheRefreshFails(t *testing.T) {
	counts := &stubCounts{
		states: []generated.CountTasksByDerivedStateRow{row("open", 9), row("done", 11)},
		active: 3,
	}
	job := newJob(t, counts)
	now := time.Date(2026, time.July, 4, 12, 0, 0, 0, time.UTC)
	require.NoError(t, job.Tick(context.Background(), now))

	counts.statesErr = errors.New("connection refused")
	err := job.Tick(context.Background(), now.Add(time.Minute))
	require.Error(t, err)

	require.InDelta(t, 9, testutil.ToFloat64(obs.TasksByStateGauge.WithLabelValues("open")), 0)
	require.InDelta(t, 11, testutil.ToFloat64(obs.TasksByStateGauge.WithLabelValues("done")), 0)
	require.InDelta(t, 20, testutil.ToFloat64(obs.TasksTotalGauge), 0)
	require.InDelta(t, 3, testutil.ToFloat64(obs.ActiveWorkspacesGauge), 0)
}

// The same rule for the second query: the task gauges have already been
// read successfully, but nothing is published until both reads succeed,
// so a scrape never sees one half of a refresh.
func TestTickLeavesTheLastGoodValuesWhenTheWorkspaceCountFails(t *testing.T) {
	counts := &stubCounts{
		states: []generated.CountTasksByDerivedStateRow{row("open", 9)},
		active: 3,
	}
	job := newJob(t, counts)
	now := time.Date(2026, time.July, 4, 12, 0, 0, 0, time.UTC)
	require.NoError(t, job.Tick(context.Background(), now))

	counts.states = []generated.CountTasksByDerivedStateRow{row("open", 100)}
	counts.activeErr = errors.New("connection refused")
	require.Error(t, job.Tick(context.Background(), now.Add(time.Minute)))

	require.InDelta(t, 9, testutil.ToFloat64(obs.TasksByStateGauge.WithLabelValues("open")), 0,
		"a half-completed refresh must not reach the gauges")
	require.InDelta(t, 3, testutil.ToFloat64(obs.ActiveWorkspacesGauge), 0)
}

// The runner ticks every job on one shared cadence; this job's own
// interval is what keeps the aggregate off the database more often than
// asked for.
func TestTickSkipsUntilTheRefreshIntervalHasElapsed(t *testing.T) {
	counts := &stubCounts{states: []generated.CountTasksByDerivedStateRow{row("open", 1)}}
	job := newJob(t, counts)
	job.RefreshInterval = 5 * time.Minute
	now := time.Date(2026, time.July, 4, 12, 0, 0, 0, time.UTC)

	require.NoError(t, job.Tick(context.Background(), now))
	require.NoError(t, job.Tick(context.Background(), now.Add(time.Minute)))
	require.Equal(t, 1, counts.calls, "a tick inside the interval must not re-read")

	require.NoError(t, job.Tick(context.Background(), now.Add(5*time.Minute)))
	require.Equal(t, 2, counts.calls, "a tick at the interval boundary refreshes")
}
