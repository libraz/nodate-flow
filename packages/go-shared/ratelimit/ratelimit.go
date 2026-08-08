// Package ratelimit provides an in-memory sliding window rate limiter.
// It is transport-agnostic: callers supply a string key (IP, user id,
// token hash, etc.) and get back an Allow/Deny decision with metadata
// suitable for populating X-RateLimit-* and Retry-After headers.
//
// Each key owns a slice of timestamps; keys are distributed over a
// fixed number of independently locked shards. A background goroutine
// periodically evicts stale entries to bound memory, one shard at a
// time. Call [Limiter.Stop] on shutdown to release it.
package ratelimit

import (
	"hash/maphash"
	"strconv"
	"sync"
	"time"
)

// Config configures a [Limiter].
type Config struct {
	// MaxRequests is the maximum number of requests allowed per Window.
	MaxRequests int
	// Window is the sliding window duration (e.g. 1 * time.Minute).
	Window time.Duration
}

// Result is returned by [Limiter.Allow]. It carries the information
// needed by the caller to populate standard rate-limit response headers.
type Result struct {
	// Allowed is true when the request is within the rate limit.
	Allowed bool
	// Limit is the configured maximum for the window (X-RateLimit-Limit).
	Limit int
	// Remaining is how many requests remain in the current window
	// (X-RateLimit-Remaining). Zero when Allowed is false.
	Remaining int
	// ResetUnix is the Unix timestamp (seconds) at which the oldest
	// request in the current window expires, opening one new slot
	// (X-RateLimit-Reset).
	ResetUnix int64
	// RetryAfter is the number of seconds the caller should wait before
	// retrying (Retry-After header). Zero when Allowed is true.
	RetryAfter int
}

// entry is a per-key bucket of request timestamps.
type entry struct {
	timestamps []time.Time
}

// shardCount is how many independently locked maps the keyspace is
// split across. The sweep walks one shard at a time, so it is also the
// factor by which a sweep's lock hold is shorter than the whole
// keyspace — and the number of concurrent Allow calls that can proceed
// while one shard is being swept.
//
// Sixteen is chosen for the shape of the traffic this limiter guards:
// the unauthenticated login / invite / public-share routes, where the
// key is a client IP and the interesting moment is a burst across many
// distinct IPs at once.
const shardCount = 16

// shard is one independently locked slice of the keyspace.
type shard struct {
	mu      sync.Mutex
	buckets map[string]*entry
}

// Limiter is a sliding window rate limiter keyed by an opaque string.
// Safe for concurrent use.
//
// Keys are spread over [shardCount] shards, each with its own mutex.
// A single global mutex would have made the periodic sweep — which
// touches every bucket — block every request for as long as the walk
// took, and the walk is longest exactly when the keyspace is widest,
// which is the middle of a burst.
type Limiter struct {
	shards    [shardCount]shard
	seed      maphash.Seed
	config    Config
	done      chan struct{}
	closeOnce sync.Once
}

// New creates a [Limiter] with the given configuration and starts a
// background goroutine that evicts stale buckets every 2x the window
// duration. Call [Limiter.Stop] on shutdown to release it.
//
// When cfg.Window is zero the cleanup goroutine is skipped. This mode is
// intended for codegen utilities (dump-openapi and similar) that construct
// the router only to inspect its shape and never serve traffic. Production
// callers must supply a non-zero Window.
func New(cfg Config) *Limiter {
	l := &Limiter{
		seed:   maphash.MakeSeed(),
		config: cfg,
		done:   make(chan struct{}),
	}
	for i := range l.shards {
		l.shards[i].buckets = make(map[string]*entry)
	}
	if cfg.Window > 0 {
		go l.cleanup(cfg.Window * 2)
	}
	return l
}

// shardFor returns the shard owning key. The seed is per-Limiter so the
// distribution cannot be predicted from outside the process and a caller
// cannot deliberately pile every key onto one shard.
func (l *Limiter) shardFor(key string) *shard {
	return &l.shards[maphash.String(l.seed, key)%shardCount]
}

// Stop releases the background cleanup goroutine. It is safe to call
// multiple times; only the first call has an effect.
func (l *Limiter) Stop() {
	l.closeOnce.Do(func() { close(l.done) })
}

// Allow records a request for the given key and returns a [Result]
// indicating whether the request is within the rate limit.
func (l *Limiter) Allow(key string) Result {
	now := time.Now()
	sh := l.shardFor(key)
	sh.mu.Lock()
	defer sh.mu.Unlock()

	b, ok := sh.buckets[key]
	if !ok {
		b = &entry{}
		sh.buckets[key] = b
	}

	// Evict timestamps outside the window.
	cutoff := now.Add(-l.config.Window)
	n := 0
	for _, ts := range b.timestamps {
		if ts.After(cutoff) {
			b.timestamps[n] = ts
			n++
		}
	}
	b.timestamps = b.timestamps[:n]

	resetUnix := now.Add(l.config.Window).Unix()
	if len(b.timestamps) > 0 {
		resetUnix = b.timestamps[0].Add(l.config.Window).Unix()
	}

	if len(b.timestamps) >= l.config.MaxRequests {
		retryAfter := b.timestamps[0].Add(l.config.Window).Sub(now)
		if retryAfter < time.Second {
			retryAfter = time.Second
		}
		return Result{
			Allowed:    false,
			Limit:      l.config.MaxRequests,
			Remaining:  0,
			ResetUnix:  resetUnix,
			RetryAfter: int(retryAfter.Seconds()),
		}
	}

	b.timestamps = append(b.timestamps, now)
	return Result{
		Allowed:   true,
		Limit:     l.config.MaxRequests,
		Remaining: l.config.MaxRequests - len(b.timestamps),
		ResetUnix: resetUnix,
	}
}

// cleanup periodically removes buckets whose newest timestamp is older
// than the window, bounding memory to O(active keys).
func (l *Limiter) cleanup(interval time.Duration) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			l.sweep(time.Now())
		case <-l.done:
			return
		}
	}
}

// sweep drops every bucket that holds nothing inside the window as of
// now, taking one shard lock at a time and releasing it before moving
// on. Requests keyed into the other shards are served throughout, so
// the sweep is never a stop-the-world pause proportional to the number
// of active keys.
func (l *Limiter) sweep(now time.Time) {
	cutoff := now.Add(-l.config.Window)
	for i := range l.shards {
		sh := &l.shards[i]
		sh.mu.Lock()
		for k, b := range sh.buckets {
			// Trim expired timestamps.
			n := 0
			for _, ts := range b.timestamps {
				if ts.After(cutoff) {
					b.timestamps[n] = ts
					n++
				}
			}
			b.timestamps = b.timestamps[:n]
			if n == 0 {
				delete(sh.buckets, k)
			}
		}
		sh.mu.Unlock()
	}
}

// size reports how many buckets the limiter is holding. Used by the
// package's own tests to observe eviction.
func (l *Limiter) size() int {
	total := 0
	for i := range l.shards {
		sh := &l.shards[i]
		sh.mu.Lock()
		total += len(sh.buckets)
		sh.mu.Unlock()
	}
	return total
}

// FormatRetryAfter formats a [Result.RetryAfter] value as a string
// suitable for the HTTP Retry-After header.
func FormatRetryAfter(seconds int) string {
	return strconv.Itoa(seconds)
}
