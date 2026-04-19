package middleware

import (
	"context"
	"net/http"

	"github.com/nodate-flow/nodate-flow/packages/go-shared/authn"
	"github.com/nodate-flow/nodate-flow/packages/go-shared/httputil"
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
			ip := httputil.ExtractClientIP(r)
			if len(ip) > clientIPMaxLen {
				ip = ip[:clientIPMaxLen]
			}
			ctx := authn.WithClientIP(r.Context(), ip)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

// WithClientIP delegates to [authn.WithClientIP].
func WithClientIP(ctx context.Context, ip string) context.Context {
	return authn.WithClientIP(ctx, ip)
}

// ClientIPFromContext delegates to [authn.ClientIPFromContext].
func ClientIPFromContext(ctx context.Context) string {
	return authn.ClientIPFromContext(ctx)
}
