package obs

import (
	"fmt"
	"net/http"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"

	nflog "github.com/nodate-flow/nodate-flow/apps/flow-api/internal/log"

	"github.com/go-chi/chi/v5"

	"github.com/nodate-flow/nodate-flow/apps/flow-api/internal/http/middleware"
)

const tracerName = "github.com/nodate-flow/nodate-flow/apps/flow-api/internal/obs"

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
			var attrs []attribute.KeyValue
			if reqID := nflog.RequestIDFromContext(ctx); reqID != "" {
				attrs = append(attrs, attribute.String("request_id", reqID))
			}
			if uid, ok := middleware.ActorFromContext(ctx); ok {
				attrs = append(attrs, attribute.Int64("user_id", int64(uid)))
			}
			if ws, ok := middleware.WorkspaceFromContext(ctx); ok {
				attrs = append(attrs, attribute.String("workspace_id", ws.PublicID.String()))
			}
			if len(attrs) > 0 {
				span.SetAttributes(attrs...)
			}

			// Record the HTTP status after the handler runs.
			rec := &statusCapture{ResponseWriter: w, status: http.StatusOK}
			next.ServeHTTP(rec, r.WithContext(ctx))

			span.SetAttributes(
				attribute.String("http.method", r.Method),
				attribute.String("http.url", r.URL.String()),
				attribute.Int("http.status_code", rec.status),
			)
		})
	}
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
