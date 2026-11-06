package lifecycle

import (
	"context"
	"net/http"
)

type streamShutdownKey struct{}

// WithStreamShutdown returns middleware that hands closing to every request
// through [StreamShutdown]. The caller closes it from
// http.Server.RegisterOnShutdown, so long-lived streams end when shutdown
// begins instead of holding Server.Shutdown open until its deadline. Request
// contexts are left alone: ordinary requests still drain normally.
func WithStreamShutdown(closing <-chan struct{}) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), streamShutdownKey{}, closing)))
		})
	}
}

// StreamShutdown returns the channel closed when the server begins shutting
// down, or nil when no [WithStreamShutdown] middleware ran. A nil channel
// never fires in a select, so a handler can always wait on it.
func StreamShutdown(ctx context.Context) <-chan struct{} {
	closing, _ := ctx.Value(streamShutdownKey{}).(<-chan struct{})
	return closing
}
