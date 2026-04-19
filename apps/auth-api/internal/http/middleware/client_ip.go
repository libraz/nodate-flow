package middleware

import (
	"net"
	"net/http"
	"strings"

	"github.com/nodate-flow/nodate-flow/packages/go-shared/authn"
)

// clientIPMaxLen caps the stored IP string to avoid unbounded
// X-Forwarded-For header abuse (legitimate IPv6 is <= 45 chars).
const clientIPMaxLen = 64

// ClientIP is a chi-compatible middleware that extracts the caller's
// IP address from X-Forwarded-For (first hop) when present, falling
// back to r.RemoteAddr with the port stripped. The result is stashed
// on the request context via [authn.WithClientIP] for downstream handlers.
func ClientIP() func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			ip := extractClientIP(r)
			if len(ip) > clientIPMaxLen {
				ip = ip[:clientIPMaxLen]
			}
			ctx := authn.WithClientIP(r.Context(), ip)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

func extractClientIP(r *http.Request) string {
	if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
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
