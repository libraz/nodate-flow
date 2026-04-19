package ratelimit

import (
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

func TestFormatRetryAfter(t *testing.T) {
	t.Parallel()
	if got := FormatRetryAfter(42); got != "42" {
		t.Fatalf("expected \"42\", got %q", got)
	}
}
