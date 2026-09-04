package obs

import (
	"errors"
	"testing"

	"github.com/prometheus/client_golang/prometheus/testutil"
)

// The outcome label values are asserted as literal strings rather than through
// the aiOutcomeSuccess / aiOutcomeError constants on purpose. Alert rules and
// dashboard queries match the literals; a test written against the constants
// would still pass if a constant were renamed, which is exactly the change that
// would leave the alerts matching a label value nothing emits any more.
const (
	wantOutcomeSuccess = "success"
	wantOutcomeError   = "error"
)

// The collectors are package-level vars registered in init(), so their values
// persist for the lifetime of the test binary and survive a repeated run of the
// same test function. Each case therefore uses its own provider/model/workspace
// triple and asserts the delta it caused rather than an absolute count, so that
// neither another test in this package nor `go test -count=N` can perturb it,
// and so the cases can run in parallel.

// invocations reads the invocation counter for one fully-qualified series.
// Reading a series that has never been incremented creates it at zero, which is
// what the "separate series" assertions rely on.
func invocations(provider, model, workspaceID, outcome string) float64 {
	return testutil.ToFloat64(aiInvocationsTotal.WithLabelValues(provider, model, workspaceID, outcome))
}

// cost reads the cumulative cost counter for one series. aiCostDollarsTotal has
// no outcome label, so three label values address it fully.
func cost(provider, model, workspaceID string) float64 {
	return testutil.ToFloat64(aiCostDollarsTotal.WithLabelValues(provider, model, workspaceID))
}

// TestRecordAIInvocationSuccessOutcome pins that a nil error records the
// invocation under the literal outcome "success" and leaves the "error" series
// for the same provider/model/workspace untouched.
func TestRecordAIInvocationSuccessOutcome(t *testing.T) {
	t.Parallel()

	const (
		provider  = "test-success-provider"
		model     = "test-success-model"
		workspace = "ws-success"
	)

	successBefore := invocations(provider, model, workspace, wantOutcomeSuccess)
	errorBefore := invocations(provider, model, workspace, wantOutcomeError)

	RecordAIInvocation(provider, model, workspace, 0, nil)

	if got := invocations(provider, model, workspace, wantOutcomeSuccess) - successBefore; got != 1 {
		t.Errorf("outcome=%q increment = %v, want 1", wantOutcomeSuccess, got)
	}
	if got := invocations(provider, model, workspace, wantOutcomeError) - errorBefore; got != 0 {
		t.Errorf("outcome=%q increment = %v, want 0: a successful call must not land on the error series", wantOutcomeError, got)
	}
}

// TestRecordAIInvocationErrorOutcome pins that a non-nil error records the
// invocation under the literal outcome "error" and leaves the "success" series
// for the same provider/model/workspace untouched.
func TestRecordAIInvocationErrorOutcome(t *testing.T) {
	t.Parallel()

	const (
		provider  = "test-error-provider"
		model     = "test-error-model"
		workspace = "ws-error"
	)

	errorBefore := invocations(provider, model, workspace, wantOutcomeError)
	successBefore := invocations(provider, model, workspace, wantOutcomeSuccess)

	RecordAIInvocation(provider, model, workspace, 0, errors.New("upstream refused the request"))

	if got := invocations(provider, model, workspace, wantOutcomeError) - errorBefore; got != 1 {
		t.Errorf("outcome=%q increment = %v, want 1", wantOutcomeError, got)
	}
	if got := invocations(provider, model, workspace, wantOutcomeSuccess) - successBefore; got != 0 {
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
		provider  = "test-cost-provider"
		model     = "test-cost-model"
		workspace = "ws-cost"
	)

	costBefore := cost(provider, model, workspace)
	successBefore := invocations(provider, model, workspace, wantOutcomeSuccess)

	RecordAIInvocation(provider, model, workspace, 0, nil)
	if got := cost(provider, model, workspace) - costBefore; got != 0 {
		t.Errorf("cost increment for costMicros=0 = %v, want 0", got)
	}

	// 2_500_000 micros is 2.5 dollars, exactly representable in float64, so the
	// comparison below needs no tolerance.
	RecordAIInvocation(provider, model, workspace, 2_500_000, nil)
	if got := cost(provider, model, workspace) - costBefore; got != 2.5 {
		t.Errorf("cost increment for 2_500_000 micros = %v, want 2.5", got)
	}

	if got := invocations(provider, model, workspace, wantOutcomeSuccess) - successBefore; got != 2 {
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
		provider  = "test-errorcost-provider"
		model     = "test-errorcost-model"
		workspace = "ws-errorcost"
	)

	errorBefore := invocations(provider, model, workspace, wantOutcomeError)
	costBefore := cost(provider, model, workspace)

	// 750_000 micros is 0.75 dollars, exactly representable in float64.
	RecordAIInvocation(provider, model, workspace, 750_000, errors.New("completion truncated"))

	if got := invocations(provider, model, workspace, wantOutcomeError) - errorBefore; got != 1 {
		t.Errorf("outcome=%q increment = %v, want 1", wantOutcomeError, got)
	}
	if got := cost(provider, model, workspace) - costBefore; got != 0.75 {
		t.Errorf("cost increment = %v, want 0.75: a failed call still spends what the provider billed", got)
	}
}
