package middleware

import (
	"encoding/json"
	"net/http"
	"strconv"
	"time"

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
