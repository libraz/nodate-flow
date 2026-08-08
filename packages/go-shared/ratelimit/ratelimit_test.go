package ratelimit

import (
	"fmt"
	"testing"
	"time"
)

func TestLimiter_AllowsUnderLimit(t *testing.T) {
	t.Parallel()
	l := New(Config{MaxRequests: 5, Window: time.Minute})
	t.Cleanup(l.Stop)

	for i := 0; i < 5; i++ {
		r := l.Allow("10.0.0.1")
		if !r.Allowed {
			t.Fatalf("request %d should be allowed", i+1)
		}
		if r.Limit != 5 {
			t.Fatalf("expected Limit=5, got %d", r.Limit)
		}
		if r.Remaining != 5-(i+1) {
			t.Fatalf("expected Remaining=%d, got %d", 5-(i+1), r.Remaining)
		}
	}
}

func TestLimiter_BlocksOverLimit(t *testing.T) {
	t.Parallel()
	l := New(Config{MaxRequests: 3, Window: time.Minute})
	t.Cleanup(l.Stop)

	for i := 0; i < 3; i++ {
		r := l.Allow("10.0.0.1")
		if !r.Allowed {
			t.Fatalf("request %d should be allowed", i+1)
		}
	}

	r := l.Allow("10.0.0.1")
	if r.Allowed {
		t.Fatal("4th request should be denied")
	}
	if r.Remaining != 0 {
		t.Fatalf("expected Remaining=0, got %d", r.Remaining)
	}
	if r.RetryAfter < 1 {
		t.Fatalf("expected RetryAfter >= 1, got %d", r.RetryAfter)
	}
	if r.ResetUnix == 0 {
		t.Fatal("expected non-zero ResetUnix")
	}
}

func TestLimiter_DifferentKeysAreIndependent(t *testing.T) {
	t.Parallel()
	l := New(Config{MaxRequests: 1, Window: time.Minute})
	t.Cleanup(l.Stop)

	r1 := l.Allow("a")
	if !r1.Allowed {
		t.Fatal("first key should be allowed")
	}
	r2 := l.Allow("b")
	if !r2.Allowed {
		t.Fatal("second key should be allowed (separate bucket)")
	}
	r3 := l.Allow("a")
	if r3.Allowed {
		t.Fatal("first key should now be blocked")
	}
}

func TestLimiter_Stop_MultipleCallsSafe(t *testing.T) {
	t.Parallel()
	l := New(Config{MaxRequests: 10, Window: time.Second})
	l.Stop()
	l.Stop()
	l.Stop()
}

// A key that has gone quiet for longer than the window must stop
// costing memory: without eviction the map is a monotonically growing
// record of every IP the process has ever answered.
func TestLimiter_SweepEvictsIdleBuckets(t *testing.T) {
	t.Parallel()
	l := New(Config{MaxRequests: 10, Window: time.Minute})
	t.Cleanup(l.Stop)

	for i := range 500 {
		l.Allow(fmt.Sprintf("idle-%d", i))
	}
	if got := l.size(); got != 500 {
		t.Fatalf("buckets before sweep = %d, want 500", got)
	}

	// A sweep run one window later than every recorded request: all of
	// them have fallen out of the window.
	l.sweep(time.Now().Add(2 * time.Minute))
	if got := l.size(); got != 0 {
		t.Fatalf("buckets after sweep = %d, want 0", got)
	}

	// A key seen inside the window survives the same sweep, so eviction
	// is not simply "drop everything".
	l.Allow("recent")
	l.sweep(time.Now())
	if got := l.size(); got != 1 {
		t.Fatalf("buckets after sweeping a live key = %d, want 1", got)
	}
}

// The sweep touches every bucket it holds a lock over. What must not
// happen is that holding one lock stops every request in the process:
// the walk is longest exactly when the keyspace is widest, which is the
// middle of a burst.
//
// The test takes a shard lock the way sweep does and then serves a key
// that belongs to a different shard. Under one global mutex there is no
// such key, and this test says so rather than hanging.
func TestLimiter_SweepDoesNotStopUnrelatedKeys(t *testing.T) {
	t.Parallel()
	l := New(Config{MaxRequests: 10, Window: time.Minute})
	t.Cleanup(l.Stop)

	swept := &l.shards[0]
	var elsewhere string
	for i := range 1000 {
		key := fmt.Sprintf("key-%d", i)
		if l.shardFor(key) != swept {
			elsewhere = key
			break
		}
	}
	if elsewhere == "" {
		t.Fatal("every key landed on one lock: a sweep would block the whole process")
	}

	swept.mu.Lock()
	served := make(chan Result, 1)
	go func() { served <- l.Allow(elsewhere) }()
	select {
	case r := <-served:
		if !r.Allowed {
			t.Errorf("first request for %q was denied", elsewhere)
		}
	case <-time.After(5 * time.Second):
		swept.mu.Unlock()
		t.Fatalf("Allow(%q) waited on a lock held for a different part of the keyspace", elsewhere)
	}
	swept.mu.Unlock()
}

// BenchmarkAllowDuringSweep measures the cost of one Allow while a
// sweep of a wide keyspace runs continuously in the background — the
// state a burst across many client IPs puts the limiter in.
func BenchmarkAllowDuringSweep(b *testing.B) {
	l := New(Config{MaxRequests: 1_000_000, Window: time.Minute})
	b.Cleanup(l.Stop)
	for i := range 100_000 {
		l.Allow(fmt.Sprintf("bench-%d", i))
	}

	stop := make(chan struct{})
	done := make(chan struct{})
	go func() {
		defer close(done)
		for {
			select {
			case <-stop:
				return
			default:
				// Sweep with a cutoff that keeps every bucket, so the
				// walk stays the full width of the keyspace.
				l.sweep(time.Now())
			}
		}
	}()

	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		i := 0
		for pb.Next() {
			l.Allow(fmt.Sprintf("live-%d", i%64))
			i++
		}
	})
	b.StopTimer()
	close(stop)
	<-done
}

func TestFormatRetryAfter(t *testing.T) {
	t.Parallel()
	if got := FormatRetryAfter(42); got != "42" {
		t.Fatalf("expected \"42\", got %q", got)
	}
}
