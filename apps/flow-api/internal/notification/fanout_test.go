// Tests for the Fanout goroutine lifecycle. The work function (f.run)
// is overridden so the goroutine plumbing — detached cancellation,
// per-event timeout, and Shutdown drain — can be exercised without a
// live MySQL connection.
package notification

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"
)

// TestHook_DetachesFromParentContext verifies that cancelling the
// parent context passed to the hook does not abort the spawned
// fan-out goroutine. The detached child should observe context.Canceled
// only on its own deadline, never propagated from the parent.
func TestHook_DetachesFromParentContext(t *testing.T) {
	t.Parallel()

	f := NewFanout(nil, nil, nil)
	f.SetTimeout(2 * time.Second)

	done := make(chan struct{})
	var observedErr atomic.Value // stores error
	f.run = func(ctx context.Context, _ uint32, _ string, _ uint32) {
		defer close(done)
		// Wait long enough that, if cancellation propagated from the
		// parent, ctx would be Done immediately.
		select {
		case <-ctx.Done():
			observedErr.Store(ctx.Err())
		case <-time.After(200 * time.Millisecond):
			// Healthy path: no cancellation, work completes.
		}
	}

	parent, cancel := context.WithCancel(context.Background())
	hook := f.Hook()
	hook(parent, 1, "task.created", 0)
	// Cancel the parent immediately; the fan-out goroutine must
	// continue running to completion.
	cancel()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("fanout goroutine did not run after parent context was cancelled")
	}

	if v := observedErr.Load(); v != nil {
		t.Fatalf("fanout context was cancelled even though parent cancellation should be detached: %v", v)
	}

	// Drain to keep things tidy.
	if err := f.Shutdown(context.Background()); err != nil {
		t.Fatalf("Shutdown returned unexpected error: %v", err)
	}
}

// TestHook_TimeoutAborts verifies that work exceeding the configured
// timeout sees ctx.Err() == context.DeadlineExceeded.
func TestHook_TimeoutAborts(t *testing.T) {
	t.Parallel()

	f := NewFanout(nil, nil, nil)
	f.SetTimeout(50 * time.Millisecond)

	done := make(chan struct{})
	var observedErr atomic.Value
	f.run = func(ctx context.Context, _ uint32, _ string, _ uint32) {
		defer close(done)
		// Block until the timeout fires.
		select {
		case <-ctx.Done():
			observedErr.Store(ctx.Err())
		case <-time.After(2 * time.Second):
			t.Error("timeout did not fire within 2s budget")
		}
	}

	hook := f.Hook()
	hook(context.Background(), 1, "task.created", 0)

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("fanout goroutine did not finish")
	}

	gotV := observedErr.Load()
	if gotV == nil {
		t.Fatal("expected DeadlineExceeded but ctx.Done was never observed")
	}
	got, _ := gotV.(error)
	if !errors.Is(got, context.DeadlineExceeded) {
		t.Fatalf("expected context.DeadlineExceeded, got %v", got)
	}

	if err := f.Shutdown(context.Background()); err != nil {
		t.Fatalf("Shutdown returned unexpected error: %v", err)
	}
}

// TestShutdown_WaitsForInFlight verifies that Shutdown blocks until
// every in-flight goroutine has returned, and that hooks fired after
// Shutdown was initiated are dropped.
func TestShutdown_WaitsForInFlight(t *testing.T) {
	t.Parallel()

	f := NewFanout(nil, nil, nil)
	f.SetTimeout(5 * time.Second)

	release := make(chan struct{})
	started := make(chan struct{})
	var finished atomic.Int32
	var startedOnce atomic.Bool
	f.run = func(_ context.Context, _ uint32, _ string, _ uint32) {
		if startedOnce.CompareAndSwap(false, true) {
			close(started)
		}
		<-release
		finished.Add(1)
	}

	hook := f.Hook()
	hook(context.Background(), 1, "task.created", 0)

	// Wait for the goroutine to actually start so we know Shutdown
	// has something to wait on.
	select {
	case <-started:
	case <-time.After(2 * time.Second):
		t.Fatal("fanout goroutine did not start")
	}

	// Shutdown should block until release is closed.
	shutdownDone := make(chan error, 1)
	go func() {
		shutdownDone <- f.Shutdown(context.Background())
	}()

	select {
	case err := <-shutdownDone:
		t.Fatalf("Shutdown returned before in-flight work finished (err=%v)", err)
	case <-time.After(100 * time.Millisecond):
	}

	// New hooks fired after Shutdown started must be dropped.
	hook(context.Background(), 1, "task.created", 0)
	if got := finished.Load(); got != 0 {
		t.Fatalf("expected 0 finished goroutines before release, got %d", got)
	}

	// Release the in-flight goroutine and confirm Shutdown returns.
	close(release)

	select {
	case err := <-shutdownDone:
		if err != nil {
			t.Fatalf("Shutdown returned error: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Shutdown did not return after in-flight goroutine finished")
	}

	if got := finished.Load(); got != 1 {
		t.Fatalf("expected exactly 1 finished goroutine (post-shutdown hook should be dropped), got %d", got)
	}
}

// TestShutdown_ContextDeadline verifies that Shutdown returns the
// supplied context's error when the wait budget is exhausted before
// in-flight work finishes.
func TestShutdown_ContextDeadline(t *testing.T) {
	t.Parallel()

	f := NewFanout(nil, nil, nil)
	f.SetTimeout(5 * time.Second)

	release := make(chan struct{})
	defer close(release)

	started := make(chan struct{})
	var startedOnce atomic.Bool
	f.run = func(_ context.Context, _ uint32, _ string, _ uint32) {
		if startedOnce.CompareAndSwap(false, true) {
			close(started)
		}
		<-release
	}

	hook := f.Hook()
	hook(context.Background(), 1, "task.created", 0)

	select {
	case <-started:
	case <-time.After(2 * time.Second):
		t.Fatal("fanout goroutine did not start")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	err := f.Shutdown(ctx)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("expected context.DeadlineExceeded, got %v", err)
	}
}
