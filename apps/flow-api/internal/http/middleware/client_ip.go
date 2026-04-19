package middleware

import (
	"net/http"

	"github.com/nodate-flow/nodate-flow/packages/go-shared/httputil"
)

// ClientIP delegates to [httputil.ClientIPMiddleware].
func ClientIP() func(http.Handler) http.Handler {
	return httputil.ClientIPMiddleware()
}
