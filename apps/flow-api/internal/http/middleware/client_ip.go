package middleware

import (
	"net/http"

	"github.com/nodate-flow/nodate-flow/packages/go-shared/httputil"
)

// ClientIP delegates to [httputil.ClientIPMiddlewareWithTrustedProxyHops].
func ClientIP(trustedProxyHops int) func(http.Handler) http.Handler {
	return httputil.ClientIPMiddlewareWithTrustedProxyHops(trustedProxyHops)
}
