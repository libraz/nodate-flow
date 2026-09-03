// Package jobs hosts the flow-worker job runner and the individual
// scheduled jobs registered against it.
//
// The runner is intentionally small: jobs register themselves by
// calling Runner.Register before Runner.Start. The calendar_event_day
// materialiser is the first such job.
package jobs

import (
	"context"
	"errors"
	"log/slog"
	"sync"
	"time"
)

// Job is the unit the runner ticks on its own goroutine.
//
// Tick should return promptly when ctx is cancelled. The runner guarantees
// that at most one Tick of a given job is in-flight at a time; long jobs
// must therefore manage their own concurrency if they fan out.
type Job interface {
	// Name is used for slog fields and future per-job metric labels.
	// MUST be stable across restarts and unique within a Runner.
	Name() string
	// Tick performs one cycle of work. now is the tick instant the runner
	// observed; jobs that key on a clock (e.g. day boundaries) MUST use
	// it rather than reading time.Now() so behaviour is deterministic
	// under test.
	Tick(ctx context.Context, now time.Time) error
}

// Runner ticks every registered Job on a shared interval configured by
// NF_FLOW_WORKER_TICK_INTERVAL. There are no per-job overrides; a job
// needing a different cadence gets its own Runner.
type Runner struct {
	// Interval is the period between ticks. Must be > 0.
	Interval time.Duration
	// Logger receives structured tick / error events. Required.
	Logger *slog.Logger
	// ShutdownTimeout caps how long Stop waits for an in-flight tick to
	// finish before returning. The caller is expected to abandon the
	// goroutine after that ceiling; in-flight ticks observe ctx cancel
	// and bail on their own.
	ShutdownTimeout time.Duration

	jobs []Job

	mu      sync.Mutex
	running bool
	done    chan struct{}
}

// Register adds a job to the runner. MUST be called before Start; the
// runner does not support dynamic registration after Start (main wires
// the calendar_event_day job and is the only caller for now).
func (r *Runner) Register(j Job) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.running {
		panic("jobs: Runner.Register called after Start")
	}
	r.jobs = append(r.jobs, j)
}

// Registered reports how many jobs are registered. A runner with none
// ticks quietly forever, which is indistinguishable from a healthy one
// at every other observation point — the boot sequence uses this to say
// so out loud.
func (r *Runner) Registered() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.jobs)
}

// Start launches the tick loop in its own goroutine and returns. The loop
// exits when ctx is cancelled; callers MUST then call Stop to wait for
// any in-flight tick to drain.
//
// When no jobs are registered Start still runs the loop, logging a debug
// "tick" line per interval. This is intentional so the binary stays
// observably alive even with no job registered.
func (r *Runner) Start(ctx context.Context) error {
	r.mu.Lock()
	if r.running {
		r.mu.Unlock()
		return errors.New("jobs: Runner already started")
	}
	if r.Interval <= 0 {
		r.mu.Unlock()
		return errors.New("jobs: Runner.Interval must be positive")
	}
	if r.Logger == nil {
		r.mu.Unlock()
		return errors.New("jobs: Runner.Logger is required")
	}
	r.running = true
	r.done = make(chan struct{})
	jobs := append([]Job(nil), r.jobs...)
	r.mu.Unlock()

	go r.loop(ctx, jobs)
	return nil
}

// loop runs the tick cycle until ctx is cancelled. Each tick runs all
// registered jobs sequentially so a slow job delays the next cycle; this
// keeps the runner simple. If a future job needs isolated cadence
// it can be registered as a separate Runner instance.
func (r *Runner) loop(ctx context.Context, jobs []Job) {
	defer close(r.done)

	t := time.NewTicker(r.Interval)
	defer t.Stop()

	for {
		select {
		case <-ctx.Done():
			r.Logger.Info("jobs: runner stopping", "reason", ctx.Err())
			return
		case now := <-t.C:
			r.runOnce(ctx, jobs, now)
		}
	}
}

// runOnce executes every registered job once. Errors are logged and the
// loop continues so one failing job cannot starve the others.
func (r *Runner) runOnce(ctx context.Context, jobs []Job, now time.Time) {
	if len(jobs) == 0 {
		r.Logger.Debug("jobs: tick (no jobs registered)", "at", now)
		return
	}
	for _, j := range jobs {
		start := time.Now()
		if err := j.Tick(ctx, now); err != nil {
			r.Logger.Error("jobs: tick failed",
				"job", j.Name(),
				"err", err,
				"duration", time.Since(start),
			)
			continue
		}
		r.Logger.Debug("jobs: tick ok",
			"job", j.Name(),
			"duration", time.Since(start),
		)
	}
}

// Stop blocks until the in-flight tick (if any) finishes, capped by
// r.ShutdownTimeout. Safe to call even if Start was never invoked or has
// already returned.
func (r *Runner) Stop() {
	r.mu.Lock()
	done := r.done
	r.mu.Unlock()
	if done == nil {
		return
	}
	timeout := r.ShutdownTimeout
	if timeout <= 0 {
		timeout = 30 * time.Second
	}
	select {
	case <-done:
	case <-time.After(timeout):
		r.Logger.Warn("jobs: shutdown timeout exceeded; abandoning in-flight tick",
			"timeout", timeout,
		)
	}
}
