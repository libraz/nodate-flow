package ctxutil

import (
	"context"
	"testing"
	"time"
)

// TestCleanupSurvivesParentCancellation asserts the returned context
// keeps running after the parent is cancelled — the whole point of
// the helper is to outlive the request that triggered the cleanup.
func TestCleanupSurvivesParentCancellation(t *testing.T) {
	t.Parallel()
	parent, parentCancel := context.WithCancel(context.Background())
	ctx, cancel := Cleanup(parent, time.Second)
	defer cancel()

	parentCancel()

	if err := ctx.Err(); err != nil {
		t.Fatalf("cleanup ctx must survive parent cancellation, got err=%v", err)
	}
}

// TestCleanupHonoursDeadline asserts the bounded timeout fires so a
// cleanup cannot leak indefinitely.
func TestCleanupHonoursDeadline(t *testing.T) {
	t.Parallel()
	ctx, cancel := Cleanup(context.Background(), 20*time.Millisecond)
	defer cancel()

	select {
	case <-ctx.Done():
		// expected
	case <-time.After(200 * time.Millisecond):
		t.Fatal("cleanup ctx did not honour its deadline")
	}

	if err := ctx.Err(); err != context.DeadlineExceeded {
		t.Fatalf("expected DeadlineExceeded, got %v", err)
	}
}

// TestCleanupInheritsValues asserts parent values are visible from the
// cleanup ctx — this is what makes WithoutCancel preferable to a fresh
// Background-derived context.
func TestCleanupInheritsValues(t *testing.T) {
	t.Parallel()
	type ctxKey string
	const k ctxKey = "trace-id"
	parent := context.WithValue(context.Background(), k, "abc")
	ctx, cancel := Cleanup(parent, time.Second)
	defer cancel()

	if got := ctx.Value(k); got != "abc" {
		t.Fatalf("expected inherited value 'abc', got %v", got)
	}
}

// TestCleanupZeroDurationUsesDefault asserts a zero / negative
// timeout falls back to the documented default rather than producing
// an immediately-expired context.
func TestCleanupZeroDurationUsesDefault(t *testing.T) {
	t.Parallel()
	ctx, cancel := Cleanup(context.Background(), 0)
	defer cancel()

	dl, ok := ctx.Deadline()
	if !ok {
		t.Fatal("expected a deadline on zero-duration cleanup")
	}
	if d := time.Until(dl); d <= 0 || d > 10*time.Second {
		t.Fatalf("default deadline out of expected range, got %v remaining", d)
	}
}

// TestCleanupNilParent asserts the helper degrades to Background when
// the caller forgets to pass a parent ctx, instead of panicking.
func TestCleanupNilParent(t *testing.T) {
	t.Parallel()
	//nolint:staticcheck // SA1012: deliberately pass nil to verify the guard.
	ctx, cancel := Cleanup(nil, 10*time.Millisecond)
	defer cancel()

	if ctx == nil {
		t.Fatal("Cleanup must never return a nil context")
	}
}
