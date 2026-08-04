// Package autoactions implements the "auto actions" stream.
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

// Derived state constants (see sql/flow/tables/tasks.sql).
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
	// KindAutoArchiveCompleted fires when a done/cancelled task has
	// been sitting idle for longer than a threshold. The task is
	// archived automatically to keep the active list clean.
	KindAutoArchiveCompleted Kind = "auto_archive_completed"
	// KindAutoCloseStale fires when an open task has had no updates
	// for longer than a threshold. The task is cancelled
	// automatically so the backlog stays honest.
	KindAutoCloseStale Kind = "auto_close_stale"
	// KindHandoffToUser fires when an AI agent assignee has been
	// attempting the task for a while with no observable progress
	// (derived_state has not changed). The executor hands the task
	// back to a human by emitting agent.task.handoff_to_user,
	// disabling the agent actor row, and stamping agent_memo with
	// handoff_status="handed_back" / handoff_reason="stuck". The
	// runtime's "stuck" reason is distinct from low_confidence /
	// cost_cap / tool_error so the inbox UI can label it explicitly.
	KindHandoffToUser Kind = "handoff_to_user"
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

	// Agent-assignee signals, populated when the task has at least one
	// enabled task_actors row with kind='agent' and role='assignee'.
	// They drive the handoff_to_user rule and are otherwise unused.
	HasAgentAssignee    bool
	AgentAttempts       int
	AgentLastFinishedAt time.Time
}

// Action is the rule engine output. Evaluate returns nil when no
// action applies (terminal state, fresh task, or nothing urgent).
type Action struct {
	Kind       Kind    `json:"kind"`
	Confidence float32 `json:"confidence"`
	Reason     string  `json:"reason"`
}

// RuleConfig holds the per-rule knobs read from the auto_action_rules
// table. The executor passes a slice of these to EvaluateWithConfig.
type RuleConfig struct {
	Kind       Kind
	Enabled    bool
	Confidence float32
	IdleHours  uint32 // 0 for rules that use due_on (escalate_overdue)
	// AttemptsThreshold is the minimum agent run-attempts count
	// before the handoff_to_user rule will fire. Only meaningful for
	// KindHandoffToUser; other kinds ignore it.
	AttemptsThreshold int
}

// DefaultRuleConfigs returns the built-in rule configurations that
// reproduce the original hardcoded behaviour. Callers that have no
// persisted per-workspace overrides should pass this slice to
// EvaluateWithConfig (and Evaluate does exactly that).
func DefaultRuleConfigs() []RuleConfig {
	return []RuleConfig{
		{Kind: KindEscalateOverdue, Enabled: true, Confidence: 0.85, IdleHours: 0},
		{Kind: KindAssignOwner, Enabled: true, Confidence: 0.75, IdleHours: 24},
		{Kind: KindNudgeAssignee, Enabled: true, Confidence: 0.70, IdleHours: 72},
		{Kind: KindCloseStaleReview, Enabled: true, Confidence: 0.70, IdleHours: 120},
		{Kind: KindAutoArchiveCompleted, Enabled: false, Confidence: 0.90, IdleHours: 336}, // 14 days
		{Kind: KindAutoCloseStale, Enabled: false, Confidence: 0.80, IdleHours: 720},       // 30 days
		// HandoffToUser: agent has been attempting the task at least
		// AttemptsThreshold times with the last finished_at older than
		// IdleHours and no observable derived_state movement.
		{Kind: KindHandoffToUser, Enabled: true, Confidence: 0.80, IdleHours: 4, AttemptsThreshold: 3},
	}
}

// Evaluate runs the deterministic auto-action rules against Signals
// using the built-in default thresholds and returns an Action or nil.
// It is a convenience wrapper around EvaluateWithConfig that preserves
// backward compatibility.
func Evaluate(s Signals) *Action {
	return EvaluateWithConfig(s, DefaultRuleConfigs())
}

