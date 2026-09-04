// Package business_metrics implements the worker job that refreshes the
// instance-wide task and workspace gauges the business dashboard reads:
// nf_tasks_total, nf_tasks_by_state and nf_active_workspaces.
//
// These are point-in-time counts, not events, so they are produced by a
// periodic read rather than incremented at a write path. A counter
// incremented by the API would restart at zero with the process and would
// miss every write that did not go through it (an import, a reconciler, a
// direct fix); a gauge re-read from the database says what is true now
// regardless of who wrote it.
//
// The read goes through the sqlc queries generated into
// internal/db/generated from sql/queries/worker-metrics.
package business_metrics

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/libraz/nodate-flow/apps/flow-worker/internal/db/generated"
	"github.com/libraz/nodate-flow/apps/flow-worker/internal/obs"
)

const (
	// JobName is the stable identifier the runner uses for log fields.
	// MUST NOT change once a deploy has observed it.
	JobName = "business_metrics"

	// defaultRefreshInterval is how often the counts are re-read when
	// Job.RefreshInterval is left zero. The gauges are scraped every 15s;
	// refreshing them faster than that would run the aggregate several
	// times per scrape for a number nobody can see, and refreshing much
	// slower would let a dashboard read a value from several minutes ago
	// without saying so.
	defaultRefreshInterval = 60 * time.Second

	// defaultActiveWindow is the trailing window
	// nf_active_workspaces counts activity in. Matches the window the
	// dashboard panel describes; changing one without the other makes the
	// panel title disagree with the series it draws.
	defaultActiveWindow = 5 * time.Minute
)

// Job re-reads the instance-wide counts and publishes them on the three
// business gauges. It holds no state beyond the last refresh instant and
// the set of states it last published.
type Job struct {
	// Counts is the read surface: the generated queries, or a stub under
	// test. Required.
	Counts generated.Querier
	// Logger receives one record per failed refresh. Required.
	Logger *slog.Logger
	// RefreshInterval is the minimum time between two refreshes. The
	// runner ticks every job on one shared cadence, so a job wanting a
	// different one throttles itself: ticks arriving sooner than this
	// return without reading. Defaults to defaultRefreshInterval when
	// non-positive.
	RefreshInterval time.Duration
	// ActiveWindow is the trailing window nf_active_workspaces counts
	// event activity in. Defaults to defaultActiveWindow when
	// non-positive.
	ActiveWindow time.Duration

	// lastRefresh is the tick instant of the last successful read. Zero
	// until the first one, so the first tick always refreshes.
	lastRefresh time.Time
	// publishedStates records which state labels the previous refresh set,
	// so a state that has since dropped to no rows can be zeroed instead
	// of keeping the count it had. Only the runner goroutine touches it.
	publishedStates map[string]struct{}
}

// New constructs a Job reading through the supplied database pool.
// Returns an error when a dependency is missing so cmd/worker can refuse
// to register a job that could never produce a number.
func New(db *sql.DB, logger *slog.Logger) (*Job, error) {
	if db == nil {
		return nil, errors.New("business_metrics: db is required")
	}
	if logger == nil {
		return nil, errors.New("business_metrics: logger is required")
	}
	return &Job{
		Counts:          generated.New(db),
		Logger:          logger,
		RefreshInterval: defaultRefreshInterval,
		ActiveWindow:    defaultActiveWindow,
	}, nil
}

// Name returns the stable runner identifier for the business metrics job.
func (j *Job) Name() string { return JobName }

// Tick refreshes the gauges when the configured interval has elapsed
// since the last successful refresh. `now` is the tick instant the runner
// observed, used both for the throttle and as the upper bound of the
// activity window so the behaviour is deterministic under test.
//
// A failed read returns the error and leaves every gauge holding its
// previous value. The gauges are deliberately NOT reset on failure: zero
// is a legal reading of these metrics ("this instance has no tasks"), so
// a database blip would publish a confident lie and fire whatever is
// wired to a task count hitting the floor. A slightly stale number is a
// far smaller error, and the failed read is in the log and in the
// runner's tick record.
func (j *Job) Tick(ctx context.Context, now time.Time) error {
	if !j.due(now) {
		return nil
	}

	states, err := j.Counts.CountTasksByDerivedState(ctx)
	if err != nil {
		j.Logger.ErrorContext(ctx, "business_metrics: task count refresh failed, gauges left at their previous values",
			slog.Any("err", err),
		)
		return fmt.Errorf("business_metrics: count tasks by state: %w", err)
	}

	since := now.Add(-j.activeWindow())
	active, err := j.Counts.CountWorkspacesWithRecentActivity(ctx, since.UTC())
	if err != nil {
		j.Logger.ErrorContext(ctx, "business_metrics: active workspace refresh failed, gauges left at their previous values",
			slog.Any("err", err),
			slog.Time("since", since),
		)
		return fmt.Errorf("business_metrics: count active workspaces: %w", err)
	}

	j.publish(states, active)
	j.lastRefresh = now
	return nil
}

// due reports whether enough time has passed since the last successful
// refresh. The runner's ticker never fires early, so a RefreshInterval
// equal to the runner cadence refreshes on every tick.
func (j *Job) due(now time.Time) bool {
	if j.lastRefresh.IsZero() {
		return true
	}
	return !now.Before(j.lastRefresh.Add(j.refreshInterval()))
}

// publish writes one read's results to the gauges. The per-state gauge is
// zeroed for every label the previous refresh published and this one did
// not: a state the query returns no row for has no tasks in it, and
// leaving the old value there would report tasks that are no longer in
// that state. The label is kept at zero rather than deleted so the series
// stays continuous across the transition.
func (j *Job) publish(states []generated.CountTasksByDerivedStateRow, active int64) {
	current := make(map[string]struct{}, len(states))
	var total int64
	for _, s := range states {
		label := string(s.DerivedState)
		obs.TasksByStateGauge.WithLabelValues(label).Set(float64(s.Total))
		current[label] = struct{}{}
		total += s.Total
	}
	for label := range j.publishedStates {
		if _, ok := current[label]; !ok {
			obs.TasksByStateGauge.WithLabelValues(label).Set(0)
		}
	}
	j.publishedStates = current

	// nf_tasks_total is summed from the same rows rather than queried
	// separately, so it can never disagree with its own breakdown.
	obs.TasksTotalGauge.Set(float64(total))
	obs.ActiveWorkspacesGauge.Set(float64(active))
}

func (j *Job) refreshInterval() time.Duration {
	if j.RefreshInterval <= 0 {
		return defaultRefreshInterval
	}
	return j.RefreshInterval
}

func (j *Job) activeWindow() time.Duration {
	if j.ActiveWindow <= 0 {
		return defaultActiveWindow
	}
	return j.ActiveWindow
}
