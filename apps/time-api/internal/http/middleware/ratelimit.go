package middleware

import "github.com/nodate-flow/nodate-flow/packages/go-shared/httputil"

// RateLimitConfig is an alias for [httputil.RateLimitConfig].
type RateLimitConfig = httputil.RateLimitConfig

// IPRateLimiter is an alias for [httputil.IPRateLimiter].
type IPRateLimiter = httputil.IPRateLimiter

// NewIPRateLimiter delegates to [httputil.NewIPRateLimiter].
func NewIPRateLimiter(cfg RateLimitConfig) *IPRateLimiter {
	return httputil.NewIPRateLimiter(cfg)
}
