package ai

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/libraz/nodate-flow/apps/flow-api/internal/ai/providers"
)

type onDemandTestProvider struct {
	started chan struct{}
	release chan struct{}
	calls   atomic.Int32
}

func (*onDemandTestProvider) Kind() providers.Kind { return providers.KindOpenAI }
func (*onDemandTestProvider) Name() string         { return "on-demand test" }
func (*onDemandTestProvider) Model() string        { return "test-model" }

func (p *onDemandTestProvider) Complete(ctx context.Context, _ providers.Request) (*providers.Response, error) {
	if p.calls.Add(1) == 1 {
		close(p.started)
	}
	select {
	case <-p.release:
		return &providers.Response{
			Text: `{"priority":"low","reason":"ready"}`, Termination: providers.TerminationComplete,
		}, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func TestGenerateOnDemandCoalescesAndCaches(t *testing.T) {
	t.Parallel()
	provider := &onDemandTestProvider{started: make(chan struct{}), release: make(chan struct{})}
	orchestrator := &Orchestrator{
		Resolver: ProviderResolverFunc(func(context.Context, uint32) (providers.Provider, error) {
			return provider, nil
		}),
		ProposalCache: NewProposalCache(time.Minute),
	}
	call := func(ctx context.Context, scope string) ([]byte, error) {
		return orchestrator.GenerateOnDemand(ctx, 12, "test", scope, "system", "prompt", priorityOutput, 64, nil)
	}
	leader := make(chan error, 1)
	go func() {
		_, err := call(t.Context(), "actor 42")
		leader <- err
	}()
	select {
	case <-provider.started:
	case <-time.After(time.Second):
		t.Fatal("the first call never reached the provider")
	}
	// A concurrent follower leaves on cancellation without making its
	// own upstream request, even while the first request is still open.
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	_, err := call(ctx, "actor 42")
	require.ErrorIs(t, err, context.Canceled)
	close(provider.release)
	require.NoError(t, <-leader)
	payload, err := call(t.Context(), "actor 42")
	require.NoError(t, err)
	require.JSONEq(t, `{"priority":"low","reason":"ready"}`, string(payload))
	require.EqualValues(t, 1, provider.calls.Load())

	_, err = call(t.Context(), "actor 43")
	require.NoError(t, err)
	require.EqualValues(t, 2, provider.calls.Load(), "another actor has a separate cache")
}

func TestGenerateOnDemandDoesNotCacheRejectedOutput(t *testing.T) {
	t.Parallel()
	provider := &onDemandTestProvider{started: make(chan struct{}), release: make(chan struct{})}
	close(provider.release)
	orchestrator := &Orchestrator{
		Resolver: ProviderResolverFunc(func(context.Context, uint32) (providers.Provider, error) {
			return provider, nil
		}),
		ProposalCache: NewProposalCache(time.Minute),
	}
	refused := errors.New("reject proposal")
	for range 2 {
		_, err := orchestrator.GenerateOnDemand(t.Context(), 12, "test", "actor", "system", "prompt",
			priorityOutput, 64, func([]byte) error { return refused })
		require.ErrorIs(t, err, refused)
	}
	require.EqualValues(t, 2, provider.calls.Load(), "one attempt per explicit request")
}
