// Metrics surface for flow-worker. Only the up gauge lives here; the
// per-job counters live with their job under internal/jobs and register
// against the same default Prometheus registry exposed by Handler().
package obs

import (
	"net/http"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

// UpGauge reports whether the flow-worker is doing the work its
// configuration calls for. Set to 1 once MySQL has been dialled, the
// metrics endpoint is bound, and every job the configuration implies is
// registered; 0 during graceful shutdown, for a worker that registered
// nothing, and for one missing a job its configuration asked for.
//
// That last case is the whole point of the metric. Prometheus already
// reports process liveness as up{job="flow-worker"} on every scrape, so a
// self-reported "the process started" would say nothing new. What only
// this process can answer is whether it came up complete: a worker whose
// flow-api job is disabled by an unset service token ticks forever,
// answers every probe, and produces nothing — and everything downstream
// of it goes quiet with no failure anywhere to point at.
//
// Jobs registered unconditionally do not count towards it. Their presence
// is a property of the binary, not of the deployment's configuration, and
// letting one satisfy the gauge is exactly what would make a misconfigured
// worker look complete.
//
//nolint:gochecknoglobals // process-wide metric, matches flow-api pattern.
var UpGauge = prometheus.NewGauge(prometheus.GaugeOpts{
	Name: "nf_flow_worker_up",
	Help: "1 when every job the flow-worker's configuration calls for is registered; " +
		"0 when one is missing (an unset NF_FLOW_API_SIGNAL_TOKEN leaves the calendar job out), " +
		"when nothing is registered, and during shutdown.",
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

// TasksTotalGauge reports how many live tasks the instance holds. A
// point-in-time count, refreshed by the business_metrics job rather than
// incremented at a write path, so it stays correct across a restart and
// across writes the worker never sees.
//
// No workspace label, here or on the two gauges below: workspaces are
// identified internally by a sequential id that must not reach an
// unauthenticated /metrics, and a per-workspace series is unbounded
// cardinality. The instance-wide number is what the dashboard asks for.
//
//nolint:gochecknoglobals // process-wide metric, matches flow-api pattern.
var TasksTotalGauge = prometheus.NewGauge(prometheus.GaugeOpts{
	Name: "nf_tasks_total",
	Help: "Live tasks across the instance: enabled tasks in enabled workspaces, archived included.",
})

// TasksByStateGauge breaks TasksTotalGauge down by derived state. The
// label values are the five values of the tasks state enum, so the
// series count is bounded and small.
//
// Both gauges come from one aggregate, so the parts always sum to the
// total; two separate queries could be scraped mid-write and disagree.
//
//nolint:gochecknoglobals // process-wide metric, matches flow-api pattern.
var TasksByStateGauge = prometheus.NewGaugeVec(
	prometheus.GaugeOpts{
		Name: "nf_tasks_by_state",
		Help: "Live tasks across the instance by derived state (open, waiting, review, done, cancelled).",
	},
	[]string{"state"},
)

// ActiveWorkspacesGauge counts the enabled workspaces that appended at
// least one event in the trailing activity window. The window is the
// job's ActiveWindow (5 minutes by default, matching the dashboard
// panel), and the event log is the definition of "active" because every
// state transition passes through it — HTTP traffic would also count a
// poll that changed nothing.
//
//nolint:gochecknoglobals // process-wide metric, matches flow-api pattern.
var ActiveWorkspacesGauge = prometheus.NewGauge(prometheus.GaugeOpts{
	Name: "nf_active_workspaces",
	Help: "Enabled workspaces with at least one event appended in the trailing 5 minute window.",
})

//nolint:gochecknoinits // metric registration must happen at package load.
func init() {
	prometheus.MustRegister(UpGauge)
	prometheus.MustRegister(JobsRegisteredGauge)
	prometheus.MustRegister(CalendarEventDayTicksTotal)
	prometheus.MustRegister(CalendarEventDayTickSeconds)
	prometheus.MustRegister(TasksTotalGauge)
	prometheus.MustRegister(TasksByStateGauge)
	prometheus.MustRegister(ActiveWorkspacesGauge)
}

// Handler returns the http.Handler that serves the Prometheus exposition
// format off the default registry. Mounted on /metrics by cmd/worker.
func Handler() http.Handler {
	return promhttp.Handler()
}
