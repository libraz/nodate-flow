package ai

import (
	"context"

	"github.com/nodate-flow/nodate-flow/apps/api/internal/db/generated"
)

// PreFlight checks whether an agent has new work to process before
// invoking the LLM. It queries the events table for activity since
// the agent's last successful run. When no events exist, the agent
// tick is skipped to avoid wasting LLM tokens on idle workspaces.
type PreFlight struct {
	Queries *generated.Queries
}

// ShouldSkip returns true when the agent should skip its LLM call
// because no new events have occurred since its last successful run.
// Returns (false, nil) on any error so the agent runs as a fallback.
func (p *PreFlight) ShouldSkip(ctx context.Context, workspaceID, agentID uint32) (bool, error) {
	lastRun, err := p.Queries.GetLastSuccessfulAgentRun(ctx, generated.GetLastSuccessfulAgentRunParams{
		WorkspaceID: workspaceID,
		AgentID:     agentID,
	})
	if err != nil {
		// No previous run or DB error — don't skip so the agent
		// executes on its first invocation and on transient failures.
		return false, nil
	}

	hasEvents, err := p.Queries.HasRecentEventsForWorkspace(ctx, generated.HasRecentEventsForWorkspaceParams{
		WorkspaceID: workspaceID,
		OccurredAt:  lastRun,
	})
	if err != nil {
		return false, nil
	}

	return !hasEvents, nil
}
