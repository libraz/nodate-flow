package httputil

import (
	"net/http"

	"github.com/libraz/nodate-flow/packages/go-shared/authn"
	"github.com/libraz/nodate-flow/packages/go-shared/stringutil"
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
			// Both values reach utf8mb4 columns further down, and both
			// can carry arbitrary bytes: net/http accepts obs-text in a
			// header value, so a User-Agent is not ASCII by contract and
			// neither is a forwarding header. The caps therefore cut on a
			// rune boundary rather than a byte index.
			ip := stringutil.TruncateBytes(ExtractClientIPWithTrustedProxyHops(r, trustedProxyHops), ClientIPMaxLen)
			ctx := authn.WithClientIP(r.Context(), ip)
			if ua := r.UserAgent(); ua != "" {
				ctx = authn.WithUserAgent(ctx, stringutil.TruncateBytes(ua, UserAgentMaxLen))
			}
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}
