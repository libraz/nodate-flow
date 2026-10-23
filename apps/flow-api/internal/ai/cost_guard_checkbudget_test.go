package ai

import (
	"context"
	"errors"
	"testing"
)

type errChecker struct{ err error }

func (c errChecker) Check(context.Context, uint32) error { return c.err }

func TestCheckBudgetCapHitOnlyForBudgetExceeded(t *testing.T) {
	t.Parallel()
	readErr := errors.New("db unavailable")
	cases := []struct {
		name    string
		g       BudgetChecker
		capHit  bool
		wantErr error
	}{
		{"nil guard", nil, false, nil},
		{"under budget", errChecker{}, false, nil},
		{"exceeded", errChecker{ErrDailyBudgetExceeded}, true, ErrDailyBudgetExceeded},
		{"read failure", errChecker{readErr}, false, readErr},
	}
	for _, tc := range cases {
		capHit, err := CheckBudget(context.Background(), tc.g, 1)
		if capHit != tc.capHit || !errors.Is(err, tc.wantErr) {
			t.Errorf("%s: capHit=%v err=%v, want capHit=%v err=%v", tc.name, capHit, err, tc.capHit, tc.wantErr)
		}
	}
}
