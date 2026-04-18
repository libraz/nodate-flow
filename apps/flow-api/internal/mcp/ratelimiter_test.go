package mcp

import (
	"testing"
	"time"
)

func TestRateLimiterAllowsWithinLimit(t *testing.T) {
	t.Parallel()
	rl := newMCPRateLimiter()
	for i := 0; i < rl.maxReqs; i++ {
		ok, _ := rl.allow("tok1")
		if !ok {
			t.Fatalf("request %d should be allowed", i+1)
		}
	}
}

func TestRateLimiterDeniesOverLimit(t *testing.T) {
	t.Parallel()
	rl := newMCPRateLimiter()
	for i := 0; i < rl.maxReqs; i++ {
		rl.allow("tok2")
	}
	ok, retryAfter := rl.allow("tok2")
	if ok {
		t.Fatal("request over limit should be denied")
	}
	if retryAfter < time.Second {
		t.Fatalf("retryAfter should be >= 1s, got %v", retryAfter)
	}
}

func TestRateLimiterEvictsStaleTokens(t *testing.T) {
	t.Parallel()
	rl := mcpRateLimiter{
		tokens:  make(map[string]*tokenBucket),
		maxReqs: 60,
		window:  100 * time.Millisecond,
	}

	// Add a request for token "stale".
	rl.allow("stale")
	if len(rl.tokens) != 1 {
		t.Fatalf("expected 1 token, got %d", len(rl.tokens))
	}

	// Wait for the window to expire.
	time.Sleep(250 * time.Millisecond)

	// Force eviction by making a request for a different token after
	// the eviction interval (2x window = 200ms) has passed.
	rl.allow("fresh")

	// The "stale" token should have been evicted.
	rl.mu.Lock()
	_, staleExists := rl.tokens["stale"]
	rl.mu.Unlock()
	if staleExists {
		t.Error("stale token should have been evicted")
	}
}

func TestRateLimiterIsolatesTokens(t *testing.T) {
	t.Parallel()
	rl := newMCPRateLimiter()
	// Exhaust limit for tok-a.
	for i := 0; i < rl.maxReqs; i++ {
		rl.allow("tok-a")
	}
	// tok-b should still be allowed.
	ok, _ := rl.allow("tok-b")
	if !ok {
		t.Fatal("different token should have its own limit")
	}
}
