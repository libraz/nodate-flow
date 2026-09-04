// Package agentguard implements the cost / scope guard for AI
// agents. Given an agent's configuration (cost cap, allowed scopes,
// paused flag) and its current month-to-date spend, it decides whether
// a prospective MCP tool invocation should be allowed, denied, or
// whether the agent itself should be auto-paused.
//
// Like the other deterministic engines in this tree (stateinfer,
// reminders, autoactions, inboxtriage, priorityopt, commitmsg), the v1
// logic is pure Go with no DB or network access. Callers load an
// Agent snapshot, call Decide, and persist the side effects (events,
// paused flag) themselves.
package agentguard

// Agent is a point-in-time snapshot of the fields agentguard cares
// about. The caller is responsible for loading it from ai_agents.
type Agent struct {
	PublicID            string
	Enabled             bool
	Paused              bool
	MonthlyCostCapCents *int64   // nil = no cap
	AllowedScopes       []string // nil = inherit from caller token
}

// Request describes a prospective MCP tool call that agentguard is
// being asked to approve.
type Request struct {
	ToolName        string
	RequiredScope   string
	EstimatedCents  int64 // cost the call is expected to add; 0 is allowed
	SpentCentsMonth int64 // month-to-date spend already attributed to this agent
}

// Outcome enumerates the possible Decide results. The zero value
// (OutcomeAllow) is the hot path.
type Outcome int

const (
	// OutcomeAllow: the call may proceed as-is.
	OutcomeAllow Outcome = iota
	// OutcomeDeny: this specific call is rejected but the agent
	// remains active (e.g., the requested scope is not in the
	// agent's allow list).
	OutcomeDeny
	// OutcomePause: the agent has exhausted its monthly budget or
	// was already paused. The caller should mark the agent paused
	// in the DB and refuse all further calls until a human resumes
	// it.
	OutcomePause
)

// Cause names which rule produced a non-allow Decision. Outcome says
// what the caller must do; Cause says why, which is what a caller
// answering a client has to put in the answer — a paused agent and an
// exhausted budget are both a pause and are not the same thing to say.
type Cause int

const (
	// CauseNone: no rule fired; the decision is an allow.
	CauseNone Cause = iota
	// CauseDisabled: the agent is not enabled.
	CauseDisabled
	// CausePaused: the agent is already paused.
	CausePaused
	// CauseScopeNotAllowed: the required scope is not in the agent's
	// allow list.
	CauseScopeNotAllowed
	// CauseCostCapExhausted: month-to-date spend has already reached
	// the effective monthly cost cap.
	CauseCostCapExhausted
	// CauseCostCapWouldExceed: the call's estimated cost would carry
	// month-to-date spend past the effective monthly cost cap.
	CauseCostCapWouldExceed
)

// Decision is the Decide result. Outcome is what the caller must do,
// Cause is which rule produced it. Reason is always human-readable and
// stable enough to surface in audit logs / UI; Cause is the enumerated
// form callers should branch on.
type Decision struct {
	Outcome Outcome
	Cause   Cause
	Reason  string
}

// DecideAccess evaluates the access half of the guard — enabled,
// paused, allowed scopes — and returns the same Decision shape. It
// answers "may this agent act at all", which is the whole question for
// a caller that grants the agent no billable work: a cost cap governs
// money, and there is nothing for it to bound where nothing is spent.
// Decide is this plus the cost rules, so a caller cannot pick up the
// kill switch and miss a rule added to it.
func DecideAccess(agent Agent, requiredScope string) Decision {
	if !agent.Enabled {
		return Decision{Outcome: OutcomePause, Cause: CauseDisabled, Reason: "agent is disabled"}
	}
	if agent.Paused {
		return Decision{Outcome: OutcomePause, Cause: CausePaused, Reason: "agent is paused"}
	}
	if len(agent.AllowedScopes) > 0 && requiredScope != "" && !contains(agent.AllowedScopes, requiredScope) {
		return Decision{
			Outcome: OutcomeDeny,
			Cause:   CauseScopeNotAllowed,
			Reason:  "scope " + requiredScope + " is not in the agent's allow list",
		}
	}
	return Decision{Outcome: OutcomeAllow, Cause: CauseNone, Reason: "within policy"}
}

// Decide evaluates the guard rules against agent + req and returns a
// Decision. Order of checks: disabled → already paused → scope check
// (all three in DecideAccess) → cap exhausted → would-exceed cap →
// allow.
func Decide(agent Agent, req Request) Decision {
	if access := DecideAccess(agent, req.RequiredScope); access.Outcome != OutcomeAllow {
		return access
	}
	if costCap := agent.MonthlyCostCapCents; costCap != nil {
		// Apply a 95% safety margin to the effective cap. Concurrent
		// tool calls may read the same spend value before any of them
		// record their cost, so capping at 95% prevents budget overrun
		// from the resulting race window.
		effectiveCap := *costCap * 95 / 100
		if effectiveCap <= 0 && *costCap > 0 {
			effectiveCap = 1
		}
		if req.SpentCentsMonth >= effectiveCap {
			return Decision{
				Outcome: OutcomePause,
				Cause:   CauseCostCapExhausted,
				Reason:  "monthly cost cap exhausted",
			}
		}
		if req.SpentCentsMonth+req.EstimatedCents > effectiveCap {
			return Decision{
				Outcome: OutcomePause,
				Cause:   CauseCostCapWouldExceed,
				Reason:  "call would exceed monthly cost cap",
			}
		}
	}
	return Decision{Outcome: OutcomeAllow, Cause: CauseNone, Reason: "within policy"}
}

func contains(xs []string, s string) bool {
	for _, x := range xs {
		if x == s {
			return true
		}
	}
	return false
}
