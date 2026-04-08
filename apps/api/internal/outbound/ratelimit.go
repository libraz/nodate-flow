// Package outbound provides primitives for making egress calls to
// external services (GitHub / Slack / Google / LLM providers) with
// uniform rate limiting, so a buggy agent can't flood a third party
// (4.SEC-2 outbound rate limit).
package outbound

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"time"
)

// ErrLimitExceeded is returned by [Limiter.Wait] when the context
// deadline fires before a token becomes available.
var ErrLimitExceeded = errors.New("outbound: rate limit exceeded")

// RateLimiter is the driver interface satisfied by [Limiter] (the
// in-process token bucket) and by the `redis` build-tagged
// RedisLimiter. Call sites should take this interface so a Redis
// backend can replace the default without code changes.
type RateLimiter interface {
	Allow() bool
	Wait(ctx context.Context) error
	Stats() LimiterStats
}

// Limiter is a per-destination token-bucket limiter. It is safe for
// concurrent use. The zero value is not usable; construct with
// [NewLimiter].
type Limiter struct {
	mu       sync.Mutex
	capacity float64
	refill   float64 // tokens per second
	tokens   float64
	last     time.Time
	now      func() time.Time

	// Observability counters (4.AGENT-2). Atomic so Stats() is lock
	// free.
	allowed atomic.Uint64
	waited  atomic.Uint64
	denied  atomic.Uint64
}

// LimiterStats is a point-in-time snapshot of a limiter's counters.
// Meant for /metrics or debug endpoints.
type LimiterStats struct {
	Allowed uint64
	Waited  uint64
	Denied  uint64
}

// Stats returns a snapshot of the counters.
func (l *Limiter) Stats() LimiterStats {
	return LimiterStats{
		Allowed: l.allowed.Load(),
		Waited:  l.waited.Load(),
		Denied:  l.denied.Load(),
	}
}

// NewLimiter constructs a token bucket with the given steady-state
// rate (tokens per second) and burst capacity. Both must be > 0.
func NewLimiter(ratePerSec float64, burst int) *Limiter {
	if ratePerSec <= 0 {
		ratePerSec = 1
	}
	if burst <= 0 {
		burst = 1
	}
	return &Limiter{
		capacity: float64(burst),
		refill:   ratePerSec,
		tokens:   float64(burst),
		last:     time.Now(),
		now:      time.Now,
	}
}

// Allow attempts to consume one token immediately. Returns true on
// success. It does not block.
func (l *Limiter) Allow() bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.advance()
	if l.tokens >= 1 {
		l.tokens--
		l.allowed.Add(1)
		return true
	}
	l.denied.Add(1)
	return false
}

// Wait blocks until a token can be consumed or ctx is cancelled.
// Returns [ErrLimitExceeded] if the context deadline fires while
// waiting for a slot.
func (l *Limiter) Wait(ctx context.Context) error {
	waited := false
	for {
		l.mu.Lock()
		l.advance()
		if l.tokens >= 1 {
			l.tokens--
			l.mu.Unlock()
			if waited {
				l.waited.Add(1)
			} else {
				l.allowed.Add(1)
			}
			return nil
		}
		waited = true
		needed := 1 - l.tokens
		wait := time.Duration(needed / l.refill * float64(time.Second))
		l.mu.Unlock()

		if wait <= 0 {
			wait = time.Millisecond
		}
		t := time.NewTimer(wait)
		select {
		case <-ctx.Done():
			t.Stop()
			l.denied.Add(1)
			return errors.Join(ErrLimitExceeded, ctx.Err())
		case <-t.C:
		}
	}
}

// advance credits the bucket for time elapsed since the last call.
// Caller must hold l.mu.
func (l *Limiter) advance() {
	n := l.now()
	elapsed := n.Sub(l.last).Seconds()
	if elapsed < 0 {
		elapsed = 0
	}
	l.tokens += elapsed * l.refill
	if l.tokens > l.capacity {
		l.tokens = l.capacity
	}
	l.last = n
}

// Registry stores per-destination limiters keyed by an opaque string
// (e.g. "github", "slack", "openai"). Each destination can be
// configured independently.
type Registry struct {
	mu       sync.RWMutex
	limiters map[string]RateLimiter
}

// NewRegistry returns an empty destination → limiter registry.
func NewRegistry() *Registry {
	return &Registry{limiters: make(map[string]RateLimiter)}
}

// Set registers or replaces the limiter for destination.
func (r *Registry) Set(destination string, l RateLimiter) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.limiters[destination] = l
}

// Snapshot returns a copy of every destination's current counters.
// Empty when nothing has been registered. Use this to feed /metrics
// scrapers or debug endpoints.
func (r *Registry) Snapshot() map[string]LimiterStats {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make(map[string]LimiterStats, len(r.limiters))
	for k, l := range r.limiters {
		out[k] = l.Stats()
	}
	return out
}

// Wait consumes one token from the destination's limiter, blocking up
// to ctx's deadline. If no limiter is configured for destination the
// call is a no-op (fail open on unconfigured, since the caller has
// already decided the destination is trustworthy enough to be listed).
func (r *Registry) Wait(ctx context.Context, destination string) error {
	r.mu.RLock()
	l := r.limiters[destination]
	r.mu.RUnlock()
	if l == nil {
		return nil
	}
	return l.Wait(ctx)
}
