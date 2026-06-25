package gateway

import (
	"context"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// recordingEmitter is a debounceEmitter that captures every Emit call
// in order. Tests inspect the captured slice to assert on debounce
// behaviour without binding any HTTP listeners.
type recordingEmitter struct {
	mu     sync.Mutex
	events []PresenceEvent
}

func (r *recordingEmitter) Emit(_ context.Context, ev PresenceEvent) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.events = append(r.events, ev)
}

func (r *recordingEmitter) snapshot() []PresenceEvent {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]PresenceEvent, len(r.events))
	copy(out, r.events)
	return out
}

// newTestDebouncer wires a Debouncer with a fixed now() so the
// leading-edge gate is deterministic. The trailing timer still uses
// real time.AfterFunc, which is fine at the chosen window (a few ms)
// because we Eventually-poll the captured events rather than sleep
// for an exact duration.
func newTestDebouncer(t *testing.T, window time.Duration) (*Debouncer, *recordingEmitter, *time.Time) {
	t.Helper()
	rec := &recordingEmitter{}
	now := time.Unix(1_700_000_000, 0).UTC()
	d := NewDebouncer(t.Context(), window, rec)
	d.now = func() time.Time { return now }
	return d, rec, &now
}

func eventFor(userID, status string) PresenceEvent {
	return PresenceEvent{
		UserID: userID,
		Status: status,
	}
}

