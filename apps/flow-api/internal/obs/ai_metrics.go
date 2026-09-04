package obs

import "github.com/prometheus/client_golang/prometheus"

// Values of the outcome label on aiInvocationsTotal. Queries and alert rules
// match on these strings, so this file is their only producer.
const (
	aiOutcomeSuccess = "success"
	aiOutcomeError   = "error"
)

// aiInvocationsTotal counts completed LLM invocations, partitioned by provider
// kind, model name, workspace, and outcome.
var aiInvocationsTotal = prometheus.NewCounterVec(
	prometheus.CounterOpts{
		Name: "nf_ai_invocations_total",
		Help: "Total number of LLM invocations, partitioned by provider, model, workspace, and outcome.",
	},
	[]string{"provider", "model", "workspace_id", "outcome"},
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
// single completed LLM call. costMicros is the estimated cost in millionths of
// a US dollar. Pass 0 when pricing is unknown (e.g. local Ollama); because a
// successful call can therefore cost 0, cost cannot distinguish success from
// failure. err is the sole determinant of the outcome label: nil records
// "success", any non-nil error records "error".
func RecordAIInvocation(provider, model, workspaceID string, costMicros int64, err error) {
	outcome := aiOutcomeSuccess
	if err != nil {
		outcome = aiOutcomeError
	}
	aiInvocationsTotal.WithLabelValues(provider, model, workspaceID, outcome).Inc()
	if costMicros > 0 {
		dollars := float64(costMicros) / 1_000_000.0
		aiCostDollarsTotal.WithLabelValues(provider, model, workspaceID).Add(dollars)
	}
}

// RecordAIAcceptanceRate sets the current acceptance rate for a workspace.
// rate must be in the range [0, 1]. Call sites should recompute this when a
// suggestion is accepted or rejected and push the new ratio.
func RecordAIAcceptanceRate(workspaceID string, rate float64) {
	aiAcceptanceRate.WithLabelValues(workspaceID).Set(rate)
}
