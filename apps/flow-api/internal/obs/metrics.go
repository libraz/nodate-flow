package obs

import (
	"net/http"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/prometheus/client_golang/prometheus/promhttp"

	sharedobs "github.com/libraz/nodate-flow/packages/go-shared/obs"
)

// MetricsHandler returns an http.Handler that serves Prometheus metrics at
// /metrics. It delegates to promhttp.Handler which writes the standard
// text exposition format.
func MetricsHandler() http.Handler {
	return promhttp.Handler()
}

// MetricsMiddleware returns a chi-compatible HTTP middleware that records
// request count and latency for every inbound request. Route patterns are
// resolved via chi.RouteContext so high-cardinality path parameters (e.g.
// UUIDs) are collapsed into their placeholder form, keeping the label
// cardinality bounded.
//
// Mount this middleware on the outermost chi router, after the request
// logger (which injects request_id) and before the application routes.
func MetricsMiddleware() func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			start := time.Now()

			rec := &metricsStatusCapture{ResponseWriter: w, status: http.StatusOK}
			next.ServeHTTP(rec, r)

			// Resolve the chi route pattern so we get a low-cardinality
			// label like "/workspaces/{workspace_id}/tasks/{task_id}"
			// instead of the raw path with UUIDs.
			routePattern := r.URL.Path
			if rctx := chi.RouteContext(r.Context()); rctx != nil {
				if pat := rctx.RoutePattern(); pat != "" {
					routePattern = pat
				}
			}

			sharedobs.ObserveHTTPRequest(r.Method, routePattern, strconv.Itoa(rec.status), time.Since(start))
		})
	}
}

// metricsStatusCapture wraps http.ResponseWriter to capture the status code
// for Prometheus labels. It implements http.Flusher when the underlying
// writer supports it so SSE streams work correctly.
type metricsStatusCapture struct {
	http.ResponseWriter
	status      int
	wroteHeader bool
}

// WriteHeader records the status code before delegating.
func (s *metricsStatusCapture) WriteHeader(code int) {
	if !s.wroteHeader {
		s.status = code
		s.wroteHeader = true
	}
	s.ResponseWriter.WriteHeader(code)
}

// Write delegates to the underlying writer, recording a 200 status if
// WriteHeader was never called explicitly.
func (s *metricsStatusCapture) Write(b []byte) (int, error) {
	if !s.wroteHeader {
		s.wroteHeader = true
		// Default status is already 200
	}
	return s.ResponseWriter.Write(b)
}

// Flush delegates to the underlying writer if it implements http.Flusher.
func (s *metricsStatusCapture) Flush() {
	if f, ok := s.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

// Unwrap returns the original ResponseWriter so middleware such as
// http.ResponseController can access it.
func (s *metricsStatusCapture) Unwrap() http.ResponseWriter {
	return s.ResponseWriter
}

// ensure interface compliance at compile time.
var _ http.ResponseWriter = (*metricsStatusCapture)(nil)
var _ http.Flusher = (*metricsStatusCapture)(nil)
