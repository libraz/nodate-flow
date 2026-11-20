package engine_test

import (
	"testing"

	"github.com/libraz/nodate-flow/apps/flow-api/internal/constraint/engine"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/generated"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/taskstate"
)

// TestNextStateParity keeps the replay copy of the state machine identical
// to the canonical one in taskstate for every (state, verb) pair.
func TestNextStateParity(t *testing.T) {
	states := []generated.TasksDerivedState{
		generated.TasksDerivedStateOpen,
		generated.TasksDerivedStateWaiting,
		generated.TasksDerivedStateReview,
		generated.TasksDerivedStateDone,
		generated.TasksDerivedStateCancelled,
	}
	verbs := []string{
		taskstate.TransitionStart,
		taskstate.TransitionBlock,
		taskstate.TransitionUnblock,
		taskstate.TransitionSubmit,
		taskstate.TransitionComplete,
		taskstate.TransitionReopen,
		taskstate.TransitionCancel,
	}
	for _, s := range states {
		for _, v := range verbs {
			want, wantOK := taskstate.NextState(s, v)
			got, gotOK := engine.NextStateForTest(engine.DerivedState(s), v)
			if string(got) != string(want) || gotOK != wantOK {
				t.Errorf("%s/%s: engine=(%q,%v) taskstate=(%q,%v)", s, v, got, gotOK, want, wantOK)
			}
		}
	}
}
