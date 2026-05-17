// Metrics surface for presence-discord. P8-1 pre-registers the three
// metrics named in docs/plan/release-8-signals-and-judge-loop.md (Phase
// 8 / P2). The gateway implementation (P8-2) increments them as events
// are received, debounced, and emitted.
//
// The Up gauge mirrors flow-worker's nf_flow_worker_up and flow-api's
// equivalent so dashboards can use a single "process up" pattern across
// all three long-running binaries.
package obs

import (
	"net/http"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

// GatewayUp reports liveness for the Discord gateway WebSocket
// connection. Set to 1 once the gateway is connected and identified
// with Discord, 0 during shutdown or while the WS link is down.
//
// This is the binary-level "alive" signal: dashboards alert when this
// is 0 for longer than the gateway's expected reconnect ceiling.
//
//nolint:gochecknoglobals // process-wide metric, matches flow-api / flow-worker pattern.
var GatewayUp = prometheus.NewGauge(prometheus.GaugeOpts{
	Name: "nf_presence_discord_gateway_up",
	Help: "1 when the Discord gateway WebSocket is connected and identified, 0 otherwise.",
})

// EventsTotal counts gateway events the binary has processed,
// partitioned by kind.
//
// kind values reserved for P8-2:
//   - "presence_update" — Discord PresenceUpdate gateway event received
//   - "signal_emitted"  — POST /signals to flow-api succeeded
//   - "signal_failed"   — POST /signals to flow-api errored
//   - "drop_no_user"    — presence belongs to a Discord user with no
//     matching user_integrations.metadata_json.external_user_id
//
// More kinds may be added by P8-2 without coordination; the label is
// low-cardinality by design (no user_id, no guild_id).
//
//nolint:gochecknoglobals // process-wide metric, matches flow-api / flow-worker pattern.
var EventsTotal = prometheus.NewCounterVec(
	prometheus.CounterOpts{
		Name: "nf_presence_discord_events_total",
		Help: "Total gateway events processed by presence-discord, by kind.",
	},
	[]string{"kind"},
)

// DebounceDroppedTotal counts presence updates that were collapsed into
// a later event within the per-user debounce window. A non-zero rate is
// expected and healthy — it confirms the storm-suppression is active.
//
//nolint:gochecknoglobals // process-wide metric, matches flow-api / flow-worker pattern.
var DebounceDroppedTotal = prometheus.NewCounter(prometheus.CounterOpts{
	Name: "nf_presence_discord_debounce_dropped_total",
	Help: "Total presence updates dropped by the per-user debounce window.",
})

//nolint:gochecknoinits // metric registration must happen at package load.
func init() {
	prometheus.MustRegister(GatewayUp)
	prometheus.MustRegister(EventsTotal)
	prometheus.MustRegister(DebounceDroppedTotal)

	// Pre-materialise the known kind label values so the counter series
	// appear in /metrics from boot with value 0. Without this the CounterVec
	// would emit only the HELP/TYPE comments until the first increment,
	// which breaks dashboards that auto-discover series by name. Add new
	// kinds here as P8-2 introduces them.
	for _, kind := range []string{
		"presence_update",
		"signal_emitted",
		"signal_failed",
		"drop_no_user",
	} {
		EventsTotal.WithLabelValues(kind)
	}
}

// Handler returns the http.Handler that serves the Prometheus exposition
// format off the default registry. Mounted on /metrics by lifecycle.Run.
func Handler() http.Handler {
	return promhttp.Handler()
}
