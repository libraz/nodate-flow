// Package reminders implements the Phase 2 Wave 2 "reminder engine"
// stream (2.AI-4). Given a compact view of a task's timing signals
// (current state, due date, now), it emits at most one reminder of a
// fixed kind. Like stateinfer, the v1 ruleset is deterministic — the
// signals already carry enough structure that a handful of date
// comparisons beat a probabilistic LLM call.
package reminders

import (
	"fmt"
	"time"
)

// State mirrors tasks.derived_state for the subset reminders care about.
type State string

// Terminal states are defined so callers can pass raw derived state
// strings without translating first.
const (
	StateOpen      State = "open"
	StateWaiting   State = "waiting"
	StateReview    State = "review"
	StateDone      State = "done"
	StateCancelled State = "cancelled"
)

// Kind classifies the reminder's urgency. Ordered from most urgent to
// least urgent; the HTTP layer uses this for sorting.
type Kind string

// Reminder kinds emitted by Evaluate.
const (
	KindOverdue  Kind = "overdue"
	KindDueToday Kind = "due_today"
	KindDueSoon  Kind = "due_soon"
)

// Signals is the compact bag of task facts the rules read. It is kept
// free of DB types so the core stays pure and unit-testable.
type Signals struct {
	State    State
	DueOn    time.Time
	HasDueOn bool
	Now      time.Time
}

// Reminder is the rule engine output. Evaluate returns nil when no
// reminder applies (terminal state, no due date, or due date still far
// out).
type Reminder struct {
	Kind         Kind   `json:"kind"`
	DaysUntilDue int    `json:"daysUntilDue"`
	Message      string `json:"message"`
}

// Window thresholds for Evaluate, in days.
const (
	dueSoonDays = 3
)

// Evaluate runs the deterministic reminder rules against Signals and
// returns a Reminder or nil. Terminal states (done, cancelled) and
// tasks without a due date never produce a reminder.
func Evaluate(s Signals) *Reminder {
	if !s.HasDueOn {
		return nil
	}
	if s.State == StateDone || s.State == StateCancelled {
		return nil
	}
	if s.Now.IsZero() {
		s.Now = time.Now().UTC()
	}
	// Compare at day granularity so "due today" is not confused with
	// "overdue by a few hours" when the server clock drifts past the
	// stored midnight.
	now := truncateDay(s.Now)
	due := truncateDay(s.DueOn)
	diffDays := int(due.Sub(now) / (24 * time.Hour))

	switch {
	case diffDays < 0:
		return &Reminder{
			Kind:         KindOverdue,
			DaysUntilDue: diffDays,
			Message:      fmt.Sprintf("overdue by %d day(s)", -diffDays),
		}
	case diffDays == 0:
		return &Reminder{
			Kind:         KindDueToday,
			DaysUntilDue: 0,
			Message:      "due today",
		}
	case diffDays <= dueSoonDays:
		return &Reminder{
			Kind:         KindDueSoon,
			DaysUntilDue: diffDays,
			Message:      fmt.Sprintf("due in %d day(s)", diffDays),
		}
	}
	return nil
}

func truncateDay(t time.Time) time.Time {
	return time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, time.UTC)
}
