// Metrics surface for flow-worker. Only the up gauge lives here; the
// per-job counters live with their job under internal/jobs and register
// against the same default Prometheus registry exposed by Handler().
package obs

import (
	"net/http"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

// UpGauge reports liveness for the flow-worker process. Set to 1 once
// MySQL has been dialled, the metrics endpoint is bound, and the runner
// has at least one job to run; 0 during graceful shutdown and for a
// worker that came up with nothing registered.
//
// That last case is why this is not simply "the process started". A
// worker whose only job is disabled by unset configuration ticks
// forever, answers every probe, and produces nothing — and everything
// downstream of it goes quiet with no failure anywhere to point at.
// Reporting 0 puts it in front of the alert that already exists.
//
//nolint:gochecknoglobals // process-wide metric, matches flow-api pattern.
var UpGauge = prometheus.NewGauge(prometheus.GaugeOpts{
	Name: "nf_flow_worker_up",
	Help: "1 when the flow-worker is initialised and has at least one job registered, 0 otherwise.",
})

// JobsRegisteredGauge reports how many jobs the runner was given. It
// names what UpGauge is reacting to, so an operator seeing up=0 can
// tell "no jobs configured" from "shutting down" without reading logs.
//
//nolint:gochecknoglobals // process-wide metric, matches flow-api pattern.
var JobsRegisteredGauge = prometheus.NewGauge(prometheus.GaugeOpts{
	Name: "nf_flow_worker_jobs_registered",
	Help: "Number of jobs registered on the flow-worker runner at boot.",
})

// CalendarEventDayTicksTotal counts how many times the calendar event-day
// job has executed a tick, partitioned by outcome. The job's Tick
// implementation increments it; registering it here means it shows up
// in /metrics output as soon as the binary boots.
//
// status values:
//   - "ok"      — tick scanned events and emitted (or skipped via dedupe) cleanly.
//   - "error"   — tick failed (DB read, internal POST /signals upstream, etc.).
//   - "skipped" — tick decided there was nothing to do (e.g. no workspaces).
//
// The workspace dimension is intentionally omitted: high-cardinality and
// out of scope for the v1 SLO. Per-workspace tick observability only
// becomes worth the cardinality once multiple jobs land and operators
// need per-tenant dashboards.
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
	prometheus.MustRegister(JobsRegisteredGauge)
	prometheus.MustRegister(CalendarEventDayTicksTotal)
	prometheus.MustRegister(CalendarEventDayTickSeconds)
}

// Handler returns the http.Handler that serves the Prometheus exposition
// format off the default registry. Mounted on /metrics by cmd/worker.
func Handler() http.Handler {
	return promhttp.Handler()
}
