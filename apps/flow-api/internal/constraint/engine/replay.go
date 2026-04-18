package engine

import (
	"errors"
	"strings"
)

// DerivedState is the replay output alphabet. It intentionally
// mirrors generated.TasksDerivedState as plain strings so the
// replay package has no sqlc import cycle and can be unit-tested
// with string literals.
type DerivedState string

const (
	StateOpen      DerivedState = "open"
	StateWaiting   DerivedState = "waiting"
	StateReview    DerivedState = "review"
	StateDone      DerivedState = "done"
	StateCancelled DerivedState = "cancelled"
)

// TransitionEvent is one event row the replay engine cares about.
// Non-transition events are filtered out by the caller before they
// reach Replay so this struct stays minimal.
type TransitionEvent struct {
	// Name is the transition verb ("start", "complete", ...). It
	// is usually parsed from the event Type suffix via
	// [ParseTransitionName].
	Name string
}

// ParseTransitionName extracts the verb from an event type string
// like "task.transition.start". Returns ok=false for anything
// else so the caller can skip non-transition events cleanly.
func ParseTransitionName(eventType string) (string, bool) {
	const prefix = "task.transition."
	if !strings.HasPrefix(eventType, prefix) {
		return "", false
	}
	name := strings.TrimPrefix(eventType, prefix)
	if name == "" {
		return "", false
	}
	return name, true
}

// ErrIllegalTransition is returned when Replay encounters a
// transition that the v1 state machine (ADR 0001) does not allow
// from the current state. It indicates a bug in the write path,
// not in replay, so surfacing it loudly is the right move.
var ErrIllegalTransition = errors.New("engine: illegal transition")

// Replay derives a task's final state by applying events in order
// from the initial "open" state, using the ADR 0001 v1 state
// machine. The result must match the task's current
// tasks.derived_state — any drift proves a write-path bug
// (3.TEST-3 replay equivalence).
//
// Replay is a pure function of its input slice.
func Replay(events []TransitionEvent) (DerivedState, error) {
	state := StateOpen
	for _, ev := range events {
		next, ok := nextState(state, ev.Name)
		if !ok {
			return state, ErrIllegalTransition
		}
		state = next
	}
	return state, nil
}

// nextState is a string-typed copy of the state machine in
// apps/flow-api/internal/http/handlers/tasks/transitions.go so the
// replay package stays free of the generated enum dependency. Any
// change to the canonical state machine MUST be reflected here
// and covered by replay equivalence tests.
func nextState(current DerivedState, transition string) (DerivedState, bool) {
	switch current {
	case StateOpen:
		switch transition {
		case "start":
			return StateWaiting, true
		case "cancel":
			return StateCancelled, true
		case "complete":
			return StateDone, true
		}
	case StateWaiting:
		switch transition {
		case "submit":
			return StateReview, true
		case "block":
			return StateOpen, true
		case "cancel":
			return StateCancelled, true
		}
	case StateReview:
		switch transition {
		case "complete":
			return StateDone, true
		case "reopen":
			return StateWaiting, true
		case "cancel":
			return StateCancelled, true
		}
	case StateDone:
		if transition == "reopen" {
			return StateWaiting, true
		}
	case StateCancelled:
		if transition == "reopen" {
			return StateOpen, true
		}
	}
	return "", false
}
