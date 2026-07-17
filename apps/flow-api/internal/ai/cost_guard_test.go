package ai

import (
	"context"
	"errors"
	"testing"

	"github.com/nodate-flow/nodate-flow/apps/flow-api/internal/ai/providers"
)

// TestCostGuardFiresOnConservativeUnknownModelSpend proves that an unknown
// model (e.g. an openai_compat route) now contributes a nonzero cost, and that
// once that spend reaches the budget the guard rejects further calls.
func TestCostGuardFiresOnConservativeUnknownModelSpend(t *testing.T) {
	t.Parallel()

	// An unknown model must produce a nonzero cost that can be metered.
	micros := providers.EstimateCostMicrosUSD("litellm/mystery-model", 1_000_000, 1_000_000)
	if micros <= 0 {
		t.Fatalf("unknown model cost = %d micros, want nonzero", micros)
	}
	spentCents := micros / 10_000
	if spentCents <= 0 {
		t.Fatalf("unknown model spend = %d cents, want nonzero", spentCents)
	}

	guard := NewCostGuard(BudgetReaderFunc(func(context.Context, uint32) (int64, error) {
		return spentCents, nil
	}), spentCents-1) // budget just under the already-spent amount

	if err := guard.Check(context.Background(), 1); !errors.Is(err, ErrDailyBudgetExceeded) {
		t.Fatalf("guard.Check = %v, want ErrDailyBudgetExceeded", err)
	}
}

// TestCostGuardAllowsSpendUnderBudget confirms the guard stays open when the
// metered spend is below the configured cap.
func TestCostGuardAllowsSpendUnderBudget(t *testing.T) {
	t.Parallel()

	guard := NewCostGuard(BudgetReaderFunc(func(context.Context, uint32) (int64, error) {
		return 10, nil
	}), 100)

	if err := guard.Check(context.Background(), 1); err != nil {
		t.Fatalf("guard.Check = %v, want nil", err)
	}
}

// TestCostGuardEffectiveBudget proves that the daily budget supplied to
// NewCostGuard (sourced from NF_FLOW_AI_DAILY_BUDGET_CENTS at startup)
// becomes the effective spend threshold, and that a zero value falls back
// to DefaultDailyBudgetCents.
func TestCostGuardEffectiveBudget(t *testing.T) {
	t.Parallel()

	reader := BudgetReaderFunc(func(context.Context, uint32) (int64, error) {
		return 5000, nil
	})

	// A tighter override than the default must trip at the overridden cap:
	// 5000 spent >= 5000 budget.
	tight := NewCostGuard(reader, 5000)
	if tight.DailyBudget != 5000 {
		t.Fatalf("override DailyBudget = %d, want 5000", tight.DailyBudget)
	}
	if err := tight.Check(context.Background(), 1); !errors.Is(err, ErrDailyBudgetExceeded) {
		t.Fatalf("tight.Check = %v, want ErrDailyBudgetExceeded", err)
	}

	// A zero (unset env) budget falls back to the built-in default, under
	// which the same 5000-cent spend is still allowed.
	fallback := NewCostGuard(reader, 0)
	if fallback.DailyBudget != DefaultDailyBudgetCents {
		t.Fatalf("fallback DailyBudget = %d, want %d", fallback.DailyBudget, DefaultDailyBudgetCents)
	}
	if err := fallback.Check(context.Background(), 1); err != nil {
		t.Fatalf("fallback.Check = %v, want nil", err)
	}
}
