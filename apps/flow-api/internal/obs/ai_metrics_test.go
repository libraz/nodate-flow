package obs

import (
	"errors"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"
	dto "github.com/prometheus/client_model/go"
)

// The outcome and token type label values are asserted as literal strings
// rather than through the aiOutcome* / aiTokenType* constants on purpose. Alert
// rules and dashboard queries match the literals; a test written against the
// constants would still pass if a constant were renamed, which is exactly the
// change that would leave the queries matching a label value nothing emits any
// more.
const (
	wantOutcomeSuccess = "success"
	wantOutcomeError   = "error"

	wantTokenTypePrompt     = "prompt"
	wantTokenTypeCompletion = "completion"
)

// The collectors are package-level vars registered in init(), so their values
// persist for the lifetime of the test binary and survive a repeated run of the
// same test function. Each case therefore uses its own provider/model pair and
// asserts the delta it caused rather than an absolute count, so that neither
// another test in this package nor `go test -count=N` can perturb it, and so
// the cases can run in parallel. The token counter is keyed on the model alone
// (with the type), so the model names in particular must be unique per case.

// invocations reads the invocation counter for one fully-qualified series.
// Reading a series that has never been incremented creates it at zero, which is
// what the "separate series" assertions rely on.
func invocations(provider, model, outcome string) float64 {
	return testutil.ToFloat64(aiInvocationsTotal.WithLabelValues(provider, model, outcome))
}

// cost reads the cumulative cost counter for one series. aiCostDollarsTotal has
// no outcome label, so two label values address it fully.
func cost(provider, model string) float64 {
	return testutil.ToFloat64(aiCostDollarsTotal.WithLabelValues(provider, model))
}

// tokens reads the token counter for one series. aiTokensTotal is keyed on the
// token type and the model, not the provider.
func tokens(tokenType, model string) float64 {
	return testutil.ToFloat64(aiTokensTotal.WithLabelValues(tokenType, model))
}

// durationStats reads the observation count and the sum of observed seconds for
// one series of the latency histogram. testutil.ToFloat64 understands only
// counters and gauges, so the histogram is read through the same dto
// representation the collector writes for the exposition format.
func durationStats(t *testing.T, provider, model string) (uint64, float64) {
	t.Helper()

	observer, err := aiRequestDuration.GetMetricWithLabelValues(provider, model)
	if err != nil {
		t.Fatalf("GetMetricWithLabelValues(%q, %q): %v", provider, model, err)
	}
	metric, ok := observer.(prometheus.Metric)
	if !ok {
		t.Fatalf("histogram observer for (%q, %q) does not implement prometheus.Metric", provider, model)
	}
	var out dto.Metric
	if err := metric.Write(&out); err != nil {
		t.Fatalf("writing histogram metric for (%q, %q): %v", provider, model, err)
	}
	return out.GetHistogram().GetSampleCount(), out.GetHistogram().GetSampleSum()
}

// TestRecordAIInvocationSuccessOutcome pins that a nil error records the
// invocation under the literal outcome "success" and leaves the "error" series
// for the same provider/model untouched.
func TestRecordAIInvocationSuccessOutcome(t *testing.T) {
	t.Parallel()

	const (
		provider = "test-success-provider"
		model    = "test-success-model"
	)

	successBefore := invocations(provider, model, wantOutcomeSuccess)
	errorBefore := invocations(provider, model, wantOutcomeError)

	RecordAIInvocation(provider, model, 0, 0, 0, time.Second, nil)

	if got := invocations(provider, model, wantOutcomeSuccess) - successBefore; got != 1 {
		t.Errorf("outcome=%q increment = %v, want 1", wantOutcomeSuccess, got)
	}
	if got := invocations(provider, model, wantOutcomeError) - errorBefore; got != 0 {
		t.Errorf("outcome=%q increment = %v, want 0: a successful call must not land on the error series", wantOutcomeError, got)
	}
}

// TestRecordAIInvocationErrorOutcome pins that a non-nil error records the
// invocation under the literal outcome "error" and leaves the "success" series
// for the same provider/model untouched.
func TestRecordAIInvocationErrorOutcome(t *testing.T) {
	t.Parallel()

	const (
		provider = "test-error-provider"
		model    = "test-error-model"
	)

	errorBefore := invocations(provider, model, wantOutcomeError)
	successBefore := invocations(provider, model, wantOutcomeSuccess)

	RecordAIInvocation(provider, model, 0, 0, 0, time.Second, errors.New("upstream refused the request"))

	if got := invocations(provider, model, wantOutcomeError) - errorBefore; got != 1 {
		t.Errorf("outcome=%q increment = %v, want 1", wantOutcomeError, got)
	}
	if got := invocations(provider, model, wantOutcomeSuccess) - successBefore; got != 0 {
		t.Errorf("outcome=%q increment = %v, want 0: a failed call must not land on the success series", wantOutcomeSuccess, got)
	}
}

