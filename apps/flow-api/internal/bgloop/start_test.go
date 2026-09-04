package bgloop

import (
	"bytes"
	"context"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// quitLoop is the shape of a supervised component that also owns an
// out-of-band stop: it blocks until either its context is cancelled or
// its own quit channel closes, and reports how many times it was
// entered. The webhook worker and the importer worker both have this
// shape.
func quitLoop(entries *atomic.Int32, quit <-chan struct{}) func(context.Context) {
	return func(ctx context.Context) {
		entries.Add(1)
		select {
		case <-ctx.Done():
		case <-quit:
		}
	}
}

// TestStopperEndsTheLoopWithoutARestart pins what a shutdown needs from
// [Start]: after the stopper runs, the loop is not running, and stays
// not running even though the component's own stop signal arrives too.
//
// A restart here is not a cosmetic fault. Everything after the stop in a
// shutdown sequence is tearing the process down, so a loop brought back
// at that point ticks against a database pool that is closing under it,
// for the whole drain window.
func TestStopperEndsTheLoopWithoutARestart(t *testing.T) {
	ResetStats()
	shortBackoff(t)
	buf := &bytes.Buffer{}

	var entries atomic.Int32
	quit := make(chan struct{})
	stop := Start(context.Background(), "test.stopped", testLogger(buf), quitLoop(&entries, quit))

	waitFor(t, func() bool { return entries.Load() == 1 }, "the loop must start")

	// The stopper first, then the component's own signal: the supervisor
	// must already see a cancelled context by the time the loop returns.
	stop()
	close(quit)

	// A restart costs one backoff, shortened to a millisecond here, so
	// this window is many times over what one would need.
	time.Sleep(50 * time.Millisecond)
	if got := entries.Load(); got != 1 {
		t.Fatalf("the loop was entered %d times; a stopped loop must not be started again", got)
	}
	waitFor(t, func() bool { return !Snapshot()["test.stopped"].Running }, "the supervisor must return")
	snap := Snapshot()["test.stopped"]
	if snap.Restarts != 0 || snap.Returns != 0 {
		t.Errorf("a stopped loop must record no failure, got %+v", snap)
	}
	if strings.Contains(buf.String(), "returned early") {
		t.Errorf("a stopped loop must not be reported as an early return:\n%s", buf.String())
	}
}

// TestALoopStoppedWithoutItsCancelIsRestarted is the other half, and the
// reason [Start] hands back one stopper instead of leaving the cancel to
// the caller: a loop ended through its own signal while its context is
// still live is indistinguishable from a loop that died, and comes back.
func TestALoopStoppedWithoutItsCancelIsRestarted(t *testing.T) {
	ResetStats()
	shortBackoff(t)
	buf := &bytes.Buffer{}

	var entries atomic.Int32
	quit := make(chan struct{})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	done := make(chan struct{})
	go func() {
		defer close(done)
		Run(ctx, "test.quit_only", testLogger(buf), quitLoop(&entries, quit))
	}()

	waitFor(t, func() bool { return entries.Load() == 1 }, "the loop must start")
	close(quit)

	waitFor(t, func() bool { return entries.Load() >= 2 },
		"a loop stopped without cancelling its context is restarted, which is what the stopper prevents")
	cancel()
	<-done
}

// TestStopperIsSafeToCallTwice covers the shutdown path calling a
// stopper that a failed startup already ran.
func TestStopperIsSafeToCallTwice(t *testing.T) {
	ResetStats()
	shortBackoff(t)

	var entries atomic.Int32
	quit := make(chan struct{})
	defer close(quit)
	stop := Start(context.Background(), "test.twice", testLogger(&bytes.Buffer{}), quitLoop(&entries, quit))

	waitFor(t, func() bool { return entries.Load() == 1 }, "the loop must start")
	stop()
	stop()

	waitFor(t, func() bool { return !Snapshot()["test.twice"].Running }, "the supervisor must return")
}

// TestStopperDoesNotWaitForTheLoop keeps a loop that ignores its context
// from holding the shutdown open: the drain budget belongs to in-flight
// requests, not to a background pass.
func TestStopperDoesNotWaitForTheLoop(t *testing.T) {
	ResetStats()
	shortBackoff(t)

	release := make(chan struct{})
	defer close(release)
	started := make(chan struct{})
	stop := Start(context.Background(), "test.deaf", testLogger(&bytes.Buffer{}), func(context.Context) {
		close(started)
		<-release
	})
	<-started

	returned := make(chan struct{})
	go func() {
		stop()
		close(returned)
	}()
	select {
	case <-returned:
	case <-time.After(time.Second):
		t.Fatal("the stopper must not block on a loop that ignores its context")
	}
}
