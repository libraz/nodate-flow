package obs

import "github.com/prometheus/client_golang/prometheus"

// itemInconsistencyTotal counts drift rows detected by the reconciler,
// partitioned by drift kind. Non-zero rate is an alert signal: a
// writer is skipping itemkit.
var itemInconsistencyTotal = prometheus.NewCounterVec(
	prometheus.CounterOpts{
		Name: "nf_item_inconsistency_total",
		Help: "Total drift rows detected by the item-consistency reconciler, partitioned by kind.",
	},
	[]string{"kind"},
)

// itemReconcilerHealTotal counts rows the reconciler successfully
// healed, partitioned by drift kind. heal_total <= inconsistency_total
// because some kinds (orphan_role) are log-only.
var itemReconcilerHealTotal = prometheus.NewCounterVec(
	prometheus.CounterOpts{
		Name: "nf_item_reconciler_heal_total",
		Help: "Total drift rows self-healed by the item-consistency reconciler, partitioned by kind.",
	},
	[]string{"kind"},
)

// itemReconcilerRunTotal counts reconciler passes (one per tick).
var itemReconcilerRunTotal = prometheus.NewCounter(
	prometheus.CounterOpts{
		Name: "nf_item_reconciler_runs_total",
		Help: "Total item-consistency reconciler passes executed.",
	},
)

// itemReconcilerErrorsTotal counts query / heal errors in reconciler
// passes. Non-zero rate indicates a DB problem, not drift.
var itemReconcilerErrorsTotal = prometheus.NewCounter(
	prometheus.CounterOpts{
		Name: "nf_item_reconciler_errors_total",
		Help: "Total errors raised during item-consistency reconciler passes (query or heal failures).",
	},
)

func init() {
	prometheus.MustRegister(itemInconsistencyTotal)
	prometheus.MustRegister(itemReconcilerHealTotal)
	prometheus.MustRegister(itemReconcilerRunTotal)
	prometheus.MustRegister(itemReconcilerErrorsTotal)
}

// ReconcilerMetrics exposes the reconciler counters via the
// MetricsSink interface expected by internal/reconciler.
type ReconcilerMetrics struct{}

// IncInconsistency increments nf_item_inconsistency_total{kind}.
func (ReconcilerMetrics) IncInconsistency(kind string) {
	itemInconsistencyTotal.WithLabelValues(kind).Inc()
}

// IncHeal increments nf_item_reconciler_heal_total{kind}.
func (ReconcilerMetrics) IncHeal(kind string) {
	itemReconcilerHealTotal.WithLabelValues(kind).Inc()
}

// IncRun increments nf_item_reconciler_runs_total.
func (ReconcilerMetrics) IncRun() {
	itemReconcilerRunTotal.Inc()
}

// IncError increments nf_item_reconciler_errors_total.
func (ReconcilerMetrics) IncError() {
	itemReconcilerErrorsTotal.Inc()
}
