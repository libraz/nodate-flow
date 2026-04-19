package constraint

import (
	"testing"
	"time"
)

// TestSprintTemplate demonstrates that the Cycle/sprint concept is
// fully expressible via constraint primitives.
func TestSprintTemplate(t *testing.T) {
	members := []string{"t-a", "t-b"}
	c := Sprint("2026-05-01", members)

	due := time.Date(2026, 4, 20, 0, 0, 0, 0, time.UTC)
	base := Facts{
		Now:   time.Date(2026, 4, 8, 0, 0, 0, 0, time.UTC),
		DueOn: &due,
		DependencyStates: map[string]string{
			"t-a": "done",
			"t-b": "done",
		},
	}
	ok, err := Evaluate(c, base)
	if err != nil || !ok {
		t.Fatalf("sprint should pass: ok=%v err=%v", ok, err)
	}

	// One member still open → sprint fails.
	base.DependencyStates["t-b"] = "waiting"
	ok, _ = Evaluate(c, base)
	if ok {
		t.Fatal("sprint should fail when a member is not done")
	}

	// All done but past the cutoff → sprint fails.
	base.DependencyStates["t-b"] = "done"
	late := time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC)
	base.DueOn = &late
	ok, _ = Evaluate(c, base)
	if ok {
		t.Fatal("sprint should fail when due_on is after the cutoff")
	}
}

// TestGoalTemplate demonstrates that the Goal concept is fully
// expressible via dependency.all_done.
func TestGoalTemplate(t *testing.T) {
	c := Goal([]string{"child-1", "child-2", "child-3"})

	f := Facts{DependencyStates: map[string]string{
		"child-1": "done",
		"child-2": "done",
		"child-3": "done",
	}}
	ok, _ := Evaluate(c, f)
	if !ok {
		t.Fatal("goal should be satisfied when all children done")
	}

	f.DependencyStates["child-3"] = "open"
	ok, _ = Evaluate(c, f)
	if ok {
		t.Fatal("goal should fail when any child is not done")
	}
}
