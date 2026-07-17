package httputil

import (
	"net/http"

	"github.com/nodate-flow/nodate-flow/packages/go-shared/authn"
)

// ClientIPMaxLen caps the stored IP string to avoid unbounded
// X-Forwarded-For header abuse (legitimate IPv6 is <= 45 chars).
const ClientIPMaxLen = 64

// UserAgentMaxLen caps the User-Agent stashed on the request context so a
// malicious client cannot use it as a write-amplification vector. It
// matches the audit_logs.user_agent column width.
const UserAgentMaxLen = 512

// ClientIPMiddleware is a chi-compatible middleware that extracts the
// caller's IP address from RemoteAddr and stashes it on the request
// context via [authn.WithClientIP] for downstream handlers. It does not
// trust X-Forwarded-For or X-Real-Ip.
func ClientIPMiddleware() func(http.Handler) http.Handler {
	return ClientIPMiddlewareWithTrustedProxyHops(0)
}

// ClientIPMiddlewareWithTrustedProxyHops returns a middleware that trusts
// forwarding headers only when trustedProxyHops is positive. See
// [ExtractClientIPWithTrustedProxyHops] for the hop-selection rules.
func ClientIPMiddlewareWithTrustedProxyHops(trustedProxyHops int) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			ip := ExtractClientIPWithTrustedProxyHops(r, trustedProxyHops)
			if len(ip) > ClientIPMaxLen {
				ip = ip[:ClientIPMaxLen]
			}
			ctx := authn.WithClientIP(r.Context(), ip)
			if ua := r.UserAgent(); ua != "" {
				if len(ua) > UserAgentMaxLen {
					ua = ua[:UserAgentMaxLen]
				}
				ctx = authn.WithUserAgent(ctx, ua)
			}
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}
