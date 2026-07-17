package obs

import (
	"fmt"
	"net/http"
	"strings"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"

	nflog "github.com/nodate-flow/nodate-flow/apps/flow-api/internal/log"

	"github.com/go-chi/chi/v5"

	"github.com/nodate-flow/nodate-flow/apps/flow-api/internal/http/middleware"
)

const tracerName = "github.com/nodate-flow/nodate-flow/apps/flow-api/internal/obs"

// domainForRoute returns the logical sub-domain a route belongs to, for
// use as an OTel span attribute. After the time-api → flow-api merge the
// process serves both task and calendar surfaces; tagging spans with
// domain=calendar lets trace queries continue to slice the calendar
// surface by itself without needing service.name to differ.
func domainForRoute(routePattern string) string {
	switch {
	case strings.Contains(routePattern, "/calendars"),
		strings.HasPrefix(routePattern, "/share/cal"),
		strings.HasPrefix(routePattern, "/invites/"),
		strings.HasPrefix(routePattern, "/me/invites"):
		return "calendar"
	default:
		return "task"
	}
}

// TraceMiddleware returns a chi-compatible HTTP middleware that starts an OTel
// span for every inbound request. The span name follows the convention
// "HTTP {method} {route_pattern}" and carries request_id, user_id, and
// workspace_id as attributes when available in the request context.
//
// The middleware should be mounted after the request-logger middleware (which
// injects request_id) and after the ACL middleware (which injects actor and
// workspace metadata).
func TraceMiddleware() func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			tracer := otel.Tracer(tracerName)

			// Use the chi route pattern when available; fall back to the
			// raw URL path so we always have a meaningful span name.
			routePattern := r.URL.Path
			if rctx := chi.RouteContext(r.Context()); rctx != nil {
				if pat := rctx.RoutePattern(); pat != "" {
					routePattern = pat
				}
			}
			spanName := fmt.Sprintf("HTTP %s %s", r.Method, routePattern)

			ctx, span := tracer.Start(r.Context(), spanName,
				trace.WithSpanKind(trace.SpanKindServer),
			)
			defer span.End()

			// Attach structured identifiers as span attributes.
			attrs := []attribute.KeyValue{
				attribute.String("domain", domainForRoute(routePattern)),
			}
			if reqID := nflog.RequestIDFromContext(ctx); reqID != "" {
				attrs = append(attrs, attribute.String("request_id", reqID))
			}
			// user_id intentionally omitted: the actor context only carries
			// the internal sequential id, which must never be exposed (see
			// requirements §11.9). The public actor UUID is not available in
			// the request context without a DB lookup, so the attribute is
			// dropped rather than leaking the internal id.
			if ws, ok := middleware.WorkspaceFromContext(ctx); ok {
				attrs = append(attrs, attribute.String("workspace_id", ws.PublicID.String()))
			}
			if len(attrs) > 0 {
				span.SetAttributes(attrs...)
			}

			// Record the HTTP status after the handler runs.
			rec := &statusCapture{ResponseWriter: w, status: http.StatusOK}
			next.ServeHTTP(rec, r.WithContext(ctx))

			// Never record r.URL.String(): the query string and capability
			// tokens embedded in path params (/share/cal/{token},
			// /public/lenses/{token}) are unguessable random strings with no
			// redactable prefix. Record the chi route pattern (fully
			// templated) plus a path with param values masked, and drop the
			// query string entirely.
			span.SetAttributes(
				attribute.String("http.method", r.Method),
				attribute.String("http.route", routePattern),
				attribute.String("http.target", maskPathParams(r)),
				attribute.Int("http.status_code", rec.status),
			)
		})
	}
}

// maskPathParams returns the request path with every chi URL-param value
// replaced by its "{name}" placeholder, so capability tokens carried in the
// path (share/lens tokens, ids) never reach the trace exporter. The query
// string is dropped. When no route context is available the raw path is
// returned unchanged.
func maskPathParams(r *http.Request) string {
	path := r.URL.Path
	rctx := chi.RouteContext(r.Context())
	if rctx == nil {
		return path
	}
	params := rctx.URLParams
	for i, key := range params.Keys {
		if i >= len(params.Values) {
			break
		}
		val := params.Values[i]
		if val == "" || key == "*" {
			continue
		}
		path = strings.Replace(path, val, "{"+key+"}", 1)
	}
	return path
}

// statusCapture wraps an http.ResponseWriter to capture the response status
// code for span attributes.
type statusCapture struct {
	http.ResponseWriter
	status int
}

// WriteHeader records the status code before delegating.
func (s *statusCapture) WriteHeader(code int) {
	s.status = code
	s.ResponseWriter.WriteHeader(code)
}
