package httputil

import (
	"log/slog"
	"net/http"
	"net/url"
	"strings"

	"github.com/go-chi/cors"
)

// BuildCORS returns a chi CORS middleware configured from the runtime
// allowlist. Credentials are enabled so the refresh cookie and
// Authorization header round-trip to the browser; a single "*" entry
// disables credentials (per the CORS spec wildcard rules) and allows
// any origin. An empty allowlist disables CORS entirely (no-op middleware).
//
// devLocalhost is a development-only convenience: when true, any origin
// of the form http://localhost:<port> or http://127.0.0.1:<port> is
// accepted in addition to the explicit allowlist. This avoids re-pinning
// NF_*_CORS every time the Vite dev server picks a different port (e.g.
// when 5173 is already held by another local project). It MUST stay off
// in production; ship the explicit allowlist there. The wildcard "*"
// shortcut is unaffected by this flag.
func BuildCORS(allowed []string, devLocalhost bool) func(http.Handler) http.Handler {
	if len(allowed) == 0 && !devLocalhost {
		return func(next http.Handler) http.Handler { return next }
	}
	allowCreds := true
	if len(allowed) == 1 && allowed[0] == "*" {
		allowCreds = false
		slog.Warn("CORS configured with wildcard origin: credentials (cookies, Authorization header) are disabled per the CORS spec")
	}
	opts := cors.Options{
		AllowedOrigins:   allowed,
		AllowedMethods:   []string{"GET", "POST", "PUT", "PATCH", "DELETE", "OPTIONS"},
		AllowedHeaders:   []string{"Accept", "Authorization", "Content-Type", "X-Requested-With"},
		ExposedHeaders:   []string{"X-Request-Id"},
		AllowCredentials: allowCreds,
		MaxAge:           3600,
	}
	if devLocalhost && allowCreds {
		slog.Warn("CORS dev-localhost mode enabled: any http://localhost:<port> or http://127.0.0.1:<port> origin will be accepted (development only; do NOT enable in production)")
		allowedSet := make(map[string]struct{}, len(allowed))
		for _, o := range allowed {
			allowedSet[strings.ToLower(strings.TrimSpace(o))] = struct{}{}
		}
		opts.AllowOriginFunc = func(_ *http.Request, origin string) bool {
			lower := strings.ToLower(origin)
			if _, ok := allowedSet[lower]; ok {
				return true
			}
			return isDevLocalhostOrigin(lower)
		}
	}
	return cors.Handler(opts)
}

// isDevLocalhostOrigin reports whether origin is an http:// (plain, not
// https) URL whose host is localhost or 127.0.0.1, with any port. The
// origin is matched case-insensitively. Returns false for malformed
// inputs, https origins, or any other host.
func isDevLocalhostOrigin(origin string) bool {
	u, err := url.Parse(origin)
	if err != nil || u.Scheme != "http" {
		return false
	}
	host := u.Hostname()
	return host == "localhost" || host == "127.0.0.1"
}
