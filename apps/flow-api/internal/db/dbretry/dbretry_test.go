package dbretry

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/go-sql-driver/mysql"
)

// TestIsTransient checks that the helper recognises the two MySQL
// error numbers it should retry and rejects everything else.
func TestIsTransient(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		err  error
		want bool
	}{
		{"nil", nil, false},
		{"plain", errors.New("boom"), false},
		{"deadlock", &mysql.MySQLError{Number: 1213, Message: "Deadlock"}, true},
		{"lock_wait", &mysql.MySQLError{Number: 1205, Message: "Lock wait"}, true},
		{"duplicate", &mysql.MySQLError{Number: 1062, Message: "Duplicate"}, false},
		{"wrapped_deadlock", wrap(&mysql.MySQLError{Number: 1213, Message: "Deadlock"}), true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := IsTransient(tc.err); got != tc.want {
				t.Fatalf("IsTransient(%v) = %v, want %v", tc.err, got, tc.want)
			}
		})
	}
}

// TestDoRetriesDeadlock verifies that Do re-invokes fn after a
// transient error and returns nil once fn succeeds.
func TestDoRetriesDeadlock(t *testing.T) {
	t.Parallel()
	calls := 0
	err := Do(context.Background(), "test.deadlock", func(_ context.Context) error {
		calls++
		if calls < 2 {
			return &mysql.MySQLError{Number: 1213, Message: "Deadlock"}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("Do returned %v, want nil", err)
	}
	if calls != 2 {
		t.Fatalf("calls = %d, want 2", calls)
	}
}

// TestDoStopsAtMaxAttempts verifies the bound on retries.
func TestDoStopsAtMaxAttempts(t *testing.T) {
	t.Parallel()
	calls := 0
	dl := &mysql.MySQLError{Number: 1213, Message: "Deadlock"}
	err := Do(context.Background(), "test.exhaust", func(_ context.Context) error {
		calls++
		return dl
	})
	if !errors.Is(err, dl) {
		t.Fatalf("Do returned %v, want last deadlock error", err)
	}
	if calls != MaxAttempts {
		t.Fatalf("calls = %d, want %d", calls, MaxAttempts)
	}
}

// TestDoReturnsNonTransientImmediately verifies that non-1213/1205
// errors short-circuit the retry loop.
func TestDoReturnsNonTransientImmediately(t *testing.T) {
	t.Parallel()
	calls := 0
	want := errors.New("permanent")
	err := Do(context.Background(), "test.permanent", func(_ context.Context) error {
		calls++
		return want
	})
	if !errors.Is(err, want) {
		t.Fatalf("Do returned %v, want %v", err, want)
	}
	if calls != 1 {
		t.Fatalf("calls = %d, want 1", calls)
	}
}

// TestDoHonoursContextCancel verifies the loop bails out promptly when
// the context is cancelled between attempts.
func TestDoHonoursContextCancel(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithCancel(context.Background())
	calls := 0
	dl := &mysql.MySQLError{Number: 1213, Message: "Deadlock"}
	go func() {
		time.Sleep(2 * time.Millisecond)
		cancel()
	}()
	err := Do(ctx, "test.ctx", func(_ context.Context) error {
		calls++
		return dl
	})
	if !errors.Is(err, dl) {
		t.Fatalf("Do returned %v, want deadlock error", err)
	}
	if calls < 1 || calls > MaxAttempts {
		t.Fatalf("calls = %d, want 1..%d", calls, MaxAttempts)
	}
}

// TestAddCommitHookFiresImmediatelyWithoutCollector verifies that on a
// context with no commit-hook collector (the auto-commit path, or a
// caller not using InTx) the callback runs synchronously, preserving
// the historical fire-on-append behavior.
func TestAddCommitHookFiresImmediatelyWithoutCollector(t *testing.T) {
	t.Parallel()
	fired := 0
	AddCommitHook(context.Background(), func() { fired++ })
	if fired != 1 {
		t.Fatalf("fired = %d, want 1 (immediate)", fired)
	}
}

// TestCommitHooksDeferUntilRun verifies that a callback registered on a
// context carrying a collector does not fire until runCommitHooks is
// invoked — the post-commit trigger InTx uses.
func TestCommitHooksDeferUntilRun(t *testing.T) {
	t.Parallel()
	ctx := WithCommitHooks(context.Background())
	fired := 0
	AddCommitHook(ctx, func() { fired++ })
	AddCommitHook(ctx, func() { fired++ })
	if fired != 0 {
		t.Fatalf("fired = %d before run, want 0 (deferred)", fired)
	}
	runCommitHooks(ctx)
	if fired != 2 {
		t.Fatalf("fired = %d after run, want 2", fired)
	}
	// A second run must not re-fire the drained callbacks.
	runCommitHooks(ctx)
	if fired != 2 {
		t.Fatalf("fired = %d after second run, want 2 (drained once)", fired)
	}
}

// TestCommitHooksDroppedWhenNeverRun verifies that hooks registered on
// an attempt's collector simply never fire when runCommitHooks is not
// called (the rollback / failed-commit path), so an aborted transaction
// leaks no side effects.
func TestCommitHooksDroppedWhenNeverRun(t *testing.T) {
	t.Parallel()
	ctx := WithCommitHooks(context.Background())
	fired := 0
	AddCommitHook(ctx, func() { fired++ })
	// Simulate a rolled-back attempt: the collector is discarded without
	// runCommitHooks ever being called.
	if fired != 0 {
		t.Fatalf("fired = %d, want 0 (dropped)", fired)
	}
}

// wrap reproduces fmt.Errorf("...: %w", err) without pulling fmt into
// the test file imports.
type wrappedErr struct{ inner error }

func (w *wrappedErr) Error() string { return "wrapped: " + w.inner.Error() }
func (w *wrappedErr) Unwrap() error { return w.inner }

func wrap(err error) error { return &wrappedErr{inner: err} }
