package autoactions

import (
	"context"
	"log/slog"
	"testing"
	"time"
)

// runStarts signals every run that reached its loop. Cancelling a
// context succeeds whether or not anything is watching it, so without
// this signal every assertion below would hold just as well for an
// executor that started nothing.
type runStarts struct{ ch chan struct{} }

func (h *runStarts) Enabled(context.Context, slog.Level) bool { return true }

// Handle records a run start and discards everything else. The send is
// non-blocking so a run is never held up by a test that is not looking.
func (h *runStarts) Handle(_ context.Context, r slog.Record) error {
	if r.Message == startedMessage {
		select {
		case h.ch <- struct{}{}:
		default:
		}
	}
	return nil
}

func (h *runStarts) WithAttrs([]slog.Attr) slog.Handler { return h }

func (h *runStarts) WithGroup(string) slog.Handler { return h }

// newIdleExecutor builds an executor whose interval is long enough that
// no tick fires during a test, so Start blocks on nothing but its
// context and never reaches the database.
func newIdleExecutor() (*Executor, *runStarts) {
	starts := &runStarts{ch: make(chan struct{}, 4)}
	return &Executor{
		Config: ExecutorConfig{Interval: time.Hour},
		Logger: slog.New(starts),
	}, starts
}

// startRun launches Start on a context of its own — which is how the
// supervisor runs it — and returns that context's cancel plus a channel
// closed when the run returns. It waits for the run to reach its loop,
// so the cancel that follows cannot race ahead of the run it is meant
// to end.
func startRun(t *testing.T, e *Executor, starts *runStarts) (context.CancelFunc, <-chan struct{}) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		e.Start(ctx)
	}()

	select {
	case <-starts.ch:
	case <-time.After(2 * time.Second):
		cancel()
		t.Fatal("Start did not begin a run")
	}
	return cancel, done
}

// requireStopped cancels the run's context and requires the run to end.
func requireStopped(t *testing.T, cancel context.CancelFunc, done <-chan struct{}, run int) {
	t.Helper()
	cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatalf("run %d kept running after its context was cancelled", run)
	}
}

// Cancellation is the whole stop, so it has to end the run it is called
// against — worth asserting on its own, since an executor that never
// starts anything satisfies every restart assertion below for free.
func TestCancellingTheContextEndsTheRun(t *testing.T) {
	t.Parallel()
	e, starts := newIdleExecutor()
	cancel, done := startRun(t, e, starts)
	requireStopped(t, cancel, done, 1)
}

// Start is handed to a supervisor that starts it again whenever it
// returns, so start / stop / start is an ordinary sequence rather than a
// test-only one. Each run must end on its own context; anything the
// loop watches that outlives a single run ends the first run and leaves
// the second ticking with nothing able to reach it.
func TestEveryRunIsStoppable(t *testing.T) {
	t.Parallel()
	e, starts := newIdleExecutor()
	for run := 1; run <= 2; run++ {
		cancel, done := startRun(t, e, starts)
		requireStopped(t, cancel, done, run)
	}
}

// A shutdown may cancel the same context more than once — a stopper
// called by both a failed startup and the shutdown path lands here —
// and may cancel one whose run has already returned.
func TestRedundantCancellationIsHarmless(t *testing.T) {
	t.Parallel()
	e, starts := newIdleExecutor()

	cancel, done := startRun(t, e, starts)
	requireStopped(t, cancel, done, 1)
	cancel()
	cancel()
}

// A run handed a context that is already cancelled must return instead
// of ticking on: the supervisor can enter Start again while the
// shutdown that cancelled the context is still in progress.
func TestARunStartedOnACancelledContextReturns(t *testing.T) {
	t.Parallel()
	e, _ := newIdleExecutor()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	done := make(chan struct{})
	go func() {
		defer close(done)
		e.Start(ctx)
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("a run whose context was already cancelled kept running")
	}
}
