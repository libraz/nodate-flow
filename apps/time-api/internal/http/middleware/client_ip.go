package middleware

import (
	"context"
	"net"
	"net/http"
	"strings"
)

// clientIPMaxLen caps the stored IP string to avoid unbounded
// X-Forwarded-For header abuse (legitimate IPv6 is <= 45 chars).
const clientIPMaxLen = 64

const ctxKeyClientIP ctxKey = 100

// ClientIP is a chi-compatible middleware that extracts the caller's
// IP address from X-Forwarded-For (first hop) when present, falling
// back to r.RemoteAddr with the port stripped. The result is stashed
// on the request context via [WithClientIP] for downstream handlers.
func ClientIP() func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			ip := extractClientIP(r)
			if len(ip) > clientIPMaxLen {
				ip = ip[:clientIPMaxLen]
			}
			ctx := WithClientIP(r.Context(), ip)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

// WithClientIP returns a new context carrying the caller's client IP
// address string.
func WithClientIP(ctx context.Context, ip string) context.Context {
	return context.WithValue(ctx, ctxKeyClientIP, ip)
}

// ClientIPFromContext extracts the caller's client IP address as
// stored by [ClientIP] or [WithClientIP].
func ClientIPFromContext(ctx context.Context) string {
	v, _ := ctx.Value(ctxKeyClientIP).(string)
	return v
}

func extractClientIP(r *http.Request) string {
	if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
		// First hop is the original client.
		if comma := strings.IndexByte(xff, ','); comma >= 0 {
			return strings.TrimSpace(xff[:comma])
		}
		return strings.TrimSpace(xff)
	}
	if host, _, err := net.SplitHostPort(r.RemoteAddr); err == nil {
		return host
	}
	return r.RemoteAddr
}
