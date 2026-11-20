// Package priorityopt implements the "priority optimization" stream
// Given a compact view of a workspace's open tasks, it
// proposes a suggested priority in [0, 4] for each task so humans can
// triage the backlog against a single consistent ranking.
//
// Like the other deterministic engines in this tree (stateinfer,
// reminders, autoactions, inboxtriage), the ruleset is pure Go and
// intentionally simple: a handful of signal weights + due-date
// urgency, tuned against the judgement calls a human makes when
// skimming a cluttered backlog. The API shape already accommodates a
// future LLM-backed path via Suggestion.Confidence + Suggestion.Reasons.
package priorityopt

import (
	"sort"
	"time"

	"github.com/libraz/nodate-flow/apps/flow-api/internal/ai/duedate"
	"github.com/libraz/nodate-flow/packages/go-shared/region"
)

// State mirrors tasks.derived_state. Only a subset is urgency-relevant
// (open / waiting / review); done / cancelled are filtered out by the
// caller before reaching Evaluate.
type State string

// Derived state constants (see sql/flow/tables/tasks.sql).
const (
	StateOpen    State = "open"
	StateWaiting State = "waiting"
	StateReview  State = "review"
)

// Signals is one task distilled to the fields the rules care about.
// Callers build it from ListTasksForWorkspaceRow + `now`.
type Signals struct {
	TaskID          string
	Title           string
	State           State
	CurrentPriority int32
	UpdatedAt       time.Time
	DueOn           time.Time
	HasDueOn        bool
	// DependentCount is the number of tasks blocked on this task.
	DependentCount int64
	HasAssignee    bool
	Now            time.Time
	// Zone is the zone whose calendar day "today" is; the zero value
	// reads the day in UTC.
	Zone region.Zone
}

// ReasonCode identifies a rule that contributed to a suggestion; clients
// render the wording.
type ReasonCode string

// Reason codes emitted in Suggestion.Reasons.
const (
	ReasonOverdue          ReasonCode = "overdue"
	ReasonDueByTomorrow    ReasonCode = "due_by_tomorrow"
	ReasonDueThisWeek      ReasonCode = "due_this_week"
	ReasonDueThisFortnight ReasonCode = "due_this_fortnight"
	ReasonInReview         ReasonCode = "in_review"
	ReasonManyDependents   ReasonCode = "many_dependents"
	ReasonUnassigned       ReasonCode = "unassigned"
)

// Suggestion is the output row. SuggestedPriority is clamped to
// [0, 4]; Confidence is in [0, 1]. Reasons is never nil; empty means
// no rule fired (steady). DependentCount is copied from the signals.
type Suggestion struct {
	TaskID            string
	Title             string
	CurrentPriority   int32
	SuggestedPriority int32
	Confidence        float32
	Reasons           []ReasonCode
	DependentCount    int64
}

// Evaluate runs the rules over signals and returns one [Suggestion]
// per task whose suggested priority differs from its current priority.
// The returned slice is sorted by descending suggested priority, then
// by descending confidence. Always non-nil.
func Evaluate(signals []Signals) []Suggestion {
	out := make([]Suggestion, 0, len(signals))
	for _, s := range signals {
		sug := evaluateOne(s)
		if sug.SuggestedPriority == s.CurrentPriority {
			continue
		}
		out = append(out, sug)
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].SuggestedPriority != out[j].SuggestedPriority {
			return out[i].SuggestedPriority > out[j].SuggestedPriority
		}
		return out[i].Confidence > out[j].Confidence
	})
	return out
}

func evaluateOne(s Signals) Suggestion {
	if s.Now.IsZero() {
		s.Now = time.Now().UTC()
	}

	// Start from a neutral score of 1.5 (~medium low) and nudge from
	// there. Score is later clamped and rounded into [0, 4].
	score := float32(1.5)
	reasons := make([]ReasonCode, 0, 4)

	// Due date urgency: overdue > soon > far > none, counted in calendar
	// days so a task due today is due soon, not overdue.
	if s.HasDueOn {
		daysUntilDue := duedate.DaysUntil(s.DueOn, s.Now, s.Zone)
		switch {
		case daysUntilDue < 0:
			score += 2.5
			reasons = append(reasons, ReasonOverdue)
		case daysUntilDue <= 1:
			score += 1.8
			reasons = append(reasons, ReasonDueByTomorrow)
		case daysUntilDue <= 7:
			score += 0.9
			reasons = append(reasons, ReasonDueThisWeek)
		case daysUntilDue <= 14:
			score += 0.3
			reasons = append(reasons, ReasonDueThisFortnight)
		}
	}

	// State: review is usually blocking a teammate, bump it.
	if s.State == StateReview {
		score += 0.4
		reasons = append(reasons, ReasonInReview)
	}
	// Waiting means someone else owes us something; slightly lower
	// urgency on our side.
	if s.State == StateWaiting {
		score -= 0.3
	}

	// A task with many dependents on it is a bottleneck.
	if s.DependentCount >= 3 {
		score += 0.5
		reasons = append(reasons, ReasonManyDependents)
	} else if s.DependentCount >= 1 {
		score += 0.2
	}

	// Unassigned in-flight work is a process smell: keep it visible.
	if !s.HasAssignee && s.State != StateWaiting {
		score += 0.2
		reasons = append(reasons, ReasonUnassigned)
	}

	// Staleness: tasks that have not moved in a long time are usually
	// lower urgency than their due date alone would suggest (they have
	// survived this long without attention).
	ageDays := float32(s.Now.Sub(s.UpdatedAt).Hours() / 24)
	if ageDays >= 30 {
		score -= 0.4
	}

	suggested := clampPriority(int32(round(score)))
	conf := confidenceFor(s, suggested)

	return Suggestion{
		TaskID:            s.TaskID,
		Title:             s.Title,
		CurrentPriority:   s.CurrentPriority,
		SuggestedPriority: suggested,
		Confidence:        conf,
		Reasons:           reasons,
		DependentCount:    s.DependentCount,
	}
}

// confidenceFor gives a rough self-assessment of how strongly the
// rules endorse the suggested priority. Larger deltas and stronger
// signals raise confidence.
func confidenceFor(s Signals, suggested int32) float32 {
	delta := suggested - s.CurrentPriority
	if delta < 0 {
		delta = -delta
	}
	base := float32(0.45) + float32(delta)*0.15
	if s.HasDueOn && duedate.Overdue(s.DueOn, s.Now, s.Zone) {
		base += 0.20 // overdue → high confidence
	}
	if base > 0.95 {
		base = 0.95
	}
	if base < 0.30 {
		base = 0.30
	}
	return base
}

func clampPriority(p int32) int32 {
	if p < 0 {
		return 0
	}
	if p > 4 {
		return 4
	}
	return p
}

func round(f float32) float32 {
	if f >= 0 {
		return float32(int32(f + 0.5))
	}
	return float32(int32(f - 0.5))
}
