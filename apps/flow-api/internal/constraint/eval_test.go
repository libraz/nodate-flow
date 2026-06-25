package constraint

import (
	"testing"
	"time"
)

func mustParse(t *testing.T, s string) Constraint {
	t.Helper()
	c, err := Parse([]byte(s))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	return c
}

func date(s string) *time.Time {
	d, _ := time.Parse("2006-01-02", s)
	return &d
}

func TestParse_UnknownOp(t *testing.T) {
	if _, err := Parse([]byte(`{"op":"bogus"}`)); err == nil {
		t.Fatal("expected error on unknown op")
	}
}

func TestParse_MissingArg(t *testing.T) {
	if _, err := Parse([]byte(`{"op":"time.due_before"}`)); err == nil {
		t.Fatal("expected error on missing arg")
	}
	if _, err := Parse([]byte(`{"op":"and","terms":[]}`)); err == nil {
		t.Fatal("expected error on empty and")
	}
}

func TestEvaluate_TimeDueBefore(t *testing.T) {
	c := mustParse(t, `{"op":"time.due_before","arg":"2026-05-01"}`)
	// Satisfied when due_on is earlier.
	ok, err := Evaluate(c, Facts{DueOn: date("2026-04-10")})
	if err != nil || !ok {
		t.Fatalf("expected true, got %v %v", ok, err)
	}
	// Not satisfied when due_on is after.
	ok, _ = Evaluate(c, Facts{DueOn: date("2026-06-01")})
	if ok {
		t.Fatal("expected false for later due")
	}
	// Not satisfied when due is nil.
	ok, _ = Evaluate(c, Facts{})
	if ok {
		t.Fatal("expected false for nil due")
	}
}

func TestEvaluate_DepAllDone(t *testing.T) {
	c := mustParse(t, `{"op":"dependency.all_done","taskIds":["a","b"]}`)
	ok, _ := Evaluate(c, Facts{DependencyStates: map[string]string{"a": "done", "b": "done"}})
	if !ok {
		t.Fatal("expected true")
	}
	ok, _ = Evaluate(c, Facts{DependencyStates: map[string]string{"a": "done", "b": "waiting"}})
	if ok {
		t.Fatal("expected false")
	}
	// Missing entry counts as not done.
	ok, _ = Evaluate(c, Facts{DependencyStates: map[string]string{"a": "done"}})
	if ok {
		t.Fatal("expected false on missing dep")
	}
}

func TestEvaluate_DepOpenAtMost_IgnoresNonBlockingKinds(t *testing.T) {
	c := mustParse(t, `{"op":"dependency.open_at_most","max":0}`)

	// Two open deps but both informational (relates / retro_of) → the
	// open count is 0, so an open_at_most:0 constraint is satisfied.
	ok, err := Evaluate(c, Facts{
		DependencyStates: map[string]string{"a": "open", "b": "waiting"},
		DependencyKinds:  map[string]string{"a": "relates", "b": "retro_of"},
	})
	if err != nil || !ok {
		t.Fatalf("expected satisfied (non-blocking deps ignored), got ok=%v err=%v", ok, err)
	}

	// A genuinely-blocking open dep still counts → not satisfied.
	ok, _ = Evaluate(c, Facts{
		DependencyStates: map[string]string{"a": "open", "b": "waiting"},
		DependencyKinds:  map[string]string{"a": "blocks", "b": "relates"},
	})
	if ok {
		t.Fatal("expected unsatisfied: a blocking open dep must count")
	}

	// subtask_of is blocking too.
	ok, _ = Evaluate(c, Facts{
		DependencyStates: map[string]string{"a": "waiting"},
		DependencyKinds:  map[string]string{"a": "subtask_of"},
	})
	if ok {
		t.Fatal("expected unsatisfied: subtask_of is a blocking kind")
	}

	// done / cancelled blocking deps do not count as open.
	ok, _ = Evaluate(c, Facts{
		DependencyStates: map[string]string{"a": "done", "b": "cancelled"},
		DependencyKinds:  map[string]string{"a": "blocks", "b": "blocks"},
	})
	if err != nil || !ok {
		t.Fatalf("expected satisfied: done/cancelled deps are not open, got ok=%v", ok)
	}

	// Backward compat: with no kinds recorded, every open dep counts.
	ok, _ = Evaluate(c, Facts{
		DependencyStates: map[string]string{"a": "open"},
	})
	if ok {
		t.Fatal("expected unsatisfied: missing kind defaults to blocking")
	}
}

func TestEvaluate_ActorHasRole(t *testing.T) {
	c := mustParse(t, `{"op":"actor.has_role","arg":"reviewer"}`)
	ok, _ := Evaluate(c, Facts{ActorRoles: map[string]bool{"reviewer": true}})
	if !ok {
		t.Fatal("expected true")
	}
	ok, _ = Evaluate(c, Facts{ActorRoles: map[string]bool{"author": true}})
	if ok {
		t.Fatal("expected false")
	}
}

func TestEvaluate_AndOrNot(t *testing.T) {
	c := mustParse(t, `{
		"op":"and","terms":[
			{"op":"actor.has_role","arg":"reviewer"},
			{"op":"or","terms":[
				{"op":"time.due_before","arg":"2026-01-01"},
				{"op":"not","term":{"op":"dependency.all_done","taskIds":["x"]}}
			]}
		]
	}`)
	f := Facts{
		ActorRoles:       map[string]bool{"reviewer": true},
		DueOn:            date("2026-04-01"),
		DependencyStates: map[string]string{"x": "waiting"},
	}
	ok, err := Evaluate(c, f)
	if err != nil || !ok {
		t.Fatalf("expected true, got %v %v", ok, err)
	}
	// Flip role → false.
	f.ActorRoles = map[string]bool{}
	ok, _ = Evaluate(c, f)
	if ok {
		t.Fatal("expected false when role missing")
	}
}

func TestEvaluate_Deterministic(t *testing.T) {
	// Same input twice must return the same output (purity smoke test).
	c := mustParse(t, `{"op":"time.due_before","arg":"2026-05-01"}`)
	f := Facts{DueOn: date("2026-04-10")}
	a, _ := Evaluate(c, f)
	b, _ := Evaluate(c, f)
	if a != b {
		t.Fatal("non-deterministic evaluation")
	}
}
