// Package ratelimit provides an in-memory sliding window rate limiter.
// It is transport-agnostic: callers supply a string key (IP, user id,
// token hash, etc.) and get back an Allow/Deny decision with metadata
// suitable for populating X-RateLimit-* and Retry-After headers.
//
// The implementation is intentionally simple: a per-key slice of
// timestamps protected by a single mutex. A background goroutine
// periodically evicts stale entries to bound memory. Call [Limiter.Stop]
// on shutdown to release it.
package ratelimit

import (
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

// Limiter is a sliding window rate limiter keyed by an opaque string.
// Safe for concurrent use.
type Limiter struct {
	mu        sync.Mutex
	buckets   map[string]*entry
	config    Config
	done      chan struct{}
	closeOnce sync.Once
}

// New creates a [Limiter] with the given configuration and starts a
// background goroutine that evicts stale buckets every 2x the window
// duration. Call [Limiter.Stop] on shutdown to release it.
func New(cfg Config) *Limiter {
	l := &Limiter{
		buckets: make(map[string]*entry),
		config:  cfg,
		done:    make(chan struct{}),
	}
	go l.cleanup(cfg.Window * 2)
	return l
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
	l.mu.Lock()
	defer l.mu.Unlock()

	b, ok := l.buckets[key]
	if !ok {
		b = &entry{}
		l.buckets[key] = b
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
			now := time.Now()
			l.mu.Lock()
			cutoff := now.Add(-l.config.Window)
			for k, b := range l.buckets {
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
					delete(l.buckets, k)
				}
			}
			l.mu.Unlock()
		case <-l.done:
			return
		}
	}
}

// FormatRetryAfter formats a [Result.RetryAfter] value as a string
// suitable for the HTTP Retry-After header.
func FormatRetryAfter(seconds int) string {
	return strconv.Itoa(seconds)
}
