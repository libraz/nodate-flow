package obs

import (
	"time"

	"github.com/prometheus/client_golang/prometheus"
)

// Values of the outcome label on aiInvocationsTotal. Queries and alert rules
// match on these strings, so this file is their only producer.
const (
	aiOutcomeSuccess = "success"
	aiOutcomeError   = "error"
)

// Values of the type label on aiTokensTotal. Dashboard queries aggregate by
// this label, so this file is their only producer.
const (
	aiTokenTypePrompt     = "prompt"
	aiTokenTypeCompletion = "completion"
)

// aiInvocationsTotal counts completed LLM invocations, partitioned by provider
// kind, model name, and outcome.
var aiInvocationsTotal = prometheus.NewCounterVec(
	prometheus.CounterOpts{
		Name: "nf_ai_invocations_total",
		Help: "Total number of LLM invocations, partitioned by provider, model, and outcome.",
	},
	[]string{"provider", "model", "outcome"},
)

// aiCostDollarsTotal tracks cumulative LLM cost in US dollars, partitioned by
// provider kind and model name. Values are floating-point dollars (e.g. 0.0015
// for 0.15 cents).
var aiCostDollarsTotal = prometheus.NewCounterVec(
	prometheus.CounterOpts{
		Name: "nf_ai_cost_dollars_total",
		Help: "Cumulative LLM cost in USD, partitioned by provider and model.",
	},
	[]string{"provider", "model"},
)

// aiTokensTotal counts tokens consumed by LLM invocations, partitioned by
// direction ("prompt" or "completion") and model name. Prompt and completion
// tokens are priced differently, so they are kept on separate series rather
// than summed.
var aiTokensTotal = prometheus.NewCounterVec(
	prometheus.CounterOpts{
		Name: "nf_ai_tokens_total",
		Help: "Total number of LLM tokens consumed, partitioned by type and model.",
	},
	[]string{"type", "model"},
)

// aiRequestDuration is a histogram of end-to-end LLM request latencies in
// seconds, partitioned by provider kind and model name. The buckets are
// explicit rather than prometheus.DefBuckets: the default set ends at 10 s,
// while a completion routinely runs longer than that, so every slow call would
// fall into the +Inf bucket and the high quantiles would be interpolated from
// nothing. The range below spans a quarter of a second to two minutes, which
// covers a short classification call and a long generation alike.
var aiRequestDuration = prometheus.NewHistogramVec(
	prometheus.HistogramOpts{
		Name:    "nf_ai_request_duration_seconds",
		Help:    "Histogram of LLM request latencies in seconds, partitioned by provider and model.",
		Buckets: []float64{0.25, 0.5, 1, 2.5, 5, 10, 20, 30, 60, 120},
	},
	[]string{"provider", "model"},
)

func init() {
	prometheus.MustRegister(aiInvocationsTotal)
	prometheus.MustRegister(aiCostDollarsTotal)
	prometheus.MustRegister(aiTokensTotal)
	prometheus.MustRegister(aiRequestDuration)
}

// RecordAIInvocation records the counters and the latency of a single completed
// LLM call.
//
// provider is the provider kind and model the model name; both become labels.
// inputTokens and outputTokens are the prompt and completion token counts, each
// recorded only when above zero so that a call that reports none writes no
// zero-valued series. costMicros is the estimated cost in millionths of a US
// dollar and is added only when positive; pass 0 when pricing is unknown (e.g.
// local Ollama), which means a successful call can cost 0 and cost therefore
// cannot distinguish success from failure. elapsed is the wall-clock duration
// of the call and is observed on every call, because a request that failed
// still consumed the time it took and latency counted only over successes
// hides the degradation that precedes an outage. err is the sole determinant
// of the outcome label: nil records "success", any non-nil error records
// "error".
func RecordAIInvocation(provider, model string, inputTokens, outputTokens int, costMicros int64, elapsed time.Duration, err error) {
	outcome := aiOutcomeSuccess
	if err != nil {
		outcome = aiOutcomeError
	}
	aiInvocationsTotal.WithLabelValues(provider, model, outcome).Inc()
	if costMicros > 0 {
		dollars := float64(costMicros) / 1_000_000.0
		aiCostDollarsTotal.WithLabelValues(provider, model).Add(dollars)
	}
	if inputTokens > 0 {
		aiTokensTotal.WithLabelValues(aiTokenTypePrompt, model).Add(float64(inputTokens))
	}
	if outputTokens > 0 {
		aiTokensTotal.WithLabelValues(aiTokenTypeCompletion, model).Add(float64(outputTokens))
	}
	aiRequestDuration.WithLabelValues(provider, model).Observe(elapsed.Seconds())
}