// waitForEmitCount blocks until rec has at least n events or the
// deadline expires, then returns the snapshot. The polling interval
// is short (1ms) because trailing timers in these tests fire on the
// order of single-digit milliseconds.
func waitForEmitCount(t *testing.T, rec *recordingEmitter, n int, timeout time.Duration) []PresenceEvent {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if snap := rec.snapshot(); len(snap) >= n {
			return snap
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("expected at least %d events; got %d", n, len(rec.snapshot()))
	return nil
}

// TestDebouncer_SingleEventEmitsImmediately verifies the leading-edge
// fast path: the first event for a user must be emitted on the
// caller's goroutine before Handle returns.
func TestDebouncer_SingleEventEmitsImmediately(t *testing.T) {
	d, rec, _ := newTestDebouncer(t, 50*time.Millisecond)
	defer d.Stop()

	d.Handle(eventFor("user-A", "online"))

	snap := rec.snapshot()
	require.Len(t, snap, 1)
	require.Equal(t, "user-A", snap[0].UserID)
	require.Equal(t, "online", snap[0].Status)
}

// TestDebouncer_BurstCollapses verifies the core suppression: 3
// events within the window collapse to 1 leading-edge emit + 1
// trailing emit carrying the LAST payload.
func TestDebouncer_BurstCollapses(t *testing.T) {
	window := 30 * time.Millisecond
	d, rec, now := newTestDebouncer(t, window)
	defer d.Stop()

	// 1st event — leading-edge emit.
	d.Handle(eventFor("user-A", "online"))
	// Advance the simulated clock just enough to be inside the
	// window. Subsequent events should be deferred.
	*now = now.Add(2 * time.Millisecond)
	d.Handle(eventFor("user-A", "idle"))
	*now = now.Add(2 * time.Millisecond)
	d.Handle(eventFor("user-A", "dnd"))

	// Trailing timer fires after `window` real-time. Wait up to 5x
	// the window to absorb scheduler jitter.
	snap := waitForEmitCount(t, rec, 2, 5*window)

	require.Len(t, snap, 2, "expected leading + trailing emit only")
	require.Equal(t, "online", snap[0].Status, "leading-edge emit carries first payload")
	require.Equal(t, "dnd", snap[1].Status, "trailing emit carries the LAST payload")
}

// TestDebouncer_AcrossWindowsPassThrough verifies that events spaced
// further apart than the window are NOT debounced — each becomes its
// own leading-edge emit.
func TestDebouncer_AcrossWindowsPassThrough(t *testing.T) {
	window := 5 * time.Millisecond
	d, rec, now := newTestDebouncer(t, window)
	defer d.Stop()

	d.Handle(eventFor("user-A", "online"))
	// Advance the simulated clock past the window. The next event
	// should also fire on the leading edge.
	*now = now.Add(window + time.Millisecond)
	d.Handle(eventFor("user-A", "offline"))

	// Both should be observable immediately (no trailing-timer wait).
	snap := rec.snapshot()
	require.Len(t, snap, 2)
	require.Equal(t, "online", snap[0].Status)
	require.Equal(t, "offline", snap[1].Status)
}

// TestDebouncer_StopDrainsWithoutPanic verifies graceful shutdown:
// pending trailing timers must be cancelled, Handle must become a
// no-op, and no events must be emitted after Stop returns.
func TestDebouncer_StopDrainsWithoutPanic(t *testing.T) {
	window := 50 * time.Millisecond
	d, rec, now := newTestDebouncer(t, window)

	// Set up a pending trailing emit.
	d.Handle(eventFor("user-A", "online"))
	*now = now.Add(2 * time.Millisecond)
	d.Handle(eventFor("user-A", "idle"))

	preStop := rec.snapshot()
	require.Len(t, preStop, 1, "expected only the leading emit before Stop")

	d.Stop()

	// Late events arriving after Stop must be dropped silently.
	d.Handle(eventFor("user-A", "dnd"))

	// Wait out the original window to confirm the trailing timer
	// does NOT fire.
	time.Sleep(2 * window)
	postStop := rec.snapshot()
	require.Len(t, postStop, 1, "Stop must cancel pending timers")

	// Stop again must be a no-op.
	d.Stop()
}

// mapSizes returns the current entry/lastEmit table cardinalities
// under the debouncer's lock. Used by the leak tests to assert the
// tables shrink back to empty rather than accumulating per-user state.
func (d *Debouncer) mapSizes() (entries, lastEmit int) {
	d.mu.Lock()
	defer d.mu.Unlock()
	return len(d.entries), len(d.lastEmit)
}

// TestDebouncer_FireDeletesEntry verifies that once a trailing emit
// fires, the per-user pendingEntry is removed from the table. Without
// this the entries map would retain a record for every user that ever
// experienced a burst.
func TestDebouncer_FireDeletesEntry(t *testing.T) {
	window := 20 * time.Millisecond
	d, rec, now := newTestDebouncer(t, window)
	defer d.Stop()

	// Leading emit, then a within-window event that schedules a trailing
	// timer (creating a pendingEntry).
	d.Handle(eventFor("user-A", "online"))
	*now = now.Add(2 * time.Millisecond)
	d.Handle(eventFor("user-A", "idle"))

	if entries, _ := d.mapSizes(); entries != 1 {
		t.Fatalf("expected one pending entry while the trailing timer is scheduled, got %d", entries)
	}

	// Let the trailing timer fire.
	waitForEmitCount(t, rec, 2, 5*window)

	// Poll until fire() has removed the entry (the delete happens under
	// the lock just before Emit).
	deadline := time.Now().Add(5 * window)
	for time.Now().Before(deadline) {
		if entries, _ := d.mapSizes(); entries == 0 {
			return
		}
		time.Sleep(time.Millisecond)
	}
	entries, _ := d.mapSizes()
	t.Fatalf("fire() must delete the settled entry; entries still has %d", entries)
}

// TestDebouncer_SweepReclaimsIdleUsers is the anti-leak regression: a
// large churning population of distinct users (each a single
// leading-edge emit) leaves one lastEmit record apiece. The periodic
// sweep must reclaim every record whose gating window has elapsed so
// the table returns to ~0 rather than growing without bound across
// reconnect snapshots.
func TestDebouncer_SweepReclaimsIdleUsers(t *testing.T) {
	window := 50 * time.Millisecond
	d, rec, now := newTestDebouncer(t, window)
	defer d.Stop()

	const userCount = 5000
	for i := 0; i < userCount; i++ {
		d.Handle(eventFor("user-"+strconv.Itoa(i), "online"))
	}

	// Every distinct user emitted on the leading edge, so the table
	// holds one lastEmit record each and zero pending entries.
	if got := len(rec.snapshot()); got != userCount {
		t.Fatalf("expected %d leading-edge emits, got %d", userCount, got)
	}
	if entries, lastEmit := d.mapSizes(); entries != 0 || lastEmit != userCount {
		t.Fatalf("pre-sweep table mismatch: entries=%d lastEmit=%d (want 0/%d)", entries, lastEmit, userCount)
	}

	// Advance the simulated clock past the window so every record is now
	// idle, then drive the sweep directly (deterministic — no reliance
	// on the background ticker cadence).
	*now = now.Add(2 * window)
	d.sweep()

	entries, lastEmit := d.mapSizes()
	if entries != 0 || lastEmit != 0 {
		t.Fatalf("sweep must reclaim all idle records: entries=%d lastEmit=%d (want 0/0)", entries, lastEmit)
	}
}

// TestDebouncer_SweepPreservesPendingEntries guards against the sweep
// evicting a user that still has a trailing emit scheduled: dropping
// the entry/lastEmit out from under a live timer would lose the settled
// payload. Pending users must survive a sweep and only be reclaimed
// after fire().
func TestDebouncer_SweepPreservesPendingEntries(t *testing.T) {
	window := 50 * time.Millisecond
	d, rec, now := newTestDebouncer(t, window)
	defer d.Stop()

	d.Handle(eventFor("user-A", "online"))
	*now = now.Add(2 * time.Millisecond)
	d.Handle(eventFor("user-A", "idle")) // schedules a trailing timer

	// Advance past the window and sweep. The pending entry must NOT be
	// reclaimed even though its lastEmit is older than the cutoff.
	*now = now.Add(2 * window)
	d.sweep()

	if entries, _ := d.mapSizes(); entries != 1 {
		t.Fatalf("sweep must preserve a user with a scheduled trailing emit, entries=%d", entries)
	}

	// The trailing emit must still fire with the last payload.
	snap := waitForEmitCount(t, rec, 2, 5*window)
	require.Equal(t, "idle", snap[1].Status, "trailing emit must survive the sweep")
}

// TestDebouncer_DistinctUsersDontCrossContaminate verifies the
// per-user keying: events for different snowflakes never throttle
// each other.
func TestDebouncer_DistinctUsersDontCrossContaminate(t *testing.T) {
	window := 50 * time.Millisecond
	d, rec, _ := newTestDebouncer(t, window)
	defer d.Stop()

	d.Handle(eventFor("user-A", "online"))
	d.Handle(eventFor("user-B", "online"))
	d.Handle(eventFor("user-C", "online"))

	snap := rec.snapshot()
	require.Len(t, snap, 3, "each user gets its own leading-edge emit")
}
