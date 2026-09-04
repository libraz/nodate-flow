package obs

import (
	"testing"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"
)

// The derived_state values are written as literals rather than through the
// taskStateDone constant or the generated enum on purpose. The dashboard
// queries match these strings; a test written against a constant would still
// pass if the constant were renamed, which is exactly the change that would
// leave the panels reading a label value nothing emits any more.
const (
	wantStateOpen      = "open"
	wantStateWaiting   = "waiting"
	wantStateReview    = "review"
	wantStateDone      = "done"
	wantStateCancelled = "cancelled"
)

// The counters are package-level vars registered in init(), so their values
// persist for the lifetime of the test binary. tasksCreatedTotal and
// tasksCompletedTotal carry no labels, so unlike the AI collectors there is no
// per-case series to isolate a test on: every case here asserts the delta it
// caused, and none of them call t.Parallel, so the deltas cannot interleave.

// transitions reads the transition counter for one fully-qualified series.
// Reading a series that has never been incremented creates it at zero.
func transitions(from, to string) float64 {
	return testutil.ToFloat64(taskStateTransitionsTotal.WithLabelValues(from, to))
}

func created() float64 {
	return testutil.ToFloat64(tasksCreatedTotal)
}

func completed() float64 {
	return testutil.ToFloat64(tasksCompletedTotal)
}

// TestIncTaskTransitionRecordsFromAndTo pins that a state change lands on the
// series keyed by the state left and the state entered, in that order. The
// reverse series must stay untouched: open -> waiting and waiting -> open are
// opposite movements through the workflow and collapsing them would make the
// by-state breakdown meaningless.
func TestIncTaskTransitionRecordsFromAndTo(t *testing.T) {
	forwardBefore := transitions(wantStateOpen, wantStateWaiting)
	reverseBefore := transitions(wantStateWaiting, wantStateOpen)
	completedBefore := completed()

	IncTaskTransition(wantStateOpen, wantStateWaiting)

	if got := transitions(wantStateOpen, wantStateWaiting) - forwardBefore; got != 1 {
		t.Errorf("from_state=%q to_state=%q increment = %v, want 1", wantStateOpen, wantStateWaiting, got)
	}
	if got := transitions(wantStateWaiting, wantStateOpen) - reverseBefore; got != 0 {
		t.Errorf("from_state=%q to_state=%q increment = %v, want 0: the labels are ordered", wantStateWaiting, wantStateOpen, got)
	}
	if got := completed() - completedBefore; got != 0 {
		t.Errorf("completion increment = %v, want 0: only a transition to %q is a completion", got, wantStateDone)
	}
}

// TestIncTaskTransitionSameStateRecordsNothing pins that an UPDATE writing back
// the value it read is not counted. Such a write moves no work through the
// workflow, and counting it would make the rate track write volume instead.
//
// The check deletes rather than reads: reading a counter through
// WithLabelValues would itself create the series it is asking about, and
// DeleteLabelValues reports whether one existed.
func TestIncTaskTransitionSameStateRecordsNothing(t *testing.T) {
	completedBefore := completed()

	IncTaskTransition(wantStateWaiting, wantStateWaiting)
	IncTaskTransition(wantStateDone, wantStateDone)

	if taskStateTransitionsTotal.DeleteLabelValues(wantStateWaiting, wantStateWaiting) {
		t.Errorf("a series exists for from_state=to_state=%q, want none", wantStateWaiting)
	}
	if taskStateTransitionsTotal.DeleteLabelValues(wantStateDone, wantStateDone) {
		t.Errorf("a series exists for from_state=to_state=%q, want none", wantStateDone)
	}
	if got := completed() - completedBefore; got != 0 {
		t.Errorf("completion increment = %v, want 0: a task already in %q did not complete again", got, wantStateDone)
	}
}

