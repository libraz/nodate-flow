package bgloop

import (
	"bytes"
	"context"
	"sync/atomic"
	"testing"
)

func TestManagedHandleWaitTracksWholeSupervisorAfterPanic(t *testing.T) {
	shortBackoff(t)
	ResetStats()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	var calls atomic.Int32
	secondRun := make(chan struct{})
	h := StartManaged(ctx, "test.managed.panic", testLogger(&bytes.Buffer{}), func(ctx context.Context) {
		if calls.Add(1) == 1 {
			panic("first pass")
		}
		close(secondRun)
		<-ctx.Done()
	})

	<-secondRun

	// A completed panic iteration must not satisfy Wait: the supervisor
	// has restarted the body and is still active until its managed context
	// is cancelled.
	waitCtx, stopWait := context.WithCancel(context.Background())
	stopWait()
	if err := h.Wait(waitCtx); err != context.Canceled {
		t.Fatalf("Wait with a canceled context = %v, want context.Canceled while the supervisor is active", err)
	}

	h.Stop()
	if err := h.Wait(context.Background()); err != nil {
		t.Fatalf("Wait after Stop: %v", err)
	}
}
