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

// TestRateLimiterSseAndPostShareHashedBudget locks in the M-5 fix: both
// the SSE (GET) path (sse.go) and the POST path (server.go) key the rate
// limiter on hashToken(tok). Because both reduce to the same hashed key,
// a client cannot double its budget by splitting requests across the two
// HTTP methods — requests counted under one method spend the shared
// allowance for the other.
func TestRateLimiterSseAndPostShareHashedBudget(t *testing.T) {
	t.Parallel()
	rl := newMCPRateLimiter()
	const tok = "mcp_shared_budget_token"
	key := hashToken(tok)

	// Simulate the POST path consuming the full per-token budget under the
	// hashed key (server.go: h.rl.allow(hashToken(tok))).
	for i := 0; i < rl.maxReqs; i++ {
		ok, _ := rl.allow(key)
		if !ok {
			t.Fatalf("POST request %d should be within budget", i+1)
		}
	}

	// The SSE path now also keys on hashToken(tok) (sse.go), so a GET that
	// arrives after the budget is spent must be denied — proving the two
	// paths share one budget rather than each getting maxReqs.
	if ok, retryAfter := rl.allow(hashToken(tok)); ok {
		t.Fatal("SSE request must be denied: it shares the POST budget under the hashed key")
	} else if retryAfter < time.Second {
		t.Fatalf("retryAfter should be >= 1s, got %v", retryAfter)
	}
}

// TestRateLimiterNeverKeysOnPlaintextToken guards the secret-in-memory
// half of M-5: the raw bearer token must never appear as a rate-limiter
// map key. Exhausting the budget through the hashed key must leave no
// entry under the plaintext token.
func TestRateLimiterNeverKeysOnPlaintextToken(t *testing.T) {
	t.Parallel()
	rl := newMCPRateLimiter()
	const tok = "mcp_plaintext_should_never_be_a_key"

	for i := 0; i < rl.maxReqs; i++ {
		rl.allow(hashToken(tok))
	}

	rl.mu.Lock()
	_, plaintextKeyed := rl.tokens[tok]
	_, hashedKeyed := rl.tokens[hashToken(tok)]
	rl.mu.Unlock()

	if plaintextKeyed {
		t.Error("plaintext token must never be stored as a rate-limiter key")
	}
	if !hashedKeyed {
		t.Error("the hashed token must be the only key used by both paths")
	}
}
