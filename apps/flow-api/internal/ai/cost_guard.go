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

// budgetSafetyMarginPercent scales the configured daily budget down to the
// effective enforcement threshold. The guard is check-then-spend: every call
// reads the already-recorded spend and only records its own cost after the
// provider responds, so N concurrent calls can all observe spend below the
// cap and pass. Enforcing at 95% reserves the remaining 5% to absorb that
// race window: as long as (parallelism x worst per-call cost) stays within
// the reserved margin — which per-call MaxTokens and the outer rate limits
// bound — total spend never exceeds the full configured budget. Mirrors the
// margin applied by agentguard for monthly agent caps.
const budgetSafetyMarginPercent int64 = 95

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

// EffectiveBudget returns the enforced spend threshold: DailyBudget scaled
// by the concurrency safety margin, floored at 1 cent so a positive budget
// never degrades into "always allow".
func (g *CostGuard) EffectiveBudget() int64 {
	effective := g.DailyBudget * budgetSafetyMarginPercent / 100
	if effective <= 0 && g.DailyBudget > 0 {
		effective = 1
	}
	return effective
}

// Check returns ErrDailyBudgetExceeded when the workspace's already-spent
// cost-cents reach or exceed the effective budget (the configured budget
// minus the concurrency safety margin — see budgetSafetyMarginPercent). A
// nil reader is treated as "no budget enforcement", so MVP deployments
// without metering still work.
func (g *CostGuard) Check(ctx context.Context, workspaceID uint32) error {
	if g == nil || g.Reader == nil {
		return nil
	}
	spent, err := g.Reader.SumTodayCentsForWorkspace(ctx, workspaceID)
	if err != nil {
		return err
	}
	if spent >= g.EffectiveBudget() {
		return ErrDailyBudgetExceeded
	}
	return nil
}
