// Package stateinfer implements the Phase 2 Wave 2 "state inference"
// stream (2.AI-1). Given a compact view of a task's signals (current
// derived state, staleness, dependency count, due date), it proposes
// the next likely state machine transition.
//
// The v1 ruleset is deterministic and LLM-free: the signals already
// carry enough structure that a handful of thresholds match human
// intuition. A future iteration may promote borderline cases to an
// LLM call under the workspace's configured provider, but the API
// shape already accommodates that (Proposal has Confidence + Reason).
package stateinfer

import (
	"fmt"
	"time"
)

// State mirrors tasks.derived_state.
type State string

// Derived state constants (see sql/tables/tasks.sql).
const (
	StateOpen      State = "open"
	StateWaiting   State = "waiting"
	StateReview    State = "review"
	StateDone      State = "done"
	StateCancelled State = "cancelled"
)

// Transition mirrors the state machine transitions exposed by
// POST /tasks/{id}/transitions (see TRANSITIONS_BY_STATE on the
// frontend and apps/api/internal/domain/tasks/transitions.go).
type Transition string

// Known transitions. Only a subset is ever suggested by Infer.
const (
	TransitionStart    Transition = "start"
	TransitionBlock    Transition = "block"
	TransitionSubmit   Transition = "submit"
	TransitionComplete Transition = "complete"
)

// Signals is the compact bag of task facts the inference rules read.
// It is built by the HTTP handler from v_task_detail + "now" and kept
// free of DB types so the core is pure and easy to test.
type Signals struct {
	State           State
	UpdatedAt       time.Time
	DueOn           time.Time
	HasDueOn        bool
	DependencyCount int64
	Now             time.Time
}

// Proposal is the result of Infer. When the rules cannot produce a
// confident suggestion, Infer returns nil.
type Proposal struct {
	Transition Transition `json:"transition"`
	Confidence float32    `json:"confidence"`
	Reason     string     `json:"reason"`
}

// Idle thresholds per source state. Tuned to match the "no progress
// in a week" rule of thumb surfaced in ADR 0004 and phase-2 plan.
const (
	openIdleThreshold    = 3 * 24 * time.Hour
	waitingIdleThreshold = 5 * 24 * time.Hour
	reviewIdleThreshold  = 2 * 24 * time.Hour
)

// Infer runs the rule-based state inference and returns a Proposal
// or nil when no confident suggestion applies. Terminal states
// (done, cancelled) never produce a Proposal.
func Infer(s Signals) *Proposal {
	if s.Now.IsZero() {
		s.Now = time.Now().UTC()
	}
	idle := s.Now.Sub(s.UpdatedAt)
	overdue := s.HasDueOn && s.Now.After(s.DueOn)

	switch s.State {
	case StateOpen:
		if idle >= openIdleThreshold {
			return &Proposal{
				Transition: TransitionStart,
				Confidence: 0.70,
				Reason:     fmt.Sprintf("open task has been idle for %d days", idleDays(idle)),
			}
		}
	case StateWaiting:
		if overdue {
			return &Proposal{
				Transition: TransitionSubmit,
				Confidence: 0.80,
				Reason:     "past due date — likely ready for review",
			}
		}
		if idle >= waitingIdleThreshold && s.DependencyCount > 0 {
			return &Proposal{
				Transition: TransitionBlock,
				Confidence: 0.75,
				Reason:     "waiting on dependencies with no recent progress",
			}
		}
	case StateReview:
		if idle >= reviewIdleThreshold {
			return &Proposal{
				Transition: TransitionComplete,
				Confidence: 0.70,
				Reason:     fmt.Sprintf("review has been open for %d days", idleDays(idle)),
			}
		}
	case StateDone, StateCancelled:
		return nil
	}
	return nil
}

func idleDays(d time.Duration) int {
	days := int(d / (24 * time.Hour))
	if days < 1 {
		return 1
	}
	return days
}
