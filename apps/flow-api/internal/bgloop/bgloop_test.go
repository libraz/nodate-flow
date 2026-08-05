package bgloop

import (
	"bytes"
	"context"
	"log/slog"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// testLogger returns a logger writing into buf so the tests can assert
// what an operator would actually see.
func testLogger(buf *bytes.Buffer) *slog.Logger {
	return slog.New(slog.NewJSONHandler(buf, &slog.HandlerOptions{Level: slog.LevelDebug}))
}

// shortBackoff makes the restart delay negligible for the duration of a
// test. The production schedule is measured in seconds.
func shortBackoff(t *testing.T) {
	t.Helper()
	prevInitial, prevMax := initialBackoff, maxBackoff
	initialBackoff, maxBackoff = time.Millisecond, 2*time.Millisecond
	t.Cleanup(func() { initialBackoff, maxBackoff = prevInitial, prevMax })
}

// TestPanicIsContainedAndRestarted is the core of the guard: a panic in
// a background pass must not reach the runtime's default handler, which
// would end the process — every tenant's API down because one
// workspace's data tripped one loop.
func TestPanicIsContainedAndRestarted(t *testing.T) {
	ResetStats()
	shortBackoff(t)
	buf := &bytes.Buffer{}

	var calls atomic.Int32
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	done := make(chan struct{})
	go func() {
		defer close(done)
		Run(ctx, "test.panics", testLogger(buf), func(ctx context.Context) {
			if calls.Add(1) <= 2 {
				panic("workspace 42 tripped this pass")
			}
			<-ctx.Done()
		})
	}()

	waitFor(t, func() bool { return calls.Load() >= 3 }, "the loop must be restarted after a panic")
	cancel()
	<-done

	snap := Snapshot()["test.panics"]
	if snap.Panics != 2 {
		t.Errorf("Panics = %d, want 2", snap.Panics)
	}
	if snap.Restarts < 2 {
		t.Errorf("Restarts = %d, want at least 2", snap.Restarts)
	}
	if !strings.Contains(snap.LastFailure, "workspace 42") {
		t.Errorf("LastFailure must carry the panic value, got %q", snap.LastFailure)
	}

	// Recovering quietly would be no better than crashing: the operator
	// has to be able to see it happened, and where.
	logged := buf.String()
	for _, want := range []string{"background loop panicked", "test.panics", "workspace 42", "stack"} {
		if !strings.Contains(logged, want) {
			t.Errorf("the restart log must carry %q; got:\n%s", want, logged)
		}
	}
}

// TestEarlyReturnIsReportedAndRestarted covers the other half. A loop
// that returns on its own leaves the process healthy to every probe
// while the work it did simply stops — the failure mode with no
// symptom. It must be restarted and, above all, said out loud.
func TestEarlyReturnIsReportedAndRestarted(t *testing.T) {
	ResetStats()
	shortBackoff(t)
	buf := &bytes.Buffer{}

	var calls atomic.Int32
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	done := make(chan struct{})
	go func() {
		defer close(done)
		Run(ctx, "test.returns", testLogger(buf), func(ctx context.Context) {
			if calls.Add(1) <= 2 {
				return // the "transient error mistaken for fatal" shape
			}
			<-ctx.Done()
		})
	}()

	waitFor(t, func() bool { return calls.Load() >= 3 }, "a loop that returned must be restarted")
	cancel()
	<-done

	snap := Snapshot()["test.returns"]
	if snap.Returns != 2 {
		t.Errorf("Returns = %d, want 2", snap.Returns)
	}
	if snap.Panics != 0 {
		t.Errorf("Panics = %d, want 0 (nothing panicked)", snap.Panics)
	}
	if !strings.Contains(buf.String(), "background loop returned early") {
		t.Errorf("an early return must be logged; got:\n%s", buf.String())
	}
}

// TestCancelIsACleanStop keeps the supervisor from crying wolf: the
// documented way to stop a loop must not be recorded as a failure, or
// every deploy would produce an alert and the real ones would be
// ignored.
func TestCancelIsACleanStop(t *testing.T) {
	ResetStats()
	shortBackoff(t)
	buf := &bytes.Buffer{}

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		Run(ctx, "test.clean", testLogger(buf), func(ctx context.Context) { <-ctx.Done() })
	}()

	time.Sleep(5 * time.Millisecond)
	cancel()
	<-done

	snap := Snapshot()["test.clean"]
	if snap.Panics != 0 || snap.Returns != 0 {
		t.Errorf("a cancelled loop must record no failure, got %+v", snap)
	}
	if snap.Running {
		t.Error("Running must be false once the supervisor returns")
	}
	if strings.Contains(buf.String(), "returned early") {
		t.Errorf("a cancelled loop must not be reported as an early return:\n%s", buf.String())
	}
	if !strings.Contains(buf.String(), "background loop stopped") {
		t.Errorf("a clean stop should still be visible:\n%s", buf.String())
	}
}

func waitFor(t *testing.T, cond func() bool, msg string) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for {
		if cond() {
			return
		}
		if time.Now().After(deadline) {
			t.Fatal(msg)
		}
		time.Sleep(time.Millisecond)
	}
}
