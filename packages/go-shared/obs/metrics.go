// Package obs declares the Prometheus collectors that more than one
// service exports.
//
// Metric names are shared between services rather than prefixed per
// service. Each service runs as its own process and is scraped as its
// own Prometheus job, so the job label already separates them, and a
// common name lets a single query span the whole deployment.
//
// A shared name has to be declared exactly once. The default registry
// admits one collector per fully-qualified name and label set, and a
// binary may link the packages of several services at once — anything
// that exercises one service against another does. Declaring here, and
// registering only here, keeps that true no matter how the packages are
// combined. Each service's own obs package keeps its middleware, its
// /metrics handler, and the collectors it alone exports.
package obs

import (
	"time"

	"github.com/prometheus/client_golang/prometheus"
)

// httpRequestsTotal counts completed HTTP requests partitioned by method,
// route pattern, and response status code.
var httpRequestsTotal = prometheus.NewCounterVec(
	prometheus.CounterOpts{
		Name: "nf_http_requests_total",
		Help: "Total number of HTTP requests handled, partitioned by method, path, and status.",
	},
	[]string{"method", "path", "status"},
)

// httpRequestDuration observes request latency in seconds partitioned by
// method and route pattern. The default histogram buckets cover the range
// from 5 ms to 10 s which is suitable for most API workloads.
var httpRequestDuration = prometheus.NewHistogramVec(
	prometheus.HistogramOpts{
		Name:    "nf_http_request_duration_seconds",
		Help:    "Histogram of HTTP request latencies in seconds, partitioned by method and path.",
		Buckets: prometheus.DefBuckets,
	},
	[]string{"method", "path"},
)

func init() {
	prometheus.MustRegister(httpRequestsTotal)
	prometheus.MustRegister(httpRequestDuration)
}

// ObserveHTTPRequest records one completed HTTP request against both HTTP
// collectors, so the count and the latency of a request can never diverge.
//
// routePattern must be the templated route (for example
// "/workspaces/{workspace_id}/tasks"), never the raw path: the label
// values are unbounded otherwise, and identifiers carried in the path
// reach an endpoint that has no authentication of its own. status is the
// decimal response status code.
func ObserveHTTPRequest(method, routePattern, status string, elapsed time.Duration) {
	httpRequestsTotal.WithLabelValues(method, routePattern, status).Inc()
	httpRequestDuration.WithLabelValues(method, routePattern).Observe(elapsed.Seconds())
}
