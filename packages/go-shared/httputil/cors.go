package httputil

import (
	"log/slog"
	"net/http"

	"github.com/go-chi/cors"
)

// BuildCORS returns a chi CORS middleware configured from the runtime
// allowlist. Credentials are enabled so the refresh cookie and
// Authorization header round-trip to the browser; a single "*" entry
// disables credentials (per the CORS spec wildcard rules) and allows
// any origin. An empty allowlist disables CORS entirely (no-op middleware).
func BuildCORS(allowed []string) func(http.Handler) http.Handler {
	if len(allowed) == 0 {
		return func(next http.Handler) http.Handler { return next }
	}
	allowCreds := true
	if len(allowed) == 1 && allowed[0] == "*" {
		allowCreds = false
		slog.Warn("CORS configured with wildcard origin: credentials (cookies, Authorization header) are disabled per the CORS spec")
	}
	return cors.Handler(cors.Options{
		AllowedOrigins:   allowed,
		AllowedMethods:   []string{"GET", "POST", "PUT", "PATCH", "DELETE", "OPTIONS"},
		AllowedHeaders:   []string{"Accept", "Authorization", "Content-Type", "X-Requested-With"},
		ExposedHeaders:   []string{"X-Request-Id"},
		AllowCredentials: allowCreds,
		MaxAge:           3600,
	})
}
