package ai

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
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

// TestCostGuardConcurrentOverspendBoundedBySafetyMargin exercises the
// check-then-spend race directly: N goroutines all call Check before any of
// them records its cost, so every one observes the same pre-race spend. The
// safety margin must absorb the resulting overrun — total spend stays within
// the full configured budget (one margin's worth past the effective cap),
// not N x per-call cost past the budget.
func TestCostGuardConcurrentOverspendBoundedBySafetyMargin(t *testing.T) {
	t.Parallel()

	const (
		budget  int64 = 10000
		callers int   = 50
		perCall int64 = 10 // callers x perCall == the reserved 5% margin
	)

	var spent atomic.Int64
	guard := NewCostGuard(BudgetReaderFunc(func(context.Context, uint32) (int64, error) {
		return spent.Load(), nil
	}), budget)

	effective := guard.EffectiveBudget()
	if margin := budget - effective; int64(callers)*perCall > margin {
		t.Fatalf("test setup: callers x perCall = %d exceeds margin %d", int64(callers)*perCall, margin)
	}

	// Worst case: the workspace is one cent short of the effective cap when
	// the burst arrives.
	spent.Store(effective - 1)

	// Phase 1: all callers check concurrently, none has recorded cost yet.
	results := make([]error, callers)
	var wg sync.WaitGroup
	for i := range callers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			results[i] = guard.Check(context.Background(), 1)
		}()
	}
	wg.Wait()

	// Phase 2: every caller that passed the check settles its real cost.
	allowed := 0
	for _, err := range results {
		if err == nil {
			allowed++
			spent.Add(perCall)
		} else if !errors.Is(err, ErrDailyBudgetExceeded) {
			t.Fatalf("guard.Check = %v, want nil or ErrDailyBudgetExceeded", err)
		}
	}
	if allowed == 0 {
		t.Fatal("no caller passed the guard; race scenario was not exercised")
	}

	if total := spent.Load(); total > budget {
		t.Fatalf("total spend %d exceeds configured budget %d (overspend not bounded by safety margin)", total, budget)
	}

	// The guard must now be closed for the next caller.
	if err := guard.Check(context.Background(), 1); !errors.Is(err, ErrDailyBudgetExceeded) {
		t.Fatalf("post-burst guard.Check = %v, want ErrDailyBudgetExceeded", err)
	}
}

// TestCostGuardEffectiveBudgetFloor proves a tiny positive budget is not
// rounded down to zero (which would mean "always allow").
func TestCostGuardEffectiveBudgetFloor(t *testing.T) {
	t.Parallel()

	guard := NewCostGuard(BudgetReaderFunc(func(context.Context, uint32) (int64, error) {
		return 1, nil
	}), 1)
	if eff := guard.EffectiveBudget(); eff != 1 {
		t.Fatalf("EffectiveBudget = %d, want 1", eff)
	}
	if err := guard.Check(context.Background(), 1); !errors.Is(err, ErrDailyBudgetExceeded) {
		t.Fatalf("guard.Check = %v, want ErrDailyBudgetExceeded", err)
	}
}
