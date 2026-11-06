package signaljudge

import (
	"context"
	"errors"
	"fmt"

	"github.com/libraz/nodate-flow/apps/flow-api/internal/ai"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/ai/providers"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/eventbus"
)

// RejectOutput records that the model did not produce a usable verdict.
// It uses the same SignalRejected payload shape as Apply's existing reject
// path, and deliberately does not persist a synthetic verdict on the signal.
func (a *Applier) RejectOutput(ctx context.Context, sig SignalRef, agent AgentRef, failure error) (*ApplyResult, error) {
	if a == nil {
		return nil, errors.New("signaljudge: nil applier")
	}
	if sig.InternalID <= 0 || sig.WorkspaceID == 0 || agent.InternalID == 0 {
		return nil, errors.New("signaljudge: invalid signal or agent ref")
	}
	if a.Bus == nil {
		return nil, errors.New("signaljudge: applier missing event bus")
	}
	if failure == nil {
		failure = providers.ErrResponseSchemaMismatch
	}
	signalInternalID := sig.InternalID
	if err := a.append(ctx, eventbus.Event{
		Type:                eventbus.SignalRejected,
		WorkspaceID:         sig.WorkspaceID,
		ActorAgentID:        agentInt64(agent),
		TriggeredBySignalID: &signalInternalID,
		Payload: map[string]any{
			"reason":          rejectVerdictInvalid,
			"validationError": signalJudgeFailureCode(failure),
			"action":          "",
			"confidence":      0.0,
		},
	}); err != nil {
		return nil, fmt.Errorf("signaljudge: append SignalRejected: %w", err)
	}
	return &ApplyResult{
		EventsAppended: 1,
		Skipped:        true,
		SkipReason:     rejectVerdictInvalid,
	}, nil
}

func signalJudgeFailureCode(err error) string {
	switch {
	case errors.Is(err, providers.ErrGenerationIncomplete):
		return "AI.RESPONSE.GENERATION_INCOMPLETE"
	case errors.Is(err, providers.ErrModelRefused):
		return "AI.RESPONSE.MODEL_REFUSED"
	case errors.Is(err, providers.ErrResponseInvalidJSON):
		return "AI.RESPONSE.INVALID_JSON"
	case errors.Is(err, providers.ErrResponseSchemaMismatch):
		return "AI.RESPONSE.SCHEMA_MISMATCH"
	case errors.Is(err, ai.ErrDailyBudgetExceeded):
		return "AI.BUDGET.DAILY_EXCEEDED"
	case errors.Is(err, ai.ErrAgentCostCapExceeded):
		return "AI.AGENT.COST_CAP_EXCEEDED"
	default:
		return "AI.INVOCATION.FAILED"
	}
}