// TestIncTaskTransitionToDoneCountsCompletion pins that entering the done state
// increments the transition counter and the completion counter together. The
// two are recorded from one call so they cannot disagree about what a
// completion is; this asserts that both actually move.
func TestIncTaskTransitionToDoneCountsCompletion(t *testing.T) {
	transitionBefore := transitions(wantStateReview, wantStateDone)
	completedBefore := completed()

	IncTaskTransition(wantStateReview, wantStateDone)

	if got := transitions(wantStateReview, wantStateDone) - transitionBefore; got != 1 {
		t.Errorf("from_state=%q to_state=%q increment = %v, want 1", wantStateReview, wantStateDone, got)
	}
	if got := completed() - completedBefore; got != 1 {
		t.Errorf("completion increment = %v, want 1: to_state=%q is a completion", got, wantStateDone)
	}
}

// TestIncTaskTransitionOutOfDoneIsNotACompletion pins that leaving the done
// state does not count as completing. A reopen moves work backwards, and a
// completion counter that grew on it would report more finished tasks than the
// workspace ever finished.
func TestIncTaskTransitionOutOfDoneIsNotACompletion(t *testing.T) {
	transitionBefore := transitions(wantStateDone, wantStateWaiting)
	completedBefore := completed()

	IncTaskTransition(wantStateDone, wantStateWaiting)

	if got := transitions(wantStateDone, wantStateWaiting) - transitionBefore; got != 1 {
		t.Errorf("from_state=%q to_state=%q increment = %v, want 1", wantStateDone, wantStateWaiting, got)
	}
	if got := completed() - completedBefore; got != 0 {
		t.Errorf("completion increment = %v, want 0: leaving %q is not a completion", got, wantStateDone)
	}
}

// TestIncTaskCreatedCountsOnce pins that one call records exactly one creation
// and touches neither of the other two counters. The creation counter and the
// completion counter are read at opposite ends of the same funnel panel, so a
// creation leaking into either the transition or the completion series would
// make the funnel close on itself.
func TestIncTaskCreatedCountsOnce(t *testing.T) {
	createdBefore := created()
	completedBefore := completed()
	transitionBefore := transitions(wantStateOpen, wantStateCancelled)

	IncTaskCreated()

	if got := created() - createdBefore; got != 1 {
		t.Errorf("creation increment = %v, want 1", got)
	}
	if got := completed() - completedBefore; got != 0 {
		t.Errorf("completion increment = %v, want 0: a new task has not finished", got)
	}
	if got := transitions(wantStateOpen, wantStateCancelled) - transitionBefore; got != 0 {
		t.Errorf("transition increment = %v, want 0: creating a task is not a transition", got)
	}
}

// TestTaskMetricNamesAndLabels pins the exposed metric names and the label
// names of the transition counter against the dashboard that reads them.
// nf_task_state_transitions_total is aggregated with `sum by (to_state)`, so a
// renamed label would leave that panel grouping by a label nothing carries
// while the metric itself kept scraping normally.
func TestTaskMetricNamesAndLabels(t *testing.T) {
	// The series must exist before it can be gathered.
	IncTaskCreated()
	IncTaskTransition(wantStateOpen, wantStateDone)

	families, err := prometheus.DefaultGatherer.Gather()
	if err != nil {
		t.Fatalf("gather: %v", err)
	}
	byName := make(map[string][]string, len(families))
	for _, f := range families {
		var labels []string
		if metrics := f.GetMetric(); len(metrics) > 0 {
			for _, pair := range metrics[0].GetLabel() {
				labels = append(labels, pair.GetName())
			}
		}
		byName[f.GetName()] = labels
	}

	for _, name := range []string{"nf_tasks_created_total", "nf_tasks_completed_total"} {
		labels, ok := byName[name]
		if !ok {
			t.Errorf("%s is not exposed", name)
			continue
		}
		if len(labels) != 0 {
			t.Errorf("%s carries labels %v, want none: no metric in this service may key on a workspace", name, labels)
		}
	}

	labels, ok := byName["nf_task_state_transitions_total"]
	if !ok {
		t.Fatal("nf_task_state_transitions_total is not exposed")
	}
	want := map[string]bool{"from_state": false, "to_state": false}
	for _, name := range labels {
		if _, expected := want[name]; !expected {
			t.Errorf("nf_task_state_transitions_total carries unexpected label %q", name)
			continue
		}
		want[name] = true
	}
	for name, seen := range want {
		if !seen {
			t.Errorf("nf_task_state_transitions_total is missing label %q", name)
		}
	}
}
