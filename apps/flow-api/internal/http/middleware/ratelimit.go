package middleware

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/nodate-flow/nodate-flow/packages/go-shared/authn"
	"github.com/nodate-flow/nodate-flow/packages/go-shared/ratelimit"
)

// RateLimitConfig configures the per-key sliding window rate limiter.
type RateLimitConfig struct {
	// MaxRequests is the maximum number of requests allowed per window.
	MaxRequests int
	// Window is the sliding window duration.
	Window time.Duration
}

// IPRateLimiter is a per-IP sliding window rate limiter middleware.
// It tracks request timestamps per IP and rejects requests that exceed
// the configured threshold with 429 Too Many Requests. It delegates to
// the shared [ratelimit.Limiter] for the core algorithm.
type IPRateLimiter struct {
	limiter *ratelimit.Limiter
}

// NewIPRateLimiter creates a new per-IP rate limiter. Call [IPRateLimiter.Stop]
// on shutdown to release the background cleanup goroutine.
func NewIPRateLimiter(cfg RateLimitConfig) *IPRateLimiter {
	return &IPRateLimiter{
		limiter: ratelimit.New(ratelimit.Config{
			MaxRequests: cfg.MaxRequests,
			Window:      cfg.Window,
		}),
	}
}

// Stop releases the background cleanup goroutine. It is safe to call
// multiple times but only the first call has an effect.
func (rl *IPRateLimiter) Stop() {
	rl.limiter.Stop()
}

// Middleware returns a chi-compatible middleware that enforces the rate
// limit per client IP. It reads the client IP from [ClientIPFromContext]
// (populated by the [ClientIP] middleware earlier in the chain).
// Responses include X-RateLimit-Limit, X-RateLimit-Remaining, and
// X-RateLimit-Reset headers. Rejected requests include a Retry-After
// header.
func (rl *IPRateLimiter) Middleware() func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			ip := ClientIPFromContext(r.Context())
			if ip == "" {
				next.ServeHTTP(w, r)
				return
			}

			res := rl.limiter.Allow(ip)
			setRateLimitHeaders(w, res)

			if !res.Allowed {
				w.Header().Set("Retry-After", ratelimit.FormatRetryAfter(res.RetryAfter))
				writeJSONError(w, http.StatusTooManyRequests, "RATE.LIMIT_EXCEEDED", "429 Too Many Requests")
				return
			}

			next.ServeHTTP(w, r)
		})
	}
}

// APIRateLimiter is a dual-bucket rate limiter that applies different
// limits for authenticated vs unauthenticated requests. Authenticated
// requests are keyed by user id; unauthenticated requests are keyed by
// client IP.
type APIRateLimiter struct {
	authed   *ratelimit.Limiter
	unauthed *ratelimit.Limiter
}

// APIRateLimitConfig configures the [APIRateLimiter].
type APIRateLimitConfig struct {
	// AuthedMaxRequests is the maximum requests per window for
	// authenticated users (keyed by user id). Default: 100.
	AuthedMaxRequests int
	// UnauthedMaxRequests is the maximum requests per window for
	// unauthenticated callers (keyed by IP). Default: 20.
	UnauthedMaxRequests int
	// Window is the sliding window duration. Default: 1 minute.
	Window time.Duration
}

// NewAPIRateLimiter creates a dual-bucket rate limiter. Call
// [APIRateLimiter.Stop] on shutdown.
func NewAPIRateLimiter(cfg APIRateLimitConfig) *APIRateLimiter {
	if cfg.AuthedMaxRequests == 0 {
		cfg.AuthedMaxRequests = 100
	}
	if cfg.UnauthedMaxRequests == 0 {
		cfg.UnauthedMaxRequests = 20
	}
	if cfg.Window == 0 {
		cfg.Window = time.Minute
	}
	return &APIRateLimiter{
		authed: ratelimit.New(ratelimit.Config{
			MaxRequests: cfg.AuthedMaxRequests,
			Window:      cfg.Window,
		}),
		unauthed: ratelimit.New(ratelimit.Config{
			MaxRequests: cfg.UnauthedMaxRequests,
			Window:      cfg.Window,
		}),
	}
}

// Stop releases the background cleanup goroutines for both buckets.
func (rl *APIRateLimiter) Stop() {
	rl.authed.Stop()
	rl.unauthed.Stop()
}

// Middleware returns a chi-compatible middleware that enforces rate
// limits. Authenticated requests (user id present in context via
// [authn.ActorFromContext]) use the per-user bucket; unauthenticated
// requests fall back to the per-IP bucket. Responses always include
// X-RateLimit-Limit, X-RateLimit-Remaining, and X-RateLimit-Reset
// headers. Rejected requests include a Retry-After header and return
// 429.
func (rl *APIRateLimiter) Middleware() func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			var res ratelimit.Result

			if userID, ok := authn.ActorFromContext(r.Context()); ok {
				key := fmt.Sprintf("user:%d", userID)
				res = rl.authed.Allow(key)
			} else {
				ip := ClientIPFromContext(r.Context())
				if ip == "" {
					next.ServeHTTP(w, r)
					return
				}
				res = rl.unauthed.Allow(ip)
			}

			setRateLimitHeaders(w, res)

			if !res.Allowed {
				w.Header().Set("Retry-After", ratelimit.FormatRetryAfter(res.RetryAfter))
				writeJSONError(w, http.StatusTooManyRequests, "RATE.LIMIT_EXCEEDED", "429 Too Many Requests")
				return
			}

			next.ServeHTTP(w, r)
		})
	}
}

// setRateLimitHeaders writes the standard X-RateLimit-* headers onto
// the response.
func setRateLimitHeaders(w http.ResponseWriter, res ratelimit.Result) {
	h := w.Header()
	h.Set("X-RateLimit-Limit", strconv.Itoa(res.Limit))
	h.Set("X-RateLimit-Remaining", strconv.Itoa(res.Remaining))
	h.Set("X-RateLimit-Reset", strconv.FormatInt(res.ResetUnix, 10))
}

// writeJSONError writes a structured JSON error response matching the
// standard apierrors envelope. It is used by middleware that runs
// before Huma and therefore cannot return a huma.StatusError.
func writeJSONError(w http.ResponseWriter, status int, code, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]any{
		"status":  status,
		"code":    code,
		"message": message,
	})
}
