package obs

import "github.com/prometheus/client_golang/prometheus"

// aiInvocationsTotal counts completed LLM invocations, partitioned by provider
// kind, model name, and workspace.
var aiInvocationsTotal = prometheus.NewCounterVec(
	prometheus.CounterOpts{
		Name: "nf_ai_invocations_total",
		Help: "Total number of LLM invocations, partitioned by provider, model, and workspace.",
	},
	[]string{"provider", "model", "workspace_id"},
)

// aiCostDollarsTotal tracks cumulative LLM cost in US dollars, partitioned by
// provider kind, model name, and workspace. Values are floating-point dollars
// (e.g. 0.0015 for 0.15 cents).
var aiCostDollarsTotal = prometheus.NewCounterVec(
	prometheus.CounterOpts{
		Name: "nf_ai_cost_dollars_total",
		Help: "Cumulative LLM cost in USD, partitioned by provider, model, and workspace.",
	},
	[]string{"provider", "model", "workspace_id"},
)

// aiAcceptanceRate tracks the acceptance ratio of AI suggestions per
// workspace. This is a gauge because it represents a point-in-time ratio
// (accepted / total) that can go up or down as new data arrives.
var aiAcceptanceRate = prometheus.NewGaugeVec(
	prometheus.GaugeOpts{
		Name: "nf_ai_acceptance_rate",
		Help: "Acceptance rate (0-1) of AI suggestions, partitioned by workspace.",
	},
	[]string{"workspace_id"},
)

func init() {
	prometheus.MustRegister(aiInvocationsTotal)
	prometheus.MustRegister(aiCostDollarsTotal)
	prometheus.MustRegister(aiAcceptanceRate)
}

// RecordAIInvocation increments the invocation counter and adds the cost for a
// single completed LLM call. costCents is the estimated cost in cents as
// returned by providers.Response.CostCents; it is converted to dollars
// internally. Pass 0 for costCents when pricing is unknown (e.g. local
// Ollama).
func RecordAIInvocation(provider, model, workspaceID string, costCents int64) {
	aiInvocationsTotal.WithLabelValues(provider, model, workspaceID).Inc()
	if costCents > 0 {
		dollars := float64(costCents) / 100.0
		aiCostDollarsTotal.WithLabelValues(provider, model, workspaceID).Add(dollars)
	}
}

// RecordAIAcceptanceRate sets the current acceptance rate for a workspace.
// rate must be in the range [0, 1]. Call sites should recompute this when a
// suggestion is accepted or rejected and push the new ratio.
func RecordAIAcceptanceRate(workspaceID string, rate float64) {
	aiAcceptanceRate.WithLabelValues(workspaceID).Set(rate)
}
