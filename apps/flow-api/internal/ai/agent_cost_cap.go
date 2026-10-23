package ai

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/libraz/nodate-flow/apps/flow-api/internal/ai/agentguard"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/generated"
)

// ErrAgentCostCapExceeded is returned when an agent's month-to-date spend
// has reached its ai_agents.monthly_cost_cap_cents. Callers map it to
// WS.TASK_AGENT.COST_CAP_EXCEEDED at the HTTP boundary.
var ErrAgentCostCapExceeded = errors.New("ai: agent monthly cost cap exceeded")

// AgentCostCapReader is the slice of the sqlc Querier the monthly cap
// check needs. *generated.Queries satisfies it.
type AgentCostCapReader interface {
	GetAgentGuardSnapshot(ctx context.Context, id uint32) (generated.GetAgentGuardSnapshotRow, error)
	SumAiCostForAgentSince(ctx context.Context, arg generated.SumAiCostForAgentSinceParams) (int64, error)
}

// AgentMonthStart returns the start of now's UTC calendar month, the
// window the monthly agent cap is counted over.
func AgentMonthStart(now time.Time) time.Time {
	u := now.UTC()
	return time.Date(u.Year(), u.Month(), 1, 0, 0, 0, 0, time.UTC)
}

// CheckAgentCostCap returns ErrAgentCostCapExceeded once agentID's spend
// since [AgentMonthStart] reaches its monthly cap, through the same
// [agentguard.Decide] rule the MCP dispatch guard applies. Every
// agent-initiated LLM call (scheduled tick, judge run, manual trigger)
// consults it before billing a provider. An agent with no cap passes.
func CheckAgentCostCap(ctx context.Context, q AgentCostCapReader, agentID uint32, now time.Time) error {
	snap, err := q.GetAgentGuardSnapshot(ctx, agentID)
	if err != nil {
		return err
	}
	if !snap.MonthlyCostCapCents.Valid {
		return nil
	}
	spent, err := q.SumAiCostForAgentSince(ctx, generated.SumAiCostForAgentSinceParams{
		AgentID:   sql.NullInt32{Int32: int32(agentID), Valid: true}, //#nosec G115 -- ai_agents.id is INT UNSIGNED, fits int32 within realistic deployments
		InvokedAt: AgentMonthStart(now),
	})
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	costCap := int64(snap.MonthlyCostCapCents.Int32)
	// Access rules (enabled / paused) are the caller's own check against
	// the row it already loaded; only the cost rules are asked here.
	d := agentguard.Decide(agentguard.Agent{Enabled: true, MonthlyCostCapCents: &costCap},
		agentguard.Request{SpentCentsMonth: spent})
	if d.Cause == agentguard.CauseCostCapExhausted || d.Cause == agentguard.CauseCostCapWouldExceed {
		return ErrAgentCostCapExceeded
	}
	return nil
}
