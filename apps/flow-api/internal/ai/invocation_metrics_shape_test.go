package ai

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/libraz/nodate-flow/apps/flow-api/internal/ai/providers"
)

// providerDelay is long enough that a hook reporting a duration it did not
// measure — a zero value, or a timer started after the call returned —
// fails the lower-bound assertions below, while staying short enough not
// to slow the package down.
const providerDelay = 5 * time.Millisecond

// TestInvocationMetricsCarryTokensAndLatency pins the two quantities the
// metrics hook exists to report besides cost. Token counts must be the
// ones the response itself reported, so the counter and the ai_invocations
// row cannot disagree about the same call; and the duration must be
// reported on the failure path too, since a provider that is timing out is
// exactly the one whose latency an operator goes looking for, and a
// failure recorded without a duration is a stall that leaves no trace in
// the histogram.
func TestInvocationMetricsCarryTokensAndLatency(t *testing.T) {
	t.Parallel()

	t.Run("success reports the response's own counts", func(t *testing.T) {
		t.Parallel()

		const (
			wantIn  = 137
			wantOut = 24
		)
		prov := &labelProvider{
			model: "claude-sonnet-4-6",
			delay: providerDelay,
			resp: &providers.Response{
				Model:        "claude-sonnet-4-6",
				Text:         `[{"title":"t","description":"d","priority":"low"}]`,
				InputTokens:  wantIn,
				OutputTokens: wantOut,
			},
		}
		var got []sample
		o := &Orchestrator{
			Resolver: ProviderResolverFunc(func(context.Context, uint32) (providers.Provider, error) {
				return prov, nil
			}),
			OnInvocation: func(provider, model string, inTok, outTok int, cost int64, elapsed time.Duration, err error) {
				got = append(got, sample{provider, model, inTok, outTok, cost, elapsed, err})
			},
		}

		if _, err := o.ProposeTasksFrom(context.Background(), 42, "ship the thing"); err != nil {
			t.Fatalf("ProposeTasksFrom: %v", err)
		}
		if len(got) != 1 {
			t.Fatalf("expected exactly one metrics sample, got %d: %+v", len(got), got)
		}
		s := got[0]
		if s.inTok != wantIn || s.outTok != wantOut {
			t.Errorf("token counts = (%d, %d), want the response's (%d, %d)", s.inTok, s.outTok, wantIn, wantOut)
		}
		if s.elapsed < providerDelay {
			t.Errorf("elapsed = %v, want at least the %v the provider took", s.elapsed, providerDelay)
		}
	})

	t.Run("failure reports no tokens but still reports the time", func(t *testing.T) {
		t.Parallel()

		prov := &labelProvider{
			model: "claude-sonnet-4-6",
			delay: providerDelay,
			err:   errors.New("upstream exploded"),
		}
		var got []sample
		o := &Orchestrator{
			Resolver: ProviderResolverFunc(func(context.Context, uint32) (providers.Provider, error) {
				return prov, nil
			}),
			OnInvocation: func(provider, model string, inTok, outTok int, cost int64, elapsed time.Duration, err error) {
				got = append(got, sample{provider, model, inTok, outTok, cost, elapsed, err})
			},
		}

		if _, err := o.ProposeTasksFrom(context.Background(), 42, "ship the thing"); err == nil {
			t.Fatal("expected the provider error to surface")
		}
		if len(got) != 1 {
			t.Fatalf("expected exactly one metrics sample, got %d: %+v", len(got), got)
		}
		s := got[0]
		if s.inTok != 0 || s.outTok != 0 {
			t.Errorf("token counts = (%d, %d); a failed call has no response to count", s.inTok, s.outTok)
		}
		if s.elapsed < providerDelay {
			t.Errorf("elapsed = %v, want at least the %v the failed call took", s.elapsed, providerDelay)
		}
	})
}
