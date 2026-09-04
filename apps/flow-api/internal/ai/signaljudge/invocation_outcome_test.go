package signaljudge

import (
	"context"
	"errors"
	"testing"

	"github.com/libraz/nodate-flow/apps/flow-api/internal/ai/providers"
)

// validVerdict parses and validates, so a response carrying it does not
// trigger the runner's single parse retry.
const validVerdict = `{"action":"noop","confidence":0.1,"reasoning_excerpt":"nothing actionable in this signal"}`

// metricSample is one call the runner made to its metrics hook.
type metricSample struct {
	model string
	cost  int64
	err   error
}

// newOutcomeRunner wires a runner around prov that records every metrics
// hook call and every invocation record.
func newOutcomeRunner(prov providers.Provider, samples *[]metricSample, logged *[]InvocationRecord) *Runner {
	return &Runner{
		Agents:   &fakeAgentLookup{snap: AgentSnapshot{AgentID: 4, WorkspaceID: 6}},
		Signals:  &fakeSignalLookup{snap: SignalSnapshot{SignalID: 2, WorkspaceID: 6, Kind: "manual"}},
		Resolver: &fakeResolver{provider: prov},
		Log:      func(_ context.Context, rec InvocationRecord) { *logged = append(*logged, rec) },
		OnInvocation: func(_, model, _ string, cost int64, err error) {
			*samples = append(*samples, metricSample{model: model, cost: cost, err: err})
		},
	}
}

// TestExecuteJudgeMetricsCarryTheOutcome pins the success/failure signal
// the judge hands its metrics hook. Cost is not a substitute: 0 also means
// "pricing unknown", which every local provider reports on a successful
// call.
func TestExecuteJudgeMetricsCarryTheOutcome(t *testing.T) {
	t.Parallel()

	t.Run("success reports no error", func(t *testing.T) {
		t.Parallel()
		prov := &recordingProvider{
			kind: providers.Kind("mock"),
			resp: &providers.Response{Text: validVerdict},
		}
		var samples []metricSample
		var logged []InvocationRecord
		r := newOutcomeRunner(prov, &samples, &logged)

		if _, err := r.ExecuteJudge(context.Background(), 6, 4, 2); err != nil {
			t.Fatalf("ExecuteJudge: %v", err)
		}
		if len(samples) != 1 {
			t.Fatalf("expected one metrics sample, got %+v", samples)
		}
		if samples[0].err != nil {
			t.Errorf("a successful call reported err = %v; the metric would label it an error", samples[0].err)
		}
	})

	t.Run("failure reports the provider error", func(t *testing.T) {
		t.Parallel()
		upstream := errors.New("upstream exploded")
		prov := &recordingProvider{kind: providers.Kind("mock"), err: upstream}
		var samples []metricSample
		var logged []InvocationRecord
		r := newOutcomeRunner(prov, &samples, &logged)

		if _, err := r.ExecuteJudge(context.Background(), 6, 4, 2); err == nil {
			t.Fatal("expected the provider error to surface")
		}
		if len(samples) != 1 {
			t.Fatalf("expected one metrics sample, got %+v", samples)
		}
		if !errors.Is(samples[0].err, upstream) {
			t.Errorf("hook err = %v, want %v", samples[0].err, upstream)
		}
	})
}

// TestExecuteJudgeCountsAFailedRetry covers the second attempt. A retry is
// a provider call that happened and was billed; when it fails, both the
// metric and ai_invocations have to show it, or a workspace whose model
// answers the first attempt unparseably and then errors looks — in the
// data — like it made one clean call.
func TestExecuteJudgeCountsAFailedRetry(t *testing.T) {
	t.Parallel()

	retryFailure := errors.New("retry exploded")
	prov := &recordingProvider{
		kind: providers.Kind("mock"),
		script: []providerAnswer{
			// Not a valid verdict, so the runner's single retry fires.
			{resp: &providers.Response{Text: "sorry, I cannot comply"}},
			{err: retryFailure},
		},
	}
	var samples []metricSample
	var logged []InvocationRecord
	r := newOutcomeRunner(prov, &samples, &logged)

	if _, err := r.ExecuteJudge(context.Background(), 6, 4, 2); err != nil {
		t.Fatalf("ExecuteJudge: %v", err)
	}
	if len(prov.reqs) != 2 {
		t.Fatalf("expected the parse-retry to make a second call, got %d", len(prov.reqs))
	}
	if len(samples) != 2 {
		t.Fatalf("expected one metrics sample per provider call, got %d: %+v", len(samples), samples)
	}
	if samples[0].err != nil {
		t.Errorf("the first attempt succeeded but reported err = %v", samples[0].err)
	}
	if !errors.Is(samples[1].err, retryFailure) {
		t.Errorf("retry sample err = %v, want %v", samples[1].err, retryFailure)
	}
	if samples[1].model == "" {
		t.Error("the failed retry must still name the model it was billed against")
	}
	if samples[1].cost != 0 {
		t.Errorf("a failed retry has no known cost, got %d", samples[1].cost)
	}
	if len(logged) != 2 {
		t.Fatalf("expected one ai_invocations record per provider call, got %d: %+v", len(logged), logged)
	}
	if logged[1].Status != "error" || logged[1].ErrorCode == "" {
		t.Errorf("the failed retry must be audited as an error; got %+v", logged[1])
	}
}

// TestExecuteJudgeCountsARetryThatAnsweredNothing covers the provider that
// breaks its own contract by returning neither a response nor an error.
// That is not a success, and recording it as a zero-cost one would put a
// call that produced no verdict in the same bucket as a local-provider
// call that produced a good one.
func TestExecuteJudgeCountsARetryThatAnsweredNothing(t *testing.T) {
	t.Parallel()

	prov := &recordingProvider{
		kind: providers.Kind("mock"),
		script: []providerAnswer{
			{resp: &providers.Response{Text: "sorry, I cannot comply"}},
			{}, // nil response, nil error
		},
	}
	var samples []metricSample
	var logged []InvocationRecord
	r := newOutcomeRunner(prov, &samples, &logged)

	if _, err := r.ExecuteJudge(context.Background(), 6, 4, 2); err != nil {
		t.Fatalf("ExecuteJudge: %v", err)
	}
	if len(samples) != 2 {
		t.Fatalf("expected one metrics sample per provider call, got %d: %+v", len(samples), samples)
	}
	if samples[1].err == nil {
		t.Error("a retry that returned no response was recorded as a success")
	}
	if len(logged) != 2 || logged[1].Status != "error" {
		t.Errorf("the empty retry must be audited as an error; got %+v", logged)
	}
}
