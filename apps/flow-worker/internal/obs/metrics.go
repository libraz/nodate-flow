// Metrics surface for flow-worker. W1 ships only the up gauge; the
// per-job counters live with their job under internal/jobs and register
// against the same default Prometheus registry exposed by Handler().
package obs

import (
	"net/http"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

// UpGauge reports liveness for the flow-worker process. Set to 1 after
// MySQL has been dialled successfully and to 0 during graceful shutdown.
//
//nolint:gochecknoglobals // process-wide metric, matches flow-api pattern.
var UpGauge = prometheus.NewGauge(prometheus.GaugeOpts{
	Name: "nf_flow_worker_up",
	Help: "1 when the flow-worker is initialised and the job runner is active, 0 otherwise.",
})

// CalendarEventDayTicksTotal counts how many times the calendar event-day
// job has executed a tick, partitioned by outcome. W2 increments this from
// the job's Tick implementation; W1 only registers the metric so it shows
// up in /metrics output as soon as the binary boots.
//
// status values:
//   - "ok"      — tick scanned events and emitted (or skipped via dedupe) cleanly.
//   - "error"   — tick failed (DB read, internal POST /signals upstream, etc.).
//   - "skipped" — tick decided there was nothing to do (e.g. no workspaces).
//
// The workspace dimension is intentionally omitted: high-cardinality and
// out of scope for the v1 SLO. Per-workspace tick observability is a
// Phase 9+ concern when multiple jobs land and operators need per-tenant
// dashboards.
//
//nolint:gochecknoglobals // process-wide metric, matches flow-api pattern.
var CalendarEventDayTicksTotal = prometheus.NewCounterVec(
	prometheus.CounterOpts{
		Name: "nf_flow_worker_calendar_event_day_ticks_total",
		Help: "Total ticks of the calendar event-day signal job, by outcome.",
	},
	[]string{"status"},
)

// CalendarEventDayTickSeconds observes the wall-clock duration of one
// calendar event-day job tick. Operators can graph p95 against the 60s
// tick cadence to detect drift before ticks start overlapping.
//
// Default buckets cover the expected 5 ms – 10 s range; the 60s tick
// interval makes anything past p95 of ~5s a soft alarm worth dashboarding
// in a later phase.
//
//nolint:gochecknoglobals // process-wide metric, matches flow-api pattern.
var CalendarEventDayTickSeconds = prometheus.NewHistogram(
	prometheus.HistogramOpts{
		Name:    "nf_flow_worker_calendar_event_day_tick_seconds",
		Help:    "Duration of one calendar event-day job tick, in seconds.",
		Buckets: prometheus.DefBuckets,
	},
)

//nolint:gochecknoinits // metric registration must happen at package load.
func init() {
	prometheus.MustRegister(UpGauge)
	prometheus.MustRegister(CalendarEventDayTicksTotal)
	prometheus.MustRegister(CalendarEventDayTickSeconds)
}

// Handler returns the http.Handler that serves the Prometheus exposition
// format off the default registry. Mounted on /metrics by cmd/worker.
func Handler() http.Handler {
	return promhttp.Handler()
}
