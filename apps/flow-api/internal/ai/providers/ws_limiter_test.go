package providers

import (
	"testing"
	"time"
)

// withWSLimiterStore swaps the package-wide store for a fresh one and
// restores it afterwards. The store is process-wide state, so a test
// asserting its size has to own it for the duration.
func withWSLimiterStore(t *testing.T, now func() time.Time) {
	t.Helper()
	prev := wsStore
	wsStore = &wsLimiterStore{
		limiters: make(map[uint32]*wsLimiter),
		rps:      10,
		burst:    5,
		now:      now,
	}
	t.Cleanup(func() { wsStore = prev })
}

// One bucket per workspace, kept for the life of the process, is a leak
// keyed by tenant: an api serving a long tail of workspaces accumulates
// one for every workspace it has ever answered and never gives any back.
func TestWorkspaceLimitersAreEvictedWhenIdle(t *testing.T) {
	clock := time.Unix(1_700_000_000, 0)
	withWSLimiterStore(t, func() time.Time { return clock })

	// Enough workspaces to trip one sweep, all at the same instant.
	for i := 1; i <= wsLimiterSweepEvery; i++ {
		//#nosec G115 -- loop bound is a small constant
		if getOrCreateWSLimiter(uint32(i)) == nil {
			t.Fatalf("workspace %d got no limiter", i)
		}
	}
	if got := wsLimiterCount(); got != wsLimiterSweepEvery {
		t.Fatalf("limiters after the first batch = %d, want %d", got, wsLimiterSweepEvery)
	}

	// One workspace stays active across the idle window; the rest go
	// quiet. The next sweep should keep only the active one plus the
	// workspaces created during this batch.
	clock = clock.Add(wsLimiterIdleTTL + time.Minute)
	getOrCreateWSLimiter(1)
	for i := wsLimiterSweepEvery + 1; i <= wsLimiterSweepEvery*2; i++ {
		//#nosec G115 -- loop bound is a small constant
		getOrCreateWSLimiter(uint32(i))
	}

	got := wsLimiterCount()
	if got > wsLimiterSweepEvery+1 {
		t.Fatalf("limiters after the idle window = %d; the quiet workspaces were not evicted", got)
	}
	if !wsLimiterHeld(1) {
		t.Error("the workspace that kept making calls lost its limiter")
	}
	if wsLimiterHeld(2) {
		t.Error("a workspace idle past the TTL kept its limiter")
	}
}

// A workspace that keeps calling must keep the same bucket: handing it a
// fresh one would hand it a fresh burst allowance too.
func TestActiveWorkspaceKeepsItsLimiter(t *testing.T) {
	clock := time.Unix(1_700_000_000, 0)
	withWSLimiterStore(t, func() time.Time { return clock })

	first := getOrCreateWSLimiter(42)
	clock = clock.Add(wsLimiterIdleTTL / 2)
	second := getOrCreateWSLimiter(42)
	if first != second {
		t.Fatal("an active workspace was given a new bucket, resetting its burst allowance")
	}
}

// wsLimiterCount reports how many workspaces hold a limiter.
func wsLimiterCount() int {
	wsStore.mu.RLock()
	defer wsStore.mu.RUnlock()
	return len(wsStore.limiters)
}

// wsLimiterHeld reports whether a workspace still holds a limiter.
func wsLimiterHeld(wsID uint32) bool {
	wsStore.mu.RLock()
	defer wsStore.mu.RUnlock()
	_, ok := wsStore.limiters[wsID]
	return ok
}