// EvaluateWithConfig runs the auto-action rules against Signals using
// the caller-supplied per-rule configuration and returns an Action or
// nil. Rules are checked in urgency order (escalate_overdue,
// assign_owner, nudge_assignee, close_stale_review); the first match
// wins so each task yields at most one action. Disabled rules are
// skipped entirely.
func EvaluateWithConfig(s Signals, rules []RuleConfig) *Action {
	if s.Now.IsZero() {
		s.Now = time.Now().UTC()
	}
	idle := s.Now.Sub(s.UpdatedAt)
	overdue := s.HasDueOn && s.Now.After(s.DueOn)

	// Build a lookup so iteration order is controlled by the
	// urgency sequence below, not by the caller's slice order.
	cfgByKind := make(map[Kind]RuleConfig, len(rules))
	for _, r := range rules {
		cfgByKind[r.Kind] = r
	}

	// Terminal tasks only get auto-archive evaluation.
	if s.State == StateDone || s.State == StateCancelled {
		rc, ok := cfgByKind[KindAutoArchiveCompleted]
		if !ok || !rc.Enabled {
			return nil
		}
		threshold := time.Duration(rc.IdleHours) * time.Hour
		if idle >= threshold {
			return &Action{
				Kind:       KindAutoArchiveCompleted,
				Confidence: rc.Confidence,
				Reason:     fmt.Sprintf("completed task idle for %d day(s) — archiving", idleDays(idle)),
			}
		}
		return nil
	}

	// Urgency order: most urgent first. KindHandoffToUser sits below
	// escalate_overdue so a missed deadline still wins, but above
	// nudge_assignee because once the agent looks stuck a human
	// touch is more useful than another automated nudge.
	order := []Kind{
		KindEscalateOverdue,
		KindHandoffToUser,
		KindAssignOwner,
		KindNudgeAssignee,
		KindCloseStaleReview,
		KindAutoCloseStale,
	}

	for _, kind := range order {
		rc, ok := cfgByKind[kind]
		if !ok || !rc.Enabled {
			continue
		}
		threshold := time.Duration(rc.IdleHours) * time.Hour

		switch kind {
		case KindEscalateOverdue:
			if overdue && (s.State == StateOpen || s.State == StateWaiting || s.State == StateReview) {
				return &Action{
					Kind:       KindEscalateOverdue,
					Confidence: rc.Confidence,
					Reason:     "past due date — escalate to an owner",
				}
			}
		case KindAssignOwner:
			if s.State == StateOpen && !s.HasAssignee && idle >= threshold {
				return &Action{
					Kind:       KindAssignOwner,
					Confidence: rc.Confidence,
					Reason:     fmt.Sprintf("open task has no assignee after %d day(s)", idleDays(idle)),
				}
			}
		case KindNudgeAssignee:
			if s.State == StateOpen && s.HasAssignee && idle >= threshold {
				return &Action{
					Kind:       KindNudgeAssignee,
					Confidence: rc.Confidence,
					Reason:     fmt.Sprintf("assigned but idle for %d day(s)", idleDays(idle)),
				}
			}
		case KindCloseStaleReview:
			if s.State == StateReview && idle >= threshold {
				return &Action{
					Kind:       KindCloseStaleReview,
					Confidence: rc.Confidence,
					Reason:     fmt.Sprintf("review has been open for %d day(s)", idleDays(idle)),
				}
			}
		case KindAutoCloseStale:
			if s.State == StateOpen && idle >= threshold {
				return &Action{
					Kind:       KindAutoCloseStale,
					Confidence: rc.Confidence,
					Reason:     fmt.Sprintf("open task idle for %d day(s) — auto-closing", idleDays(idle)),
				}
			}
		case KindHandoffToUser:
			if !s.HasAgentAssignee {
				continue
			}
			if s.AgentAttempts < rc.AttemptsThreshold {
				continue
			}
			// Use last_finished_at when available so cold attempts (a
			// run that started but never recorded a finish) do not
			// short-circuit the idle window. Fall back to UpdatedAt
			// so the rule still fires for tasks whose memo has not
			// yet been stamped with a finish.
			ref := s.AgentLastFinishedAt
			if ref.IsZero() {
				ref = s.UpdatedAt
			}
			if s.Now.Sub(ref) < threshold {
				continue
			}
			return &Action{
				Kind:       KindHandoffToUser,
				Confidence: rc.Confidence,
				Reason: fmt.Sprintf(
					"agent has attempted %d time(s) with no progress for %d hour(s) — handing back to a human",
					s.AgentAttempts, idleHours(s.Now.Sub(ref)),
				),
			}
		}
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

func idleHours(d time.Duration) int {
	hours := int(d / time.Hour)
	if hours < 1 {
		return 1
	}
	return hours
}
