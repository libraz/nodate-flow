package middleware

import (
	"context"
	"net/http"

	"github.com/nodate-flow/nodate-flow/packages/go-shared/authn"
	"github.com/nodate-flow/nodate-flow/packages/go-shared/httputil"
)

// ClientIP delegates to [httputil.ClientIPMiddleware].
func ClientIP() func(http.Handler) http.Handler {
	return httputil.ClientIPMiddleware()
}

// WithClientIP delegates to [authn.WithClientIP].
func WithClientIP(ctx context.Context, ip string) context.Context {
	return authn.WithClientIP(ctx, ip)
}

// ClientIPFromContext delegates to [authn.ClientIPFromContext].
func ClientIPFromContext(ctx context.Context) string {
	return authn.ClientIPFromContext(ctx)
}