// TestRecordAIInvocationCostThreshold pins that cost is added only for a
// positive costMicros. Zero is the documented value for providers with unknown
// pricing, so a zero-cost call must still be counted as an invocation while
// contributing nothing to the cost total.
func TestRecordAIInvocationCostThreshold(t *testing.T) {
	t.Parallel()

	const (
		provider = "test-cost-provider"
		model    = "test-cost-model"
	)

	costBefore := cost(provider, model)
	successBefore := invocations(provider, model, wantOutcomeSuccess)

	RecordAIInvocation(provider, model, 0, 0, 0, time.Second, nil)
	if got := cost(provider, model) - costBefore; got != 0 {
		t.Errorf("cost increment for costMicros=0 = %v, want 0", got)
	}

	// 2_500_000 micros is 2.5 dollars, exactly representable in float64, so the
	// comparison below needs no tolerance.
	RecordAIInvocation(provider, model, 0, 0, 2_500_000, time.Second, nil)
	if got := cost(provider, model) - costBefore; got != 2.5 {
		t.Errorf("cost increment for 2_500_000 micros = %v, want 2.5", got)
	}

	if got := invocations(provider, model, wantOutcomeSuccess) - successBefore; got != 2 {
		t.Errorf("outcome=%q increment = %v, want 2: both calls are invocations regardless of cost", wantOutcomeSuccess, got)
	}
}

// TestRecordAIInvocationErrorWithCost pins that a failed call still accrues
// cost. A provider bills for tokens it has already generated, so a response that
// is rejected downstream (a truncated or unparseable completion) arrives with a
// non-zero costMicros and a non-nil error. Cost and outcome are independent: the
// spend lands on aiCostDollarsTotal, which carries no outcome label.
func TestRecordAIInvocationErrorWithCost(t *testing.T) {
	t.Parallel()

	const (
		provider = "test-errorcost-provider"
		model    = "test-errorcost-model"
	)

	errorBefore := invocations(provider, model, wantOutcomeError)
	costBefore := cost(provider, model)

	// 750_000 micros is 0.75 dollars, exactly representable in float64.
	RecordAIInvocation(provider, model, 0, 0, 750_000, time.Second, errors.New("completion truncated"))

	if got := invocations(provider, model, wantOutcomeError) - errorBefore; got != 1 {
		t.Errorf("outcome=%q increment = %v, want 1", wantOutcomeError, got)
	}
	if got := cost(provider, model) - costBefore; got != 0.75 {
		t.Errorf("cost increment = %v, want 0.75: a failed call still spends what the provider billed", got)
	}
}

// TestRecordAIInvocationTokenTypes pins that prompt and completion counts land
// on their own type series and never on each other's. The dashboard breaks
// spend down by direction, and the two directions are priced differently, so a
// count credited to the wrong type would be wrong by more than its own value.
func TestRecordAIInvocationTokenTypes(t *testing.T) {
	t.Parallel()

	const (
		provider = "test-tokens-provider"
		model    = "test-tokens-model"
	)

	promptBefore := tokens(wantTokenTypePrompt, model)
	completionBefore := tokens(wantTokenTypeCompletion, model)

	RecordAIInvocation(provider, model, 7, 11, 0, time.Second, nil)

	if got := tokens(wantTokenTypePrompt, model) - promptBefore; got != 7 {
		t.Errorf("type=%q increment = %v, want 7", wantTokenTypePrompt, got)
	}
	if got := tokens(wantTokenTypeCompletion, model) - completionBefore; got != 11 {
		t.Errorf("type=%q increment = %v, want 11", wantTokenTypeCompletion, got)
	}
}

// TestRecordAIInvocationZeroTokensWritesNoSeries pins that a call reporting no
// tokens creates no token series at all. A failed call reports zero, and a
// zero-valued series is indistinguishable on a graph from a model that is
// genuinely idle, so it must not be created. The check deletes rather than
// reads: reading a counter through WithLabelValues would itself create the
// series it is asking about, and DeleteLabelValues reports whether one existed.
func TestRecordAIInvocationZeroTokensWritesNoSeries(t *testing.T) {
	t.Parallel()

	const (
		provider = "test-zerotokens-provider"
		model    = "test-zerotokens-model"
	)

	errorBefore := invocations(provider, model, wantOutcomeError)

	RecordAIInvocation(provider, model, 0, 0, 0, time.Second, errors.New("provider unreachable"))

	if aiTokensTotal.DeleteLabelValues(wantTokenTypePrompt, model) {
		t.Errorf("type=%q series exists for model %q, want none: a zero count must write no series", wantTokenTypePrompt, model)
	}
	if aiTokensTotal.DeleteLabelValues(wantTokenTypeCompletion, model) {
		t.Errorf("type=%q series exists for model %q, want none: a zero count must write no series", wantTokenTypeCompletion, model)
	}
	if got := invocations(provider, model, wantOutcomeError) - errorBefore; got != 1 {
		t.Errorf("outcome=%q increment = %v, want 1: the call is still an invocation", wantOutcomeError, got)
	}
}

// TestRecordAIInvocationDurationObservedOnEveryCall pins that the latency
// histogram observes the failed call as well as the successful one. A request
// that failed still consumed the time it took, and a latency series that counts
// only successes hides the degradation that precedes an outage.
func TestRecordAIInvocationDurationObservedOnEveryCall(t *testing.T) {
	t.Parallel()

	const (
		provider = "test-duration-provider"
		model    = "test-duration-model"
	)

	countBefore, sumBefore := durationStats(t, provider, model)

	// 250 ms and 1.5 s are exactly representable in float64 as seconds, so the
	// sum below needs no tolerance.
	RecordAIInvocation(provider, model, 0, 0, 0, 250*time.Millisecond, nil)
	RecordAIInvocation(provider, model, 0, 0, 0, 1500*time.Millisecond, errors.New("provider timed out"))

	countAfter, sumAfter := durationStats(t, provider, model)

	if got := countAfter - countBefore; got != 2 {
		t.Errorf("observation increment = %v, want 2: the failed call must be observed too", got)
	}
	if got := sumAfter - sumBefore; got != 1.75 {
		t.Errorf("observed seconds increment = %v, want 1.75", got)
	}
}
