// Package autoactions implements the "auto actions" stream (2.AI-3).
// Given a compact view of a task's state, staleness,
// assignment, and due date, it proposes at most one concrete next
// action a workspace operator should take (nudge the assignee, assign
// an owner, escalate, close a stale review).
//
// Like stateinfer and reminders, the v1 ruleset is deterministic: the
// signals already carry enough structure that a few thresholds match
// the judgement calls humans make when grooming a backlog, and a pure
// rule engine is cheaper and easier to test than an LLM call. A
// future iteration may escalate borderline cases to a provider call
// under the workspace's LLM configuration, but the API shape already
// accommodates that via Action.Confidence + Action.Reason.
package autoactions

import (
	"fmt"
	"time"
)

// State mirrors tasks.derived_state for the subset auto actions care
// about. Callers pass the raw derived state string straight through.
type State string

// Derived state constants (see sql/tables/tasks.sql).
const (
	StateOpen      State = "open"
	StateWaiting   State = "waiting"
	StateReview    State = "review"
	StateDone      State = "done"
	StateCancelled State = "cancelled"
)

// Kind classifies the recommended action. Ordered roughly from most
// urgent to least urgent; the HTTP layer uses this for sorting.
type Kind string

// Action kinds emitted by Evaluate. The set is intentionally small:
// each kind maps to a concrete UI affordance in the glass dock.
const (
	// KindEscalateOverdue fires when an open / waiting / review task
	// has passed its due date. The operator should raise priority or
	// escalate to an owner.
	KindEscalateOverdue Kind = "escalate_overdue"
	// KindAssignOwner fires when an open task has sat idle with no
	// primary assignee. The operator should pick someone.
	KindAssignOwner Kind = "assign_owner"
	// KindNudgeAssignee fires when an open task has sat idle with an
	// assignee but no progress. The operator should send a nudge.
	KindNudgeAssignee Kind = "nudge_assignee"
	// KindCloseStaleReview fires when a task has been in review for
	// longer than a working week with no activity. The operator
	// should either sign off or kick it back.
	KindCloseStaleReview Kind = "close_stale_review"
)

// Signals is the compact bag of task facts the rules read. It is kept
// free of DB types so the core stays pure and unit-testable.
type Signals struct {
	State         State
	UpdatedAt     time.Time
	DueOn         time.Time
	HasDueOn      bool
	HasAssignee   bool
	AssigneeCount int64
	Now           time.Time
}

// Action is the rule engine output. Evaluate returns nil when no
// action applies (terminal state, fresh task, or nothing urgent).
type Action struct {
	Kind       Kind    `json:"kind"`
	Confidence float32 `json:"confidence"`
	Reason     string  `json:"reason"`
}

// Idle thresholds per rule. Tuned to match the "backlog grooming"
// rhythm documented in the plan and match the day-granularity
// comparisons used elsewhere in the AI package.
const (
	assignOwnerIdleThreshold   = 1 * 24 * time.Hour
	nudgeAssigneeIdleThreshold = 3 * 24 * time.Hour
	staleReviewIdleThreshold   = 5 * 24 * time.Hour
)

// Evaluate runs the deterministic auto-action rules against Signals
// and returns an Action or nil. Rules are checked in descending order
// of urgency; the first match wins so each task yields at most one
// action.
func Evaluate(s Signals) *Action {
	if s.State == StateDone || s.State == StateCancelled {
		return nil
	}
	if s.Now.IsZero() {
		s.Now = time.Now().UTC()
	}
	idle := s.Now.Sub(s.UpdatedAt)
	overdue := s.HasDueOn && s.Now.After(s.DueOn)

	// Most urgent: anything past its due date.
	if overdue && (s.State == StateOpen || s.State == StateWaiting || s.State == StateReview) {
		return &Action{
			Kind:       KindEscalateOverdue,
			Confidence: 0.85,
			Reason:     "past due date — escalate to an owner",
		}
	}

	switch s.State {
	case StateOpen:
		// No owner and the task has been sitting there for a day:
		// the first action is to give it one.
		if !s.HasAssignee && idle >= assignOwnerIdleThreshold {
			return &Action{
				Kind:       KindAssignOwner,
				Confidence: 0.75,
				Reason:     fmt.Sprintf("open task has no assignee after %d day(s)", idleDays(idle)),
			}
		}
		// Has an owner but no progress: nudge them.
		if s.HasAssignee && idle >= nudgeAssigneeIdleThreshold {
			return &Action{
				Kind:       KindNudgeAssignee,
				Confidence: 0.70,
				Reason:     fmt.Sprintf("assigned but idle for %d day(s)", idleDays(idle)),
			}
		}
	case StateReview:
		if idle >= staleReviewIdleThreshold {
			return &Action{
				Kind:       KindCloseStaleReview,
				Confidence: 0.70,
				Reason:     fmt.Sprintf("review has been open for %d day(s)", idleDays(idle)),
			}
		}
	case StateWaiting, StateDone, StateCancelled:
		// No auto action for plain waiting or terminal states.
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
