package bgloop

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"
)

// TestHookRunnerBoundsConcurrency fires a burst far larger than the
// runner's bound and requires that no more than the bound ever run at
// once, and that every accepted job still runs.
func TestHookRunnerBoundsConcurrency(t *testing.T) {
	t.Parallel()

	const bound, burst = 3, 40
	r := NewHookRunner("test.bound", bound, time.Second)

	var running, peak, ran atomic.Int32
	for range burst {
		if !r.Go(context.Background(), 0, func(context.Context) {
			n := running.Add(1)
			for {
				p := peak.Load()
				if n <= p || peak.CompareAndSwap(p, n) {
					break
				}
			}
			time.Sleep(5 * time.Millisecond)
			running.Add(-1)
			ran.Add(1)
		}) {
			t.Fatal("a runner that has not been shut down must accept jobs")
		}
	}
	if err := r.Shutdown(context.Background()); err != nil {
		t.Fatalf("Shutdown: %v", err)
	}
	if got := peak.Load(); got > bound {
		t.Errorf("peak concurrency = %d, want <= %d", got, bound)
	}
	if got := ran.Load(); got != burst {
		t.Errorf("ran %d jobs, want every accepted job (%d)", got, burst)
	}
}

// TestHookRunnerDetachesAndBoundsEachJob holds the two context rules: the
// submitting request's cancellation does not reach the job, and the job's
// own deadline does.
func TestHookRunnerDetachesAndBoundsEachJob(t *testing.T) {
	t.Parallel()

	r := NewHookRunner("test.deadline", 1, 30*time.Millisecond)
	parent, cancel := context.WithCancel(context.Background())
	errc := make(chan error, 1)
	r.Go(parent, 0, func(ctx context.Context) {
		<-ctx.Done()
		errc <- ctx.Err()
	})
	cancel()

	select {
	case err := <-errc:
		if !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("job ended with %v, want its own deadline rather than the parent's cancel", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("job deadline never fired")
	}
}

// TestHookRunnerDrainsAndRefusesAfterShutdown holds the shutdown
// contract: queued work is waited for, work after Shutdown is refused,
// and a panicking job neither ends the process nor wedges the drain.
func TestHookRunnerDrainsAndRefusesAfterShutdown(t *testing.T) {
	t.Parallel()

	r := NewHookRunner("test.drain", 1, time.Second)
	release := make(chan struct{})
	var ran atomic.Int32
	r.Go(context.Background(), 0, func(context.Context) { <-release; ran.Add(1) })
	r.Go(context.Background(), 0, func(context.Context) { panic("bad event") })
	r.Go(context.Background(), 0, func(context.Context) { ran.Add(1) })

	done := make(chan error, 1)
	go func() { done <- r.Shutdown(context.Background()) }()
	time.Sleep(20 * time.Millisecond)
	if r.Go(context.Background(), 0, func(context.Context) { ran.Add(1) }) {
		t.Error("a job submitted after Shutdown must be refused")
	}
	select {
	case <-done:
		t.Fatal("Shutdown returned while accepted jobs were still queued")
	default:
	}
	close(release)
	if err := <-done; err != nil {
		t.Fatalf("Shutdown: %v", err)
	}
	if got := ran.Load(); got != 2 {
		t.Errorf("ran %d jobs, want the two accepted non-panicking ones", got)
	}
}
