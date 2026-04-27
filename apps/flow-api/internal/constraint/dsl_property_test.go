package constraint

import (
	"encoding/json"
	"math/rand"
	"reflect"
	"testing"
)

// dsl_property_test.go — property-based tests for the constraint DSL
// that complement property_test.go (algebraic laws) and fuzz_test.go
// (crash resistance). These focus on structural invariants:
//
//  1. JSON round-trip: Parse(json.Marshal(c)) == c for any valid Constraint.
//  2. Parse never panics on randomised (but structurally valid) JSON.
//  3. Explain is pure: same Constraint always produces the same string.

// randConstraint builds a random but valid Constraint tree up to the
// given depth. It uses testing/quick style manual generation since
// gopter/rapid are not project dependencies.
func randConstraint(rng *rand.Rand, maxDepth int) Constraint {
	if maxDepth <= 0 {
		return randLeaf(rng)
	}
	// 70% leaf, 30% composite to keep trees small.
	if rng.Intn(10) < 7 {
		return randLeaf(rng)
	}
	return randComposite(rng, maxDepth)
}

func randLeaf(rng *rand.Rand) Constraint {
	leaves := []func(*rand.Rand) Constraint{
		func(_ *rand.Rand) Constraint {
			return Constraint{Op: OpTimeDueBefore, Arg: "2026-06-01"}
		},
		func(_ *rand.Rand) Constraint {
			return Constraint{Op: OpTimeDueAfter, Arg: "2026-01-15"}
		},
		func(_ *rand.Rand) Constraint {
			ids := []string{"t1", "t2", "t3"}
			n := rng.Intn(len(ids)) + 1
			return Constraint{Op: OpDepAllDone, TaskIDs: ids[:n]}
		},
		func(r *rand.Rand) Constraint {
			m := r.Intn(5)
			return Constraint{Op: OpDepOpenAtMost, Max: &m}
		},
		func(_ *rand.Rand) Constraint {
			roles := []string{"reviewer", "author", "admin"}
			return Constraint{Op: OpActorHasRole, Arg: roles[rng.Intn(len(roles))]}
		},
		func(_ *rand.Rand) Constraint {
			return Constraint{Op: OpSignalReceived, Arg: "github.pr.merged"}
		},
		func(_ *rand.Rand) Constraint {
			return Constraint{Op: OpApprovalGranted, Arg: "owner"}
		},
		func(_ *rand.Rand) Constraint {
			statuses := []string{"success", "failure", "pending"}
			return Constraint{Op: OpCIStatusIs, Arg: statuses[rng.Intn(len(statuses))]}
		},
	}
	return leaves[rng.Intn(len(leaves))](rng)
}

func randComposite(rng *rand.Rand, maxDepth int) Constraint {
	switch rng.Intn(3) {
	case 0: // and
		n := rng.Intn(3) + 1
		terms := make([]Constraint, n)
		for i := range terms {
			terms[i] = randConstraint(rng, maxDepth-1)
		}
		return Constraint{Op: OpAnd, Terms: terms}
	case 1: // or
		n := rng.Intn(3) + 1
		terms := make([]Constraint, n)
		for i := range terms {
			terms[i] = randConstraint(rng, maxDepth-1)
		}
		return Constraint{Op: OpOr, Terms: terms}
	default: // not
		inner := randConstraint(rng, maxDepth-1)
		return Constraint{Op: OpNot, Term: &inner}
	}
}

// TestProperty_JSONRoundTrip verifies that any valid Constraint
// survives json.Marshal -> Parse without information loss.
func TestProperty_JSONRoundTrip(t *testing.T) {
	t.Parallel()
	rng := rand.New(rand.NewSource(42)) //#nosec G404 -- property test seeded for reproducibility, not crypto
	for i := 0; i < 500; i++ {
		original := randConstraint(rng, 4)
		data, err := json.Marshal(original)
		if err != nil {
			t.Fatalf("iteration %d: marshal failed: %v", i, err)
		}
		parsed, err := Parse(data)
		if err != nil {
			t.Fatalf("iteration %d: parse of valid constraint failed: %v\njson: %s", i, err, data)
		}
		if !reflect.DeepEqual(original, parsed) {
			t.Fatalf("iteration %d: round-trip mismatch\noriginal: %+v\nparsed:   %+v\njson:     %s", i, original, parsed, data)
		}
	}
}

