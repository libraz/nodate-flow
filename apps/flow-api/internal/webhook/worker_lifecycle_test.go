package webhook

import (
	"context"
	"testing"

	"github.com/libraz/nodate-flow/apps/flow-api/internal/bgloop"
)

func TestWorkerStopSignalAndSupervisorCompletionAreSeparate(t *testing.T) {
	w := NewWorker(nil, nil)
	w.Start(context.Background())

	w.BeginStop()
	select {
	case <-w.done:
	default:
		t.Fatal("BeginStop did not close the component stop signal")
	}
	if w.handle == nil {
		t.Fatal("Start did not retain the managed supervisor handle")
	}
	if err := w.Wait(context.Background()); err != nil {
		t.Fatalf("Wait: %v", err)
	}
	if err := w.Shutdown(context.Background()); err != nil {
		t.Fatalf("repeated Shutdown: %v", err)
	}
}

func TestWorkerStopBeforeStartPreventsLateStart(t *testing.T) {
	w := NewWorker(nil, nil)
	if err := w.Shutdown(context.Background()); err != nil {
		t.Fatalf("Shutdown before Start: %v", err)
	}
	w.Start(context.Background())
	if w.handle != nil {
		t.Fatal("Start after stop created a resident loop")
	}
}

// TestWorkerBeginStopIsNotReadAsAnEarlyReturn: the loop wakes on either
// its context or w.done. If w.done closed first, the supervisor would see
// a loop returning under a live context and count it as a failure.
func TestWorkerBeginStopIsNotReadAsAnEarlyReturn(t *testing.T) {
	const name = "webhook.worker"
	before := bgloop.Snapshot()[name].Returns
	for i := 0; i < 20000; i++ {
		w := NewWorker(nil, nil)
		w.Start(context.Background())
		if err := w.Shutdown(context.Background()); err != nil {
			t.Fatalf("Shutdown: %v", err)
		}
	}
	if got := bgloop.Snapshot()[name].Returns - before; got != 0 {
		t.Fatalf("the supervisor recorded %d early returns during BeginStop, want 0", got)
	}
}
