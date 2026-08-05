package bgloop

import "github.com/prometheus/client_golang/prometheus"

// restartsTotal counts how often a supervised background loop had to be
// restarted, split by why.
//
// The scrape is what makes a silent stop noticeable: a loop that dies
// and restarts every 30 seconds looks identical to a healthy one from
// the outside — the process is up, /health passes, requests are served
// — while the work that loop does simply stops happening. Alerting on
// any non-zero rate here is the intended use.
//
// The counter lives here rather than in internal/obs so that package's
// own polling loop can be supervised too: obs would otherwise have to
// import this package while this package imported obs.
var restartsTotal = prometheus.NewCounterVec(
	prometheus.CounterOpts{
		Name: "nf_background_loop_restarts_total",
		Help: "Background loop restarts, labelled by loop name and reason (panic|returned).",
	},
	[]string{"loop", "reason"},
)

func init() {
	prometheus.MustRegister(restartsTotal)
}
