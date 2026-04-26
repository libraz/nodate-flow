package taskstate

import (
	"testing"

	"github.com/nodate-flow/nodate-flow/apps/flow-api/internal/db/generated"
)

// TestNextState walks every legal (current, transition) pair and a
// representative selection of illegal pairs, asserting the v1 state
// machine. Adding a new transition or reshaping the diagram should
// require touching this table — keep the table in lockstep with
// docs/adr/0001 and the OpenAPI enum on TransitionTaskBody.
func TestNextState(t *testing.T) {
	t.Parallel()

	type tc struct {
		from  generated.TasksDerivedState
		via   string
		want  generated.TasksDerivedState
		legal bool
	}

	cases := []tc{
		// open -> ...
		{generated.TasksDerivedStateOpen, TransitionStart, generated.TasksDerivedStateWaiting, true},
		{generated.TasksDerivedStateOpen, TransitionCancel, generated.TasksDerivedStateCancelled, true},
		{generated.TasksDerivedStateOpen, TransitionComplete, generated.TasksDerivedStateDone, true},
		{generated.TasksDerivedStateOpen, TransitionSubmit, "", false},
		{generated.TasksDerivedStateOpen, TransitionReopen, "", false},

		// waiting -> ...
		{generated.TasksDerivedStateWaiting, TransitionSubmit, generated.TasksDerivedStateReview, true},
		{generated.TasksDerivedStateWaiting, TransitionBlock, generated.TasksDerivedStateOpen, true},
		{generated.TasksDerivedStateWaiting, TransitionCancel, generated.TasksDerivedStateCancelled, true},
		{generated.TasksDerivedStateWaiting, TransitionStart, "", false},

		// review -> ...
		{generated.TasksDerivedStateReview, TransitionComplete, generated.TasksDerivedStateDone, true},
		{generated.TasksDerivedStateReview, TransitionReopen, generated.TasksDerivedStateWaiting, true},
		{generated.TasksDerivedStateReview, TransitionCancel, generated.TasksDerivedStateCancelled, true},
		{generated.TasksDerivedStateReview, TransitionStart, "", false},

		// terminal -> reopen only
		{generated.TasksDerivedStateDone, TransitionReopen, generated.TasksDerivedStateWaiting, true},
		{generated.TasksDerivedStateDone, TransitionStart, "", false},
		{generated.TasksDerivedStateCancelled, TransitionReopen, generated.TasksDerivedStateOpen, true},
		{generated.TasksDerivedStateCancelled, TransitionComplete, "", false},
	}

	for _, c := range cases {
		c := c
		name := string(c.from) + "/" + c.via
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			got, ok := NextState(c.from, c.via)
			if ok != c.legal {
				t.Fatalf("NextState(%q,%q) ok=%v want=%v", c.from, c.via, ok, c.legal)
			}
			if got != c.want {
				t.Fatalf("NextState(%q,%q) got=%q want=%q", c.from, c.via, got, c.want)
			}
		})
	}
}

// TestIsKnownTransition guards the enum used by the HTTP handler and
// the auto-action executor. New transition names must be added in
// state.go (knownTransitions + NextState) before this set widens.
func TestIsKnownTransition(t *testing.T) {
	t.Parallel()
	known := []string{
		TransitionStart, TransitionBlock, TransitionUnblock,
		TransitionSubmit, TransitionComplete, TransitionReopen,
		TransitionCancel,
	}
	for _, k := range known {
		if !IsKnownTransition(k) {
			t.Fatalf("expected %q to be known", k)
		}
	}
	for _, k := range []string{"", "teleport", "Start", "complete "} {
		if IsKnownTransition(k) {
			t.Fatalf("expected %q to be unknown", k)
		}
	}
}
