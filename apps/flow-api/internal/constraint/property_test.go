package constraint

import (
	"testing"
	"time"
)

// Property tests. Go has no fast-check; instead we
// enumerate a small, deterministic fact matrix and assert that
// every algebraic law we care about holds across every (fact,
// primitive) pair.

func factMatrix() []Facts {
	d1 := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	d2 := time.Date(2026, 12, 31, 0, 0, 0, 0, time.UTC)
	return []Facts{
		{},
		{DueOn: &d1},
		{DueOn: &d2},
		{DependencyStates: map[string]string{"a": "done"}},
		{DependencyStates: map[string]string{"a": "waiting"}},
		{ActorRoles: map[string]bool{"reviewer": true}},
		{SignalsReceived: map[string]bool{"github.pr.merged": true}},
		{Approvals: map[string]bool{"owner": true}},
		{CIStatus: "success"},
		{CIStatus: "failure"},
	}
}

func primitives() []Constraint {
	return []Constraint{
		{Op: OpTimeDueBefore, Arg: "2026-06-01"},
		{Op: OpTimeDueAfter, Arg: "2026-06-01"},
		{Op: OpDepAllDone, TaskIDs: []string{"a"}},
		{Op: OpActorHasRole, Arg: "reviewer"},
		{Op: OpSignalReceived, Arg: "github.pr.merged"},
		{Op: OpApprovalGranted, Arg: "owner"},
		{Op: OpCIStatusIs, Arg: "success"},
	}
}

func mustEval(t *testing.T, c Constraint, f Facts) bool {
	t.Helper()
	v, err := Evaluate(c, f)
	if err != nil {
		t.Fatalf("eval: %v", err)
	}
	return v
}

func TestProperty_Idempotence(t *testing.T) {
	for _, f := range factMatrix() {
		for _, p := range primitives() {
			a := mustEval(t, p, f)
			b := mustEval(t, p, f)
			if a != b {
				t.Fatalf("non-idempotent eval: %v on %+v", p.Op, f)
			}
		}
	}
}

func TestProperty_DoubleNegation(t *testing.T) {
	for _, f := range factMatrix() {
		for _, p := range primitives() {
			once := Constraint{Op: OpNot, Term: &p}
			twice := Constraint{Op: OpNot, Term: &once}
			if mustEval(t, twice, f) != mustEval(t, p, f) {
				t.Fatalf("¬¬p ≠ p for %v on %+v", p.Op, f)
			}
		}
	}
}

func TestProperty_DeMorgan(t *testing.T) {
	ps := primitives()
	for _, f := range factMatrix() {
		for i := 0; i < len(ps); i++ {
			for j := 0; j < len(ps); j++ {
				a, b := ps[i], ps[j]
				// ¬(a ∧ b) ≡ (¬a ∨ ¬b)
				left := Constraint{Op: OpNot, Term: &Constraint{Op: OpAnd, Terms: []Constraint{a, b}}}
				na := Constraint{Op: OpNot, Term: &a}
				nb := Constraint{Op: OpNot, Term: &b}
				right := Constraint{Op: OpOr, Terms: []Constraint{na, nb}}
				if mustEval(t, left, f) != mustEval(t, right, f) {
					t.Fatalf("de Morgan failed for %v,%v on %+v", a.Op, b.Op, f)
				}
			}
		}
	}
}

func TestProperty_Commutativity(t *testing.T) {
	ps := primitives()
	for _, f := range factMatrix() {
		for i := 0; i < len(ps); i++ {
			for j := 0; j < len(ps); j++ {
				ab := Constraint{Op: OpAnd, Terms: []Constraint{ps[i], ps[j]}}
				ba := Constraint{Op: OpAnd, Terms: []Constraint{ps[j], ps[i]}}
				if mustEval(t, ab, f) != mustEval(t, ba, f) {
					t.Fatal("AND not commutative")
				}
				or1 := Constraint{Op: OpOr, Terms: []Constraint{ps[i], ps[j]}}
				or2 := Constraint{Op: OpOr, Terms: []Constraint{ps[j], ps[i]}}
				if mustEval(t, or1, f) != mustEval(t, or2, f) {
					t.Fatal("OR not commutative")
				}
			}
		}
	}
}

func TestProperty_SingletonIdentity(t *testing.T) {
	// and([p]) ≡ p and or([p]) ≡ p
	for _, f := range factMatrix() {
		for _, p := range primitives() {
			and1 := Constraint{Op: OpAnd, Terms: []Constraint{p}}
			or1 := Constraint{Op: OpOr, Terms: []Constraint{p}}
			want := mustEval(t, p, f)
			if mustEval(t, and1, f) != want || mustEval(t, or1, f) != want {
				t.Fatalf("singleton identity failed for %v", p.Op)
			}
		}
	}
}
