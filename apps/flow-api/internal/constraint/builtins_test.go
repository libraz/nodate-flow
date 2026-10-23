package constraint

import (
	"errors"
	"testing"
)

func intPtr(i int) *int { return &i }

func TestEvaluate_TimeDueAfter(t *testing.T) {
	c := mustParse(t, `{"op":"time.due_after","arg":"2026-01-01"}`)
	ok, _ := Evaluate(c, Facts{DueOn: date("2026-04-10")})
	if !ok {
		t.Fatal("expected true")
	}
	ok, _ = Evaluate(c, Facts{DueOn: date("2025-12-01")})
	if ok {
		t.Fatal("expected false")
	}
}

func TestEvaluate_DepOpenAtMost(t *testing.T) {
	c := Constraint{Op: OpDepOpenAtMost, Max: intPtr(1)}
	ok, _ := Evaluate(c, Facts{DependencyStates: map[string]string{
		"a": "done", "b": "waiting",
	}})
	if !ok {
		t.Fatal("expected true (1 open ≤ 1)")
	}
	ok, _ = Evaluate(c, Facts{DependencyStates: map[string]string{
		"a": "open", "b": "waiting",
	}})
	if ok {
		t.Fatal("expected false (2 open > 1)")
	}
	// cancelled counts as closed.
	ok, _ = Evaluate(c, Facts{DependencyStates: map[string]string{
		"a": "cancelled", "b": "waiting",
	}})
	if !ok {
		t.Fatal("cancelled should count as closed")
	}
}

func TestParse_DepOpenAtMostRequiresMax(t *testing.T) {
	if _, err := Parse([]byte(`{"op":"dependency.open_at_most"}`)); err == nil {
		t.Fatal("expected error")
	}
}

func TestEvaluate_Signal(t *testing.T) {
	c := mustParse(t, `{"op":"signal.received","arg":"github.pr.merged"}`)
	ok, _ := Evaluate(c, Facts{SignalsReceived: map[string]bool{"github.pr.merged": true}})
	if !ok {
		t.Fatal("expected true")
	}
	ok, _ = Evaluate(c, Facts{})
	if ok {
		t.Fatal("expected false")
	}
}

// TestParse_RefusesOpsWithNoFactSource pins that approval and CI-status
// constraints are refused at create: nothing records the fact either
// would read, so a stored one could only ever evaluate false.
func TestParse_RefusesOpsWithNoFactSource(t *testing.T) {
	for _, raw := range []string{
		`{"op":"approval.granted","arg":"reviewer"}`,
		`{"op":"ci.status_is","arg":"success"}`,
	} {
		_, err := Parse([]byte(raw))
		var pe *ParseError
		if !errors.As(err, &pe) || pe.Code != CodeUnsupportedOperator {
			t.Fatalf("Parse(%s) err = %v, want %s", raw, err, CodeUnsupportedOperator)
		}
	}
}
