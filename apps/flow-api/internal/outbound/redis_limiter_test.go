// redis_limiter_test.go — unit tests for [RedisLimiter] that do not
// require a live Redis instance. Liveness with a real broker is
// covered by the smoke test in apps/flow-api/internal/stream.
package outbound

import (
	"context"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"
)

// TestRedisLimiterAllowReturnsPromptlyOnCancelledContext asserts that
// a cancelled caller ctx short-circuits [RedisLimiter.Allow] instead
// of waiting for the internal 500ms Redis timeout. This guards the
// regression where Allow used context.Background() and ignored the
// caller's deadline, so a request that the client had already given
// up on still consumed a Redis round trip.
func TestRedisLimiterAllowReturnsPromptlyOnCancelledContext(t *testing.T) {
	t.Parallel()
	// Point at a closed loopback port so any actual Redis dial
	// would block until the internal 500ms timeout fires. If the
	// cancellation short-circuit is wired correctly we never reach
	// the dial.
	rdb := redis.NewClient(&redis.Options{
		Addr:        "127.0.0.1:1",
		DialTimeout: 500 * time.Millisecond,
	})
	t.Cleanup(func() { _ = rdb.Close() })

	l := NewRedisLimiter(rdb, "test", 10, 5)

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // pre-cancelled: Allow must observe and bail out.

	start := time.Now()
	got := l.Allow(ctx)
	elapsed := time.Since(start)

	if got {
		t.Fatalf("Allow on cancelled ctx must return false (denied), got true")
	}
	// Generous bound: the cancellation check is synchronous, so the
	// real cost is just the call frame. If we are accidentally
	// waiting for the 500ms timeout the bound below will trip.
	if elapsed > 100*time.Millisecond {
		t.Fatalf("Allow on cancelled ctx must short-circuit, took %v", elapsed)
	}

	// The denial counter should advance so observability stays
	// honest about cancelled calls.
	if s := l.Stats(); s.Denied == 0 {
		t.Fatalf("expected denied counter > 0, got %+v", s)
	}
}

// TestRedisLimiterAllowDeadlineHonoured asserts that a caller ctx
// with a deadline shorter than the limiter's internal 500ms cap is
// honoured: when the dial would otherwise block 500ms, Allow returns
// at the caller's deadline instead. Uses a closed loopback port to
// guarantee the Redis round trip never succeeds.
func TestRedisLimiterAllowDeadlineHonoured(t *testing.T) {
	t.Parallel()
	rdb := redis.NewClient(&redis.Options{
		Addr:        "127.0.0.1:1",
		DialTimeout: 500 * time.Millisecond,
	})
	t.Cleanup(func() { _ = rdb.Close() })

	l := NewRedisLimiter(rdb, "test", 10, 5)

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	start := time.Now()
	// Result is irrelevant; we care about the wall-clock bound. A
	// dial failure makes the limiter fail-open (returns true), but
	// it must do so well before the internal 500ms ceiling.
	_ = l.Allow(ctx)
	elapsed := time.Since(start)

	if elapsed > 250*time.Millisecond {
		t.Fatalf("Allow should honour caller deadline, took %v", elapsed)
	}
}
