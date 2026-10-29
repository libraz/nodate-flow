package ai

import (
	"context"
	"database/sql"
	"log/slog"

	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/generated"
)

// PreFlight checks whether an agent has new work to process before
// invoking the LLM. It queries the events table for activity since
// the agent's last successful run. When no events exist, the agent
// tick is skipped to avoid wasting LLM tokens on idle workspaces.
type PreFlight struct {
	Queries *generated.Queries
}

// ShouldSkip returns true when the agent should skip its LLM call
// because no new activity has occurred since its last successful run.
// Returns (false, nil) on any error so the agent runs as a fallback.
func (p *PreFlight) ShouldSkip(ctx context.Context, workspaceID, agentID uint32) (bool, error) {
	lastRun, err := p.Queries.GetLastSuccessfulAgentRun(ctx, generated.GetLastSuccessfulAgentRunParams{
		WorkspaceID: workspaceID,
		AgentID:     agentID,
	})
	if err != nil {
		// No previous run or DB error — don't skip so the agent
		// executes on its first invocation and on transient failures.
		slog.WarnContext(ctx, "preflight: GetLastSuccessfulAgentRun failed, agent will run",
			"workspace_id", workspaceID, "agent_id", agentID, "error", err)
		return false, nil
	}

	// The agent's own run bookkeeping is excluded, otherwise every tick
	// would see the previous tick's events and never skip.
	hasActivity, err := p.Queries.HasNewActivityForAgent(ctx, generated.HasNewActivityForAgentParams{
		WorkspaceID:  workspaceID,
		OccurredAt:   lastRun,
		ActorAgentID: sql.NullInt32{Int32: int32(agentID), Valid: true}, //#nosec G115 -- ai_agents.id is INT UNSIGNED, fits int32 within realistic deployments
	})
	if err != nil {
		slog.WarnContext(ctx, "preflight: HasNewActivityForAgent failed, agent will run",
			"workspace_id", workspaceID, "agent_id", agentID, "error", err)
		return false, nil
	}

	return !hasActivity, nil
}
