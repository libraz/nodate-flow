package httputil

import (
	"net/http"

	"github.com/nodate-flow/nodate-flow/packages/go-shared/authn"
)

// ClientIPMaxLen caps the stored IP string to avoid unbounded
// X-Forwarded-For header abuse (legitimate IPv6 is <= 45 chars).
const ClientIPMaxLen = 64

// ClientIPMiddleware is a chi-compatible middleware that extracts the
// caller's IP address from X-Forwarded-For / X-Real-Ip / RemoteAddr
// via [ExtractClientIP] and stashes it on the request context via
// [authn.WithClientIP] for downstream handlers.
func ClientIPMiddleware() func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			ip := ExtractClientIP(r)
			if len(ip) > ClientIPMaxLen {
				ip = ip[:ClientIPMaxLen]
			}
			ctx := authn.WithClientIP(r.Context(), ip)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}
