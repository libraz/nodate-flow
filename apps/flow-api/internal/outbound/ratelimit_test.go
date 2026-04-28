package outbound

import (
	"context"
	"testing"
	"time"
)

func TestLimiterAllowBurst(t *testing.T) {
	l := NewLimiter(1, 3)
	for i := 0; i < 3; i++ {
		if !l.Allow(context.Background()) {
			t.Fatalf("burst slot %d should be allowed", i)
		}
	}
	if l.Allow(context.Background()) {
		t.Fatal("4th call should be denied until refill")
	}
}

func TestLimiterRefill(t *testing.T) {
	base := time.Unix(1_700_000_000, 0)
	clock := base
	l := NewLimiter(10, 1)
	l.now = func() time.Time { return clock }
	l.last = clock
	l.tokens = 1

	if !l.Allow(context.Background()) {
		t.Fatal("first token should be available")
	}
	if l.Allow(context.Background()) {
		t.Fatal("second token should be gone")
	}
	clock = base.Add(250 * time.Millisecond) // +2.5 tokens at 10/sec
	if !l.Allow(context.Background()) {
		t.Fatal("after refill the limiter should allow again")
	}
}

func TestLimiterWaitCancellation(t *testing.T) {
	l := NewLimiter(0.1, 1) // very slow refill
	if !l.Allow(context.Background()) {
		t.Fatal("first token should be available")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	if err := l.Wait(ctx); err == nil {
		t.Fatal("expected cancellation error")
	}
}

func TestRegistryUnconfiguredIsNoop(t *testing.T) {
	r := NewRegistry()
	if err := r.Wait(context.Background(), "unknown"); err != nil {
		t.Fatalf("unconfigured destination should not error, got %v", err)
	}
}

func TestRegistryAppliesLimiter(t *testing.T) {
	r := NewRegistry()
	r.Set("github", NewLimiter(100, 1))
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	if err := r.Wait(ctx, "github"); err != nil {
		t.Fatalf("first call should succeed: %v", err)
	}
}

func TestLimiterStats(t *testing.T) {
	l := NewLimiter(1000, 2)
	if !l.Allow(context.Background()) {
		t.Fatal("first allow")
	}
	if !l.Allow(context.Background()) {
		t.Fatal("second allow")
	}
	// Drain - third should be denied (no time advance).
	if l.Allow(context.Background()) {
		t.Fatal("third should be denied without refill")
	}
	s := l.Stats()
	if s.Allowed != 2 || s.Denied != 1 {
		t.Fatalf("unexpected stats: %+v", s)
	}
	r := NewRegistry()
	r.Set("x", l)
	snap := r.Snapshot()
	if snap["x"].Allowed != 2 {
		t.Fatalf("snapshot mismatch: %+v", snap)
	}
}
