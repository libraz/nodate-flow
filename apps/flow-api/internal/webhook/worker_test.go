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
