package errormodel

import (
	"errors"
	"fmt"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/libraz/nodate-flow/apps/flow-api/internal/ai"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/ai/nlcommand"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/ai/nlconstraint"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/ai/nlquery"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/ai/providers"
	apierrors "github.com/libraz/nodate-flow/apps/flow-api/internal/errors"
	"github.com/libraz/nodate-flow/packages/go-shared/apierr"
)

// TestAIFailureMapsEachFailureOnce holds every AI-layer failure to the one
// code every surface answers with. Each error is wrapped the way a caller
// receives it, so a mapping that only matched the bare sentinel fails.
func TestAIFailureMapsEachFailureOnce(t *testing.T) {
	t.Parallel()

	cases := []struct {
		err  error
		want *apierr.Spec
	}{
		{ai.ErrNoProvider, apierrors.AiProviderNotConfigured},
		{providers.ErrNoEnabledProvider, apierrors.AiProviderNotConfigured},
		{ai.ErrDailyBudgetExceeded, apierrors.AiCostGuardExceeded},
		{nlquery.ErrBudgetExceeded, apierrors.AiCostGuardExceeded},
		{nlconstraint.ErrBudgetExceeded, apierrors.AiCostGuardExceeded},
		{nlcommand.ErrBudgetExceeded, apierrors.AiCostGuardExceeded},
		{ai.ErrAgentCostCapExceeded, apierrors.WsTaskAgentCostCapExceeded},
		{ai.ErrAgentPaused, apierrors.AiAgentPaused},
		{nlquery.ErrUnparseable, apierrors.AiNlQueryUnparseable},
		{ai.ErrParse, apierrors.AiResponseInvalidJson},
		{nlconstraint.ErrUnparseable, apierrors.AiResponseInvalidJson},
		{nlcommand.ErrUnresolvable, apierrors.AiResponseInvalidJson},
		{providers.ErrResponseInvalidJSON, apierrors.AiResponseInvalidJson},
		{providers.ErrResponseSchemaMismatch, apierrors.AiResponseSchemaMismatch},
		{providers.ErrUpstreamTimeout, apierrors.AiProviderUpstreamTimeout},
		{providers.ErrUpstreamAuthRejected, apierrors.AiProviderUpstreamAuthRejected},
		{providers.ErrUpstreamRequestRejected, apierrors.AiProviderUpstreamRequestRejected},
		{providers.ErrKeyDecryptFailed, apierrors.AiProviderKeyDecryptFailed},
		{providers.ErrBaseURLDestinationNotAllowed, apierrors.AiProviderBaseUrlNotAllowed},
		{providers.ErrInvalidBaseURL, apierrors.AiProviderBaseUrlNotAllowed},
		{providers.ErrBaseURLNotAllowed, apierrors.AiProviderNotConfigured},
		{providers.ErrUnknownKind, apierrors.AiProviderNotConfigured},
		{providers.ErrMissingKey, apierrors.AiProviderNotConfigured},
		{providers.ErrUpstreamUnreachable, apierrors.AiProviderUpstreamUnreachable},
		{errors.New("something else"), apierrors.AiProviderUpstreamUnreachable},
	}
	for _, tc := range cases {
		t.Run(tc.err.Error(), func(t *testing.T) {
			t.Parallel()
			spec, retryAfter := AIFailure(fmt.Errorf("caller: %w", tc.err))
			require.Equal(t, tc.want.Code, spec.Code)
			require.Empty(t, retryAfter)
		})
	}
}

// TestAIFailureCarriesRetryAfter pins that a rate-limited provider keeps
// its Retry-After through the mapping, however deep it is wrapped.
func TestAIFailureCarriesRetryAfter(t *testing.T) {
	t.Parallel()

	err := fmt.Errorf("ai: complete: %w", &providers.UpstreamError{
		Sentinel:   providers.ErrUpstreamRateLimited,
		Status:     429,
		RetryAfter: "17",
	})
	spec, retryAfter := AIFailure(err)
	require.Equal(t, apierrors.AiProviderUpstreamRateLimited.Code, spec.Code)
	require.Equal(t, "17", retryAfter)
}

// TestPageGenerationFailureMatchesTheOtherAISurfaces holds page generation,
// on REST and MCP alike, to the shared codes: only an unreachable provider
// takes the page-specific code, and a spent budget or an empty model
// answer must not read as a retryable upstream outage.
func TestPageGenerationFailureMatchesTheOtherAISurfaces(t *testing.T) {
	t.Parallel()

	cases := []struct {
		err  error
		want *apierr.Spec
	}{
		{ai.ErrDailyBudgetExceeded, apierrors.AiCostGuardExceeded},
		{ai.ErrParse, apierrors.AiResponseInvalidJson},
		{ai.ErrNoProvider, apierrors.AiProviderNotConfigured},
		{providers.ErrNoEnabledProvider, apierrors.AiProviderNotConfigured},
		{providers.ErrKeyDecryptFailed, apierrors.AiProviderKeyDecryptFailed},
		{providers.ErrUpstreamUnreachable, apierrors.PageGenerationUpstreamUnavailable},
	}
	for _, tc := range cases {
		t.Run(tc.err.Error(), func(t *testing.T) {
			t.Parallel()
			spec, _ := PageGenerationFailure(fmt.Errorf("ai: generate page: %w", tc.err))
			require.Equal(t, tc.want.Code, spec.Code)
		})
	}
}
