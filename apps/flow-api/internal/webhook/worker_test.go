package webhook

import (
	"context"
	"sync"
	"testing"
	"time"
)

// TestHookContextDetached asserts that the goroutine spawned by Hook
// keeps running after the parent request context is cancelled. This is
// the regression guard for the bug where Hook used context.Background(),
// dropping trace span / logger values, while a context.WithCancel-
// derived context would have aborted in-flight DB writes the moment the
// HTTP handler returned.
func TestHookContextDetached(t *testing.T) {
	t.Parallel()

	started := make(chan struct{})
	finished := make(chan struct{})

	w := &Worker{
		run: func(ctx context.Context, _ uint32, _ string) {
			close(started)
			// Wait for cancellation OR the test to give up; the
			// context must NOT be cancelled when the parent is.
			select {
			case <-ctx.Done():
				t.Errorf("detached context unexpectedly cancelled: %v", ctx.Err())
			case <-time.After(50 * time.Millisecond):
			}
			close(finished)
		},
	}

	parent, cancelParent := context.WithCancel(context.Background())
	w.Hook()(parent, 1, "task.created", 0)

	// Wait until the goroutine has started, then cancel the parent.
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("hook goroutine did not start")
	}
	cancelParent()

	select {
	case <-finished:
	case <-time.After(time.Second):
		t.Fatal("hook goroutine did not finish")
	}
}

// TestHookRecoversPanic asserts that a panic in the inner work function
// is recovered, so a single bad event payload cannot crash the flow-api
// process. Without the deferred recover() the test goroutine would
// abort the test runner.
func TestHookRecoversPanic(t *testing.T) {
	t.Parallel()

	var wg sync.WaitGroup
	wg.Add(1)
	w := &Worker{
		run: func(_ context.Context, _ uint32, _ string) {
			defer wg.Done()
			panic("boom")
		},
	}

	w.Hook()(context.Background(), 1, "task.created", 0)

	done := make(chan struct{})
	go func() {
		wg.Wait()
		close(done)
	}()

	select {
	case <-done:
		// Give the deferred recover a chance to run.
		time.Sleep(10 * time.Millisecond)
	case <-time.After(time.Second):
		t.Fatal("hook goroutine did not run")
	}
}

// TestStopIsIdempotent asserts that Stop can be reached twice — a
// signal handler and an explicit call on the same worker — without
// panicking, and that a single Stop still ends the delivery loop.
//
// Both halves matter: a Stop that closed nothing would never panic and
// would also never stop anything, so the loop is observed here through
// w.done alone, with cancel left unset.
func TestStopIsIdempotent(t *testing.T) {
	t.Parallel()

	w := NewWorker(nil, nil)

	looped := make(chan struct{})
	go func() {
		defer close(looped)
		w.loop(context.Background())
	}()

	w.Stop()
	w.Stop()

	select {
	case <-looped:
	case <-time.After(time.Second):
		t.Fatal("delivery loop did not exit after Stop")
	}
}

// TestShutdownWaitsForHookJobs holds the drain the hook never had: a
// delivery row still being written when the process exits is an event
// whose webhook is lost, so Shutdown must wait for it, and a hook fired
// after Shutdown began must be refused rather than started.
func TestShutdownWaitsForHookJobs(t *testing.T) {
	t.Parallel()

	release := make(chan struct{})
	started := make(chan struct{})
	var once sync.Once
	calls := 0
	var mu sync.Mutex
	w := &Worker{
		run: func(_ context.Context, _ uint32, _ string) {
			mu.Lock()
			calls++
			mu.Unlock()
			once.Do(func() { close(started) })
			<-release
		},
	}

	w.Hook()(context.Background(), 1, "task.created", 0)
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("hook job did not start")
	}

	done := make(chan error, 1)
	go func() { done <- w.Shutdown(context.Background()) }()
	select {
	case err := <-done:
		t.Fatalf("Shutdown returned while a hook job was in flight (err=%v)", err)
	case <-time.After(50 * time.Millisecond):
	}

	w.Hook()(context.Background(), 1, "task.created", 0)
	close(release)
	if err := <-done; err != nil {
		t.Fatalf("Shutdown: %v", err)
	}
	mu.Lock()
	defer mu.Unlock()
	if calls != 1 {
		t.Fatalf("hook ran %d times, want 1: a hook fired after Shutdown must be refused", calls)
	}
}
