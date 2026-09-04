package ai

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/libraz/nodate-flow/apps/flow-api/internal/ai/providers"
)

// TestInvocationMetricsCarryTheOutcome pins the success/failure signal the
// metrics hook is given. Cost cannot stand in for it: a hook argument of 0
// also means "pricing unknown", which is what every local-provider call
// reports, so a panel built on cost alone counts successful local calls as
// failures and cannot show an error rate at all.
func TestInvocationMetricsCarryTheOutcome(t *testing.T) {
	t.Parallel()

	upstream := errors.New("upstream exploded")

	cases := []struct {
		name    string
		prov    *labelProvider
		wantErr error
	}{
		{
			name: "success reports no error",
			prov: &labelProvider{
				model: "claude-sonnet-4-6",
				resp: &providers.Response{
					Model: "claude-sonnet-4-6",
					Text:  `[{"title":"t","description":"d","priority":"low"}]`,
				},
			},
			wantErr: nil,
		},
		{
			name:    "failure reports the provider error",
			prov:    &labelProvider{model: "claude-sonnet-4-6", err: upstream},
			wantErr: upstream,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			var got []sample
			o := &Orchestrator{
				Resolver: ProviderResolverFunc(func(context.Context, uint32) (providers.Provider, error) {
					return tc.prov, nil
				}),
				OnInvocation: func(provider, model string, inTok, outTok int, cost int64, elapsed time.Duration, err error) {
					got = append(got, sample{provider, model, inTok, outTok, cost, elapsed, err})
				},
			}
			_, callErr := o.ProposeTasksFrom(context.Background(), 42, "ship the thing")
			if tc.wantErr == nil && callErr != nil {
				t.Fatalf("ProposeTasksFrom: %v", callErr)
			}
			if tc.wantErr != nil && callErr == nil {
				t.Fatal("expected the provider error to surface")
			}
			if len(got) != 1 {
				t.Fatalf("expected exactly one metrics sample, got %d: %+v", len(got), got)
			}
			switch {
			case tc.wantErr == nil && got[0].err != nil:
				t.Errorf("a successful call reported err = %v; the metric would label it an error", got[0].err)
			case tc.wantErr != nil && got[0].err == nil:
				t.Error("a failed call reported err = nil; the failure is invisible in the metric")
			case tc.wantErr != nil && !errors.Is(got[0].err, tc.wantErr):
				t.Errorf("hook err = %v, want %v", got[0].err, tc.wantErr)
			}
		})
	}
}
