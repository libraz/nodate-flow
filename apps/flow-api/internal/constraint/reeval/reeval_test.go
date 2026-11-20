package reeval

import (
	"context"
	"strings"
	"testing"

	"github.com/libraz/nodate-flow/apps/flow-api/internal/eventbus"
)

// countingGo accepts jobs without running them and counts the submissions.
type countingGo struct{ calls int }

func (c *countingGo) Go(context.Context, uint32, func(context.Context)) bool {
	c.calls++
	return true
}

// TestSubscriberIgnoresEngineOutputKinds pins the loop guard: a verdict
// event never schedules a job, so the engine cannot re-trigger itself.
func TestSubscriberIgnoresEngineOutputKinds(t *testing.T) {
	g := &countingGo{}
	hook := (&Subscriber{Go: g.Go}).Hook()

	for _, kind := range []eventbus.Kind{eventbus.TaskConstraintSatisfied, eventbus.TaskConstraintFailed} {
		hook(context.Background(), 1, string(kind), 1)
	}
	if g.calls != 0 {
		t.Fatalf("verdict events scheduled %d jobs, want 0", g.calls)
	}

	hook(context.Background(), 1, string(eventbus.TaskUpdated), 1)
	if g.calls != 1 {
		t.Fatalf("a task event scheduled %d jobs, want 1", g.calls)
	}
}

// TestTriggersDependentsKinds pins the set of kinds that re-evaluate
// dependents: transitions, auto-completion, deletion and disabling, and no
// other declared kind.
func TestTriggersDependentsKinds(t *testing.T) {
	want := map[eventbus.Kind]bool{
		eventbus.TaskAutoCompleted: true,
		eventbus.ItemDeleted:       true,
		eventbus.TaskDisabled:      true,
	}
	for _, k := range eventbus.Kinds() {
		expected := want[k] || strings.HasPrefix(string(k), "task.transition.")
		if got := triggersDependents(string(k)); got != expected {
			t.Errorf("triggersDependents(%q) = %v, want %v", k, got, expected)
		}
	}
	if !triggersDependents(string(eventbus.TaskTransition("archive"))) {
		t.Error("a runtime-minted transition kind must trigger dependents")
	}
	for _, k := range []string{"task.transitions", "task.transition", "", "item.deleted.extra"} {
		if triggersDependents(k) {
			t.Errorf("triggersDependents(%q) = true, want false", k)
		}
	}
}
