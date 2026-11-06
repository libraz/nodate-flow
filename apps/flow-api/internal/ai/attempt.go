package ai

import (
	"context"
	"time"

	"github.com/libraz/nodate-flow/apps/flow-api/internal/ai/providers"
)

// CompleteAndRecord runs one provider call and records its final validated
// outcome. The validator runs only after the response has explicit complete
// stop evidence. It must validate or decode the result without caching,
// appending, or applying it; callers may do those things only after this
// method returns nil error. The context is forwarded unchanged so invocation
// audience, agent/task attribution, and input-resource provenance survive.
//
// The method is exported so sibling AI caller packages can use the same
// fail-closed accounting boundary without rebuilding invocation context.
func (o *Orchestrator) CompleteAndRecord(
	ctx context.Context,
	workspaceID uint32,
	purpose string,
	provider providers.Provider,
	request providers.Request,
	validate func(*providers.Response) error,
) (*providers.Response, error) {
	if o == nil || provider == nil {
		return nil, ErrNoProvider
	}
	started := time.Now()
	response, outcomeErr := provider.Complete(ctx, request)
	elapsed := time.Since(started)
	inputTokens, outputTokens, costMicros := 0, 0, int64(0)
	if response != nil {
		inputTokens = response.InputTokens
		outputTokens = response.OutputTokens
		costMicros = response.EstimatedCostMicros()
	}
	if outcomeErr == nil {
		outcomeErr = providers.RequireComplete(response)
	}
	if outcomeErr == nil && validate != nil {
		outcomeErr = validate(response)
	}

	o.recordMetrics(string(provider.Kind()), request.Model, inputTokens, outputTokens, costMicros, elapsed, outcomeErr)
	if outcomeErr != nil {
		o.logFailure(ctx, workspaceID, purpose, request, outcomeErr, response)
		return response, outcomeErr
	}
	o.logSuccess(ctx, workspaceID, purpose, request, response)
	return response, nil
}
