package middleware

import (
	"net/http"
	"strconv"
	"sync"
	"time"

	"github.com/nodate-flow/nodate-flow/packages/go-shared/authn"
)

// RateLimitConfig configures the per-IP sliding window rate limiter.
type RateLimitConfig struct {
	// MaxRequests is the maximum number of requests allowed per window.
	MaxRequests int
	// Window is the sliding window duration.
	Window time.Duration
}

type ipEntry struct {
	timestamps []time.Time
}

// IPRateLimiter is a per-IP sliding window rate limiter middleware.
// It tracks request timestamps per IP and rejects requests that exceed
// the configured threshold with 429 Too Many Requests.
type IPRateLimiter struct {
	mu     sync.Mutex
	ips    map[string]*ipEntry
	config RateLimitConfig
	done   chan struct{}
}

// NewIPRateLimiter creates a new per-IP rate limiter. Call [IPRateLimiter.Stop]
// on shutdown to release the background cleanup goroutine.
func NewIPRateLimiter(cfg RateLimitConfig) *IPRateLimiter {
	rl := &IPRateLimiter{
		ips:    make(map[string]*ipEntry),
		config: cfg,
		done:   make(chan struct{}),
	}
	// Background cleanup of stale entries every 2x window.
	go rl.cleanup(cfg.Window * 2)
	return rl
}

// Stop releases the background cleanup goroutine. It is safe to call
// multiple times but only the first call has an effect.
func (rl *IPRateLimiter) Stop() {
	select {
	case <-rl.done:
		// already closed
	default:
		close(rl.done)
	}
}

// Middleware returns a chi-compatible middleware that enforces the rate
// limit. It reads the client IP from [ClientIPFromContext] (populated
// by the [ClientIP] middleware earlier in the chain).
func (rl *IPRateLimiter) Middleware() func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			ip := authn.ClientIPFromContext(r.Context())
			if ip == "" {
				next.ServeHTTP(w, r)
				return
			}

			now := time.Now()
			rl.mu.Lock()
			entry, ok := rl.ips[ip]
			if !ok {
				entry = &ipEntry{}
				rl.ips[ip] = entry
			}
			// Evict timestamps outside the window.
			cutoff := now.Add(-rl.config.Window)
			n := 0
			for _, ts := range entry.timestamps {
				if ts.After(cutoff) {
					entry.timestamps[n] = ts
					n++
				}
			}
			entry.timestamps = entry.timestamps[:n]

			if len(entry.timestamps) >= rl.config.MaxRequests {
				// Calculate Retry-After from the oldest timestamp in the window.
				retryAfter := entry.timestamps[0].Add(rl.config.Window).Sub(now)
				if retryAfter < time.Second {
					retryAfter = time.Second
				}
				rl.mu.Unlock()
				w.Header().Set("Retry-After", strconv.Itoa(int(retryAfter.Seconds())))
				http.Error(w, "429 Too Many Requests", http.StatusTooManyRequests)
				return
			}

			entry.timestamps = append(entry.timestamps, now)
			rl.mu.Unlock()

			next.ServeHTTP(w, r)
		})
	}
}

func (rl *IPRateLimiter) cleanup(interval time.Duration) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			now := time.Now()
			rl.mu.Lock()
			for ip, entry := range rl.ips {
				cutoff := now.Add(-rl.config.Window)
				n := 0
				for _, ts := range entry.timestamps {
					if ts.After(cutoff) {
						entry.timestamps[n] = ts
						n++
					}
				}
				entry.timestamps = entry.timestamps[:n]
				if len(entry.timestamps) == 0 {
					delete(rl.ips, ip)
				}
			}
			rl.mu.Unlock()
		case <-rl.done:
			return
		}
	}
}
