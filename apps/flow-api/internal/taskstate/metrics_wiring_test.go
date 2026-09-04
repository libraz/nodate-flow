package taskstate

import (
	"os"
	"strings"
	"testing"
)

// TestTransitionIsCounted pins that ApplyTransitionTx records the transition
// counter, and records it with the state read under the row lock rather than a
// value reconstructed from somewhere else.
//
// The assertion is on the source because the counter sits after a locked read,
// an UPDATE and an event append, none of which run without a database. What it
// can still prove is the part that would be wrong silently: this function is
// the only writer of derived_state — the trg_tasks_derived_state_guard trigger
// rejects every other one — so a transition that is not counted here is not
// counted anywhere, and the dashboard would read a flat line rather than an
// error.
func TestTransitionIsCounted(t *testing.T) {
	t.Parallel()

	src := readStateSource(t)

	const call = "obs.IncTaskTransition(string(locked.DerivedState), string(next))"
	if n := strings.Count(src, call); n != 1 {
		t.Errorf("ApplyTransitionTx must contain exactly one %s; found %d.\n"+
			"The prior state must come from the locked row: any other source is a guess about "+
			"what the task was before this transaction, and from_state would be wrong on the dashboard.", call, n)
	}
}

// TestTransitionCountedOnlyOnTheSuccessPath pins that the counter is recorded
// after the event append rather than before it. A transition whose event append
// fails rolls the whole transaction back, so counting it earlier would report
// workflow movement that never persisted.
func TestTransitionCountedOnlyOnTheSuccessPath(t *testing.T) {
	t.Parallel()

	src := readStateSource(t)

	appendAt := strings.Index(src, "eventbus.Append(")
	if appendAt < 0 {
		t.Fatal("could not locate the eventbus.Append call in ApplyTransitionTx")
	}
	countAt := strings.Index(src, "obs.IncTaskTransition(")
	if countAt < 0 {
		t.Fatal("could not locate the obs.IncTaskTransition call in ApplyTransitionTx")
	}
	if countAt < appendAt {
		t.Error("obs.IncTaskTransition must come after eventbus.Append: a failed append aborts the transition, and a counter recorded first would count it anyway")
	}
}

func readStateSource(t *testing.T) string {
	t.Helper()

	b, err := os.ReadFile("state.go")
	if err != nil {
		t.Fatalf("read state.go: %v", err)
	}
	return string(b)
}
