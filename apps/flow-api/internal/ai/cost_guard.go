package ai

import (
	"context"
	"errors"
)

// DefaultDailyBudgetCents is the per-workspace daily cap when
// NF_FLOW_AI_DAILY_BUDGET_CENTS is unset. $100/day is generous for development
// and obvious in audit. The budget is per-day and set by the deployment, not
// editable from workspace settings.
const DefaultDailyBudgetCents int64 = 10000

// ErrDailyBudgetExceeded is returned by CostGuard.Check when the workspace
// has already spent more than its daily budget. Callers should map it to
// the AI.COST.GUARD_EXCEEDED error code at the HTTP boundary.
var ErrDailyBudgetExceeded = errors.New("ai: daily budget exceeded")

// BudgetReader returns the cost-cents already spent today for a workspace.
// The implementation in production wraps the sqlc query
// SumAiCostTodayForWorkspace; tests can supply a func adapter.
type BudgetReader interface {
	SumTodayCentsForWorkspace(ctx context.Context, workspaceID uint32) (int64, error)
}

// BudgetReaderFunc is an adapter to let plain functions satisfy BudgetReader.
type BudgetReaderFunc func(ctx context.Context, workspaceID uint32) (int64, error)

// SumTodayCentsForWorkspace implements BudgetReader.
func (f BudgetReaderFunc) SumTodayCentsForWorkspace(ctx context.Context, workspaceID uint32) (int64, error) {
	return f(ctx, workspaceID)
}

// CostGuard enforces the per-workspace daily LLM spend cap.
type CostGuard struct {
	Reader      BudgetReader
	DailyBudget int64
}

// NewCostGuard returns a guard configured with the supplied reader and a
// per-workspace daily budget. A zero or negative dailyBudget falls back to
// DefaultDailyBudgetCents.
func NewCostGuard(reader BudgetReader, dailyBudget int64) *CostGuard {
	if dailyBudget <= 0 {
		dailyBudget = DefaultDailyBudgetCents
	}
	return &CostGuard{Reader: reader, DailyBudget: dailyBudget}
}

// Check returns ErrDailyBudgetExceeded when the workspace's already-spent
// cost-cents reach or exceed the configured budget. A nil reader is treated
// as "no budget enforcement", so MVP deployments without metering still
// work.
func (g *CostGuard) Check(ctx context.Context, workspaceID uint32) error {
	if g == nil || g.Reader == nil {
		return nil
	}
	spent, err := g.Reader.SumTodayCentsForWorkspace(ctx, workspaceID)
	if err != nil {
		return err
	}
	if spent >= g.DailyBudget {
		return ErrDailyBudgetExceeded
	}
	return nil
}