// TestProperty_ParseArbitraryJSON ensures Parse never panics on
// structurally random but syntactically valid JSON. This supplements
// FuzzParse by covering more object shapes with controlled randomness.
func TestProperty_ParseArbitraryJSON(t *testing.T) {
	t.Parallel()
	rng := rand.New(rand.NewSource(99)) //#nosec G404 -- property test seeded for reproducibility, not crypto
	ops := []string{
		"and", "or", "not", "time.due_before", "bogus", "",
		"dependency.all_done", "actor.has_role", "ci.status_is",
	}
	for i := 0; i < 200; i++ {
		// Build a random JSON object with plausible keys.
		m := map[string]any{
			"op": ops[rng.Intn(len(ops))],
		}
		if rng.Intn(2) == 0 {
			m["arg"] = "some-value"
		}
		if rng.Intn(2) == 0 {
			m["terms"] = []any{}
		}
		if rng.Intn(2) == 0 {
			n := rng.Intn(10)
			m["max"] = n
		}
		if rng.Intn(2) == 0 {
			m["taskIds"] = []string{"a", "b"}
		}
		data, _ := json.Marshal(m)
		// Must not panic — error is fine.
		_, _ = Parse(data)
	}
}

// TestProperty_ExplainPure verifies Explain is a pure function:
// same input always produces the same output, across many random
// constraint trees.
func TestProperty_ExplainPure(t *testing.T) {
	t.Parallel()
	rng := rand.New(rand.NewSource(77)) //#nosec G404 -- property test seeded for reproducibility, not crypto
	for i := 0; i < 200; i++ {
		c := randConstraint(rng, 3)
		a := Explain(c)
		b := Explain(c)
		if a != b {
			t.Fatalf("iteration %d: Explain not pure for %v: %q vs %q", i, c.Op, a, b)
		}
	}
}

// TestProperty_ExplainNonEmpty verifies Explain never returns an empty
// string for any valid constraint.
func TestProperty_ExplainNonEmpty(t *testing.T) {
	t.Parallel()
	rng := rand.New(rand.NewSource(33)) //#nosec G404 -- property test seeded for reproducibility, not crypto
	for i := 0; i < 200; i++ {
		c := randConstraint(rng, 3)
		out := Explain(c)
		if out == "" {
			t.Fatalf("iteration %d: Explain returned empty for %v", i, c.Op)
		}
		if out == "unknown" {
			t.Fatalf("iteration %d: Explain returned 'unknown' for valid op %v", i, c.Op)
		}
	}
}

// TestProperty_Associativity verifies that and/or are associative:
// and(a, and(b, c)) == and(and(a, b), c) for all fact/primitive combos.
func TestProperty_Associativity(t *testing.T) {
	t.Parallel()
	ps := primitives()
	facts := factMatrix()
	for _, f := range facts {
		for i := 0; i < len(ps); i++ {
			for j := 0; j < len(ps); j++ {
				for k := 0; k < len(ps); k++ {
					a, b, c := ps[i], ps[j], ps[k]
					// and(a, and(b, c)) vs and(and(a, b), c)
					leftAnd := Constraint{Op: OpAnd, Terms: []Constraint{a, {Op: OpAnd, Terms: []Constraint{b, c}}}}
					rightAnd := Constraint{Op: OpAnd, Terms: []Constraint{{Op: OpAnd, Terms: []Constraint{a, b}}, c}}
					if mustEval(t, leftAnd, f) != mustEval(t, rightAnd, f) {
						t.Fatalf("AND not associative for %v,%v,%v", a.Op, b.Op, c.Op)
					}
					// or(a, or(b, c)) vs or(or(a, b), c)
					leftOr := Constraint{Op: OpOr, Terms: []Constraint{a, {Op: OpOr, Terms: []Constraint{b, c}}}}
					rightOr := Constraint{Op: OpOr, Terms: []Constraint{{Op: OpOr, Terms: []Constraint{a, b}}, c}}
					if mustEval(t, leftOr, f) != mustEval(t, rightOr, f) {
						t.Fatalf("OR not associative for %v,%v,%v", a.Op, b.Op, c.Op)
					}
				}
			}
		}
	}
}
