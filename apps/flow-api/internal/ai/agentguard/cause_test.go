package agentguard

import (
	"fmt"
	"go/ast"
	"go/format"
	"go/parser"
	"go/token"
	"os"
	"slices"
	"strings"
	"testing"
)

// guardCase is one point in the guard's input space with the full
// Decision both entry points must return for it. Outcome, Cause and
// Reason are asserted together: Outcome alone does not distinguish a
// disabled agent from a paused one from an exhausted budget, and a
// Cause asserted without its Outcome would not notice the two drifting
// apart.
type guardCase struct {
	name       string
	agent      Agent
	req        Request
	wantDecide Decision
	wantAccess Decision
}

// pairwiseCases covers every pair of values drawn from the parameters
// the guard reads:
//
//	Enabled              true, false
//	Paused               true, false
//	AllowedScopes        nil, empty, containing the required scope,
//	                     excluding it
//	RequiredScope        empty, a scope name
//	MonthlyCostCapCents  nil, 0, 1, 100
//	SpentCentsMonth      zero, one under the effective cap, exactly at
//	                     it, past it
//	EstimatedCents       zero, exactly filling the remaining headroom,
//	                     one past it
//
// The rules interact through an evaluation order — disabled beats
// paused beats scope beats cost — so a case chosen for one parameter
// carries an arbitrary value for the rest, and hand-picked cases tend to
// agree on those. Choosing the combinations by all-pairs instead puts
// every parameter against every value of every other, which is what
// makes a rule that fires under the wrong companion values visible.
// Boundaries that no combinatorial model produces on its own are pinned
// separately in boundaryCases.
var pairwiseCases = []guardCase{
	{
		name:       "enabled/active/scopes-contains/required-read/cap-100/spend-just-under/est-zero",
		agent:      Agent{Enabled: true, Paused: false, MonthlyCostCapCents: capOf(100), AllowedScopes: []string{"read:workspace", "write:task"}},
		req:        Request{RequiredScope: "read:workspace", SpentCentsMonth: 94, EstimatedCents: 0},
		wantDecide: Decision{Outcome: OutcomeAllow, Cause: CauseNone, Reason: "within policy"},
		wantAccess: Decision{Outcome: OutcomeAllow, Cause: CauseNone, Reason: "within policy"},
	},
	{
		name:       "enabled/active/scopes-nil/required-read/cap-nil/spend-above/est-exceeds",
		agent:      Agent{Enabled: true, Paused: false, MonthlyCostCapCents: nil, AllowedScopes: nil},
		req:        Request{RequiredScope: "read:workspace", SpentCentsMonth: 500, EstimatedCents: 1000},
		wantDecide: Decision{Outcome: OutcomeAllow, Cause: CauseNone, Reason: "within policy"},
		wantAccess: Decision{Outcome: OutcomeAllow, Cause: CauseNone, Reason: "within policy"},
	},
	{
		name:       "disabled/paused/scopes-nil/required-none/cap-100/spend-zero/est-fits-exactly",
		agent:      Agent{Enabled: false, Paused: true, MonthlyCostCapCents: capOf(100), AllowedScopes: nil},
		req:        Request{RequiredScope: "", SpentCentsMonth: 0, EstimatedCents: 95},
		wantDecide: Decision{Outcome: OutcomePause, Cause: CauseDisabled, Reason: "agent is disabled"},
		wantAccess: Decision{Outcome: OutcomePause, Cause: CauseDisabled, Reason: "agent is disabled"},
	},
	{
		name:       "disabled/active/scopes-empty/required-none/cap-0/spend-at-effective/est-zero",
		agent:      Agent{Enabled: false, Paused: false, MonthlyCostCapCents: capOf(0), AllowedScopes: []string{}},
		req:        Request{RequiredScope: "", SpentCentsMonth: 0, EstimatedCents: 0},
		wantDecide: Decision{Outcome: OutcomePause, Cause: CauseDisabled, Reason: "agent is disabled"},
		wantAccess: Decision{Outcome: OutcomePause, Cause: CauseDisabled, Reason: "agent is disabled"},
	},
	{
		name:       "enabled/paused/scopes-excludes/required-read/cap-1/spend-at-effective/est-fits-exactly",
		agent:      Agent{Enabled: true, Paused: true, MonthlyCostCapCents: capOf(1), AllowedScopes: []string{"write:task"}},
		req:        Request{RequiredScope: "read:workspace", SpentCentsMonth: 1, EstimatedCents: 0},
		wantDecide: Decision{Outcome: OutcomePause, Cause: CausePaused, Reason: "agent is paused"},
		wantAccess: Decision{Outcome: OutcomePause, Cause: CausePaused, Reason: "agent is paused"},
	},
	{
		name:       "disabled/paused/scopes-contains/required-none/cap-nil/spend-just-under/est-exceeds",
		agent:      Agent{Enabled: false, Paused: true, MonthlyCostCapCents: nil, AllowedScopes: []string{"read:workspace", "write:task"}},
		req:        Request{RequiredScope: "", SpentCentsMonth: 50, EstimatedCents: 1000},
		wantDecide: Decision{Outcome: OutcomePause, Cause: CauseDisabled, Reason: "agent is disabled"},
		wantAccess: Decision{Outcome: OutcomePause, Cause: CauseDisabled, Reason: "agent is disabled"},
	},
	{
		name:       "disabled/paused/scopes-excludes/required-none/cap-1/spend-above/est-zero",
		agent:      Agent{Enabled: false, Paused: true, MonthlyCostCapCents: capOf(1), AllowedScopes: []string{"write:task"}},
		req:        Request{RequiredScope: "", SpentCentsMonth: 11, EstimatedCents: 0},
		wantDecide: Decision{Outcome: OutcomePause, Cause: CauseDisabled, Reason: "agent is disabled"},
		wantAccess: Decision{Outcome: OutcomePause, Cause: CauseDisabled, Reason: "agent is disabled"},
	},
	{
		name:       "enabled/paused/scopes-empty/required-read/cap-0/spend-zero/est-exceeds",
		agent:      Agent{Enabled: true, Paused: true, MonthlyCostCapCents: capOf(0), AllowedScopes: []string{}},
		req:        Request{RequiredScope: "read:workspace", SpentCentsMonth: 0, EstimatedCents: 1},
		wantDecide: Decision{Outcome: OutcomePause, Cause: CausePaused, Reason: "agent is paused"},
		wantAccess: Decision{Outcome: OutcomePause, Cause: CausePaused, Reason: "agent is paused"},
	},
	{
		name:       "disabled/active/scopes-excludes/required-read/cap-1/spend-zero/est-exceeds",
		agent:      Agent{Enabled: false, Paused: false, MonthlyCostCapCents: capOf(1), AllowedScopes: []string{"write:task"}},
		req:        Request{RequiredScope: "read:workspace", SpentCentsMonth: 0, EstimatedCents: 2},
		wantDecide: Decision{Outcome: OutcomePause, Cause: CauseDisabled, Reason: "agent is disabled"},
		wantAccess: Decision{Outcome: OutcomePause, Cause: CauseDisabled, Reason: "agent is disabled"},
	},
	{
		name:       "enabled/active/scopes-contains/required-none/cap-0/spend-above/est-fits-exactly",
		agent:      Agent{Enabled: true, Paused: false, MonthlyCostCapCents: capOf(0), AllowedScopes: []string{"read:workspace", "write:task"}},
		req:        Request{RequiredScope: "", SpentCentsMonth: 10, EstimatedCents: 0},
		wantDecide: Decision{Outcome: OutcomePause, Cause: CauseCostCapExhausted, Reason: "monthly cost cap exhausted"},
		wantAccess: Decision{Outcome: OutcomeAllow, Cause: CauseNone, Reason: "within policy"},
	},
	{
		name:       "disabled/paused/scopes-empty/required-read/cap-nil/spend-just-under/est-fits-exactly",
		agent:      Agent{Enabled: false, Paused: true, MonthlyCostCapCents: nil, AllowedScopes: []string{}},
		req:        Request{RequiredScope: "read:workspace", SpentCentsMonth: 50, EstimatedCents: 25},
		wantDecide: Decision{Outcome: OutcomePause, Cause: CauseDisabled, Reason: "agent is disabled"},
		wantAccess: Decision{Outcome: OutcomePause, Cause: CauseDisabled, Reason: "agent is disabled"},
	},
	{
		name:       "enabled/active/scopes-contains/required-read/cap-100/spend-at-effective/est-exceeds",
		agent:      Agent{Enabled: true, Paused: false, MonthlyCostCapCents: capOf(100), AllowedScopes: []string{"read:workspace", "write:task"}},
		req:        Request{RequiredScope: "read:workspace", SpentCentsMonth: 95, EstimatedCents: 1},
		wantDecide: Decision{Outcome: OutcomePause, Cause: CauseCostCapExhausted, Reason: "monthly cost cap exhausted"},
		wantAccess: Decision{Outcome: OutcomeAllow, Cause: CauseNone, Reason: "within policy"},
	},
	{
		name:       "disabled/paused/scopes-nil/required-read/cap-nil/spend-zero/est-zero",
		agent:      Agent{Enabled: false, Paused: true, MonthlyCostCapCents: nil, AllowedScopes: nil},
		req:        Request{RequiredScope: "read:workspace", SpentCentsMonth: 0, EstimatedCents: 0},
		wantDecide: Decision{Outcome: OutcomePause, Cause: CauseDisabled, Reason: "agent is disabled"},
		wantAccess: Decision{Outcome: OutcomePause, Cause: CauseDisabled, Reason: "agent is disabled"},
	},
	{
		name:       "enabled/paused/scopes-nil/required-none/cap-1/spend-just-under/est-zero",
		agent:      Agent{Enabled: true, Paused: true, MonthlyCostCapCents: capOf(1), AllowedScopes: nil},
		req:        Request{RequiredScope: "", SpentCentsMonth: 0, EstimatedCents: 0},
		wantDecide: Decision{Outcome: OutcomePause, Cause: CausePaused, Reason: "agent is paused"},
		wantAccess: Decision{Outcome: OutcomePause, Cause: CausePaused, Reason: "agent is paused"},
	},
	{
		name:       "enabled/active/scopes-empty/required-none/cap-100/spend-above/est-exceeds",
		agent:      Agent{Enabled: true, Paused: false, MonthlyCostCapCents: capOf(100), AllowedScopes: []string{}},
		req:        Request{RequiredScope: "", SpentCentsMonth: 105, EstimatedCents: 1},
		wantDecide: Decision{Outcome: OutcomePause, Cause: CauseCostCapExhausted, Reason: "monthly cost cap exhausted"},
		wantAccess: Decision{Outcome: OutcomeAllow, Cause: CauseNone, Reason: "within policy"},
	},
	{
		name:       "enabled/active/scopes-excludes/required-read/cap-0/spend-just-under/est-fits-exactly",
		agent:      Agent{Enabled: true, Paused: false, MonthlyCostCapCents: capOf(0), AllowedScopes: []string{"write:task"}},
		req:        Request{RequiredScope: "read:workspace", SpentCentsMonth: 0, EstimatedCents: 0},
		wantDecide: Decision{Outcome: OutcomeDeny, Cause: CauseScopeNotAllowed, Reason: "scope read:workspace is not in the agent's allow list"},
		wantAccess: Decision{Outcome: OutcomeDeny, Cause: CauseScopeNotAllowed, Reason: "scope read:workspace is not in the agent's allow list"},
	},
	{
		name:       "disabled/paused/scopes-nil/required-none/cap-0/spend-at-effective/est-exceeds",
		agent:      Agent{Enabled: false, Paused: true, MonthlyCostCapCents: capOf(0), AllowedScopes: nil},
		req:        Request{RequiredScope: "", SpentCentsMonth: 0, EstimatedCents: 1},
		wantDecide: Decision{Outcome: OutcomePause, Cause: CauseDisabled, Reason: "agent is disabled"},
		wantAccess: Decision{Outcome: OutcomePause, Cause: CauseDisabled, Reason: "agent is disabled"},
	},
	{
		name:       "disabled/paused/scopes-excludes/required-none/cap-nil/spend-at-effective/est-zero",
		agent:      Agent{Enabled: false, Paused: true, MonthlyCostCapCents: nil, AllowedScopes: []string{"write:task"}},
		req:        Request{RequiredScope: "", SpentCentsMonth: 95, EstimatedCents: 0},
		wantDecide: Decision{Outcome: OutcomePause, Cause: CauseDisabled, Reason: "agent is disabled"},
		wantAccess: Decision{Outcome: OutcomePause, Cause: CauseDisabled, Reason: "agent is disabled"},
	},
	{
		name:       "disabled/paused/scopes-contains/required-read/cap-1/spend-zero/est-zero",
		agent:      Agent{Enabled: false, Paused: true, MonthlyCostCapCents: capOf(1), AllowedScopes: []string{"read:workspace", "write:task"}},
		req:        Request{RequiredScope: "read:workspace", SpentCentsMonth: 0, EstimatedCents: 0},
		wantDecide: Decision{Outcome: OutcomePause, Cause: CauseDisabled, Reason: "agent is disabled"},
		wantAccess: Decision{Outcome: OutcomePause, Cause: CauseDisabled, Reason: "agent is disabled"},
	},
	{
		name:       "enabled/active/scopes-empty/required-read/cap-1/spend-zero/est-exceeds",
		agent:      Agent{Enabled: true, Paused: false, MonthlyCostCapCents: capOf(1), AllowedScopes: []string{}},
		req:        Request{RequiredScope: "read:workspace", SpentCentsMonth: 0, EstimatedCents: 2},
		wantDecide: Decision{Outcome: OutcomePause, Cause: CauseCostCapWouldExceed, Reason: "call would exceed monthly cost cap"},
		wantAccess: Decision{Outcome: OutcomeAllow, Cause: CauseNone, Reason: "within policy"},
	},
	{
		name:       "disabled/active/scopes-excludes/required-read/cap-100/spend-at-effective/est-exceeds",
		agent:      Agent{Enabled: false, Paused: false, MonthlyCostCapCents: capOf(100), AllowedScopes: []string{"write:task"}},
		req:        Request{RequiredScope: "read:workspace", SpentCentsMonth: 95, EstimatedCents: 1},
		wantDecide: Decision{Outcome: OutcomePause, Cause: CauseDisabled, Reason: "agent is disabled"},
		wantAccess: Decision{Outcome: OutcomePause, Cause: CauseDisabled, Reason: "agent is disabled"},
	},
}

// boundaryCases pin the edges of the cost and scope rules. A
// combinatorial model works in value classes and cannot know that the
// interesting spend is the one that lands exactly on the effective cap,
// that a cap of 1 is floored back up after the 95% margin rounds it to
// zero, or that an empty allow list has to behave like an absent one
// rather than like a list nothing is in.
var boundaryCases = []guardCase{
	{
		// 95% of 10_000 is 9_500, and spend plus estimate reaching it
		// exactly is still within policy: the rule is a strict
		// greater-than, so the last cent under the margin is spendable.
		name:       "boundary/estimate exactly fills the 95% margin",
		agent:      Agent{Enabled: true, MonthlyCostCapCents: capOf(10_000)},
		req:        Request{SpentCentsMonth: 9_000, EstimatedCents: 500},
		wantDecide: Decision{Outcome: OutcomeAllow, Cause: CauseNone, Reason: "within policy"},
		wantAccess: Decision{Outcome: OutcomeAllow, Cause: CauseNone, Reason: "within policy"},
	},
	{
		name:       "boundary/one cent past the 95% margin",
		agent:      Agent{Enabled: true, MonthlyCostCapCents: capOf(10_000)},
		req:        Request{SpentCentsMonth: 9_000, EstimatedCents: 501},
		wantDecide: Decision{Outcome: OutcomePause, Cause: CauseCostCapWouldExceed, Reason: "call would exceed monthly cost cap"},
		wantAccess: Decision{Outcome: OutcomeAllow, Cause: CauseNone, Reason: "within policy"},
	},
	{
		name:       "boundary/spend lands exactly on the effective cap",
		agent:      Agent{Enabled: true, MonthlyCostCapCents: capOf(10_000)},
		req:        Request{SpentCentsMonth: 9_500, EstimatedCents: 0},
		wantDecide: Decision{Outcome: OutcomePause, Cause: CauseCostCapExhausted, Reason: "monthly cost cap exhausted"},
		wantAccess: Decision{Outcome: OutcomeAllow, Cause: CauseNone, Reason: "within policy"},
	},
	{
		// A cap of zero is a cap, not the absence of one: an agent
		// configured with it has nothing to spend from the first call.
		name:       "boundary/cap of zero refuses a free call",
		agent:      Agent{Enabled: true, MonthlyCostCapCents: capOf(0)},
		req:        Request{SpentCentsMonth: 0, EstimatedCents: 0},
		wantDecide: Decision{Outcome: OutcomePause, Cause: CauseCostCapExhausted, Reason: "monthly cost cap exhausted"},
		wantAccess: Decision{Outcome: OutcomeAllow, Cause: CauseNone, Reason: "within policy"},
	},
	{
		// 95% of 1 truncates to 0, which would make a cap of one cent
		// behave like a cap of zero. The floor lifts it back to 1, so
		// the first cent is still spendable.
		name:       "boundary/cap of one is floored back above zero",
		agent:      Agent{Enabled: true, MonthlyCostCapCents: capOf(1)},
		req:        Request{SpentCentsMonth: 0, EstimatedCents: 1},
		wantDecide: Decision{Outcome: OutcomeAllow, Cause: CauseNone, Reason: "within policy"},
		wantAccess: Decision{Outcome: OutcomeAllow, Cause: CauseNone, Reason: "within policy"},
	},
	{
		name:       "boundary/cap of one is exhausted by one cent spent",
		agent:      Agent{Enabled: true, MonthlyCostCapCents: capOf(1)},
		req:        Request{SpentCentsMonth: 1, EstimatedCents: 0},
		wantDecide: Decision{Outcome: OutcomePause, Cause: CauseCostCapExhausted, Reason: "monthly cost cap exhausted"},
		wantAccess: Decision{Outcome: OutcomeAllow, Cause: CauseNone, Reason: "within policy"},
	},
	{
		// A nil cap is unlimited, not zero: no spend and no estimate
		// can reach a cost cause.
		name:       "boundary/nil cap never reaches a cost cause",
		agent:      Agent{Enabled: true, MonthlyCostCapCents: nil},
		req:        Request{SpentCentsMonth: 9_999_999, EstimatedCents: 9_999_999},
		wantDecide: Decision{Outcome: OutcomeAllow, Cause: CauseNone, Reason: "within policy"},
		wantAccess: Decision{Outcome: OutcomeAllow, Cause: CauseNone, Reason: "within policy"},
	},
	{
		// nil means "inherit the caller token's scopes", so the guard
		// has nothing of its own to check against.
		name:       "boundary/nil allow list admits any required scope",
		agent:      Agent{Enabled: true, AllowedScopes: nil},
		req:        Request{RequiredScope: "write:workspace"},
		wantDecide: Decision{Outcome: OutcomeAllow, Cause: CauseNone, Reason: "within policy"},
		wantAccess: Decision{Outcome: OutcomeAllow, Cause: CauseNone, Reason: "within policy"},
	},
	{
		// An empty list has to read the same way as a nil one. Treating
		// it as a list nothing is in would deny every scoped call for an
		// agent whose scopes were merely never configured.
		name:       "boundary/empty allow list admits any required scope",
		agent:      Agent{Enabled: true, AllowedScopes: []string{}},
		req:        Request{RequiredScope: "write:workspace"},
		wantDecide: Decision{Outcome: OutcomeAllow, Cause: CauseNone, Reason: "within policy"},
		wantAccess: Decision{Outcome: OutcomeAllow, Cause: CauseNone, Reason: "within policy"},
	},
	{
		// A request naming no scope asks for nothing the allow list
		// governs, so an allow list it is absent from is not a refusal.
		name:       "boundary/empty required scope is not measured against the allow list",
		agent:      Agent{Enabled: true, AllowedScopes: []string{"write:task"}},
		req:        Request{RequiredScope: ""},
		wantDecide: Decision{Outcome: OutcomeAllow, Cause: CauseNone, Reason: "within policy"},
		wantAccess: Decision{Outcome: OutcomeAllow, Cause: CauseNone, Reason: "within policy"},
	},
	{
		// The scope rule is exact-match, not prefix-match.
		name:       "boundary/required scope is a prefix of an allowed one",
		agent:      Agent{Enabled: true, AllowedScopes: []string{"read:workspace:members"}},
		req:        Request{RequiredScope: "read:workspace"},
		wantDecide: Decision{Outcome: OutcomeDeny, Cause: CauseScopeNotAllowed, Reason: "scope read:workspace is not in the agent's allow list"},
		wantAccess: Decision{Outcome: OutcomeDeny, Cause: CauseScopeNotAllowed, Reason: "scope read:workspace is not in the agent's allow list"},
	},
	{
		// Disabled outranks paused, and both outrank an exhausted
		// budget: a caller answering a client has to be told the agent
		// was switched off, not that it ran out of money.
		name:       "boundary/disabled outranks paused and an exhausted cap",
		agent:      Agent{Enabled: false, Paused: true, MonthlyCostCapCents: capOf(100), AllowedScopes: []string{"write:task"}},
		req:        Request{RequiredScope: "read:workspace", SpentCentsMonth: 1_000, EstimatedCents: 1_000},
		wantDecide: Decision{Outcome: OutcomePause, Cause: CauseDisabled, Reason: "agent is disabled"},
		wantAccess: Decision{Outcome: OutcomePause, Cause: CauseDisabled, Reason: "agent is disabled"},
	},
	{
		name:       "boundary/paused outranks a rejected scope and an exhausted cap",
		agent:      Agent{Enabled: true, Paused: true, MonthlyCostCapCents: capOf(100), AllowedScopes: []string{"write:task"}},
		req:        Request{RequiredScope: "read:workspace", SpentCentsMonth: 1_000, EstimatedCents: 1_000},
		wantDecide: Decision{Outcome: OutcomePause, Cause: CausePaused, Reason: "agent is paused"},
		wantAccess: Decision{Outcome: OutcomePause, Cause: CausePaused, Reason: "agent is paused"},
	},
	{
		name:       "boundary/rejected scope outranks an exhausted cap",
		agent:      Agent{Enabled: true, MonthlyCostCapCents: capOf(100), AllowedScopes: []string{"write:task"}},
		req:        Request{RequiredScope: "read:workspace", SpentCentsMonth: 1_000, EstimatedCents: 1_000},
		wantDecide: Decision{Outcome: OutcomeDeny, Cause: CauseScopeNotAllowed, Reason: "scope read:workspace is not in the agent's allow list"},
		wantAccess: Decision{Outcome: OutcomeDeny, Cause: CauseScopeNotAllowed, Reason: "scope read:workspace is not in the agent's allow list"},
	},
	{
		// The exhausted rule is checked before the would-exceed one, so
		// spend already at the cap is reported as exhausted whatever the
		// call was going to add.
		name:       "boundary/exhausted outranks would-exceed",
		agent:      Agent{Enabled: true, MonthlyCostCapCents: capOf(10_000)},
		req:        Request{SpentCentsMonth: 9_600, EstimatedCents: 5_000},
		wantDecide: Decision{Outcome: OutcomePause, Cause: CauseCostCapExhausted, Reason: "monthly cost cap exhausted"},
		wantAccess: Decision{Outcome: OutcomeAllow, Cause: CauseNone, Reason: "within policy"},
	},
}

// TestDecideAndDecideAccessCauses asserts the full Decision — outcome,
// cause and reason together — that each entry point returns.
//
// Both are driven from the same table because they share the access
// rules: DecideAccess answers the access half alone, Decide answers it
// and then the cost half, and a table that exercised only one of them
// would not notice the other losing a cause. The cost rows are what pins
// the split: the same input that pauses Decide for an exhausted budget
// has to come back from DecideAccess as an allow, because a cost cap is
// not an access rule.
func TestDecideAndDecideAccessCauses(t *testing.T) {
	t.Parallel()

	for _, tc := range slices.Concat(pairwiseCases, boundaryCases) {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			assertDecision(t, "Decide", Decide(tc.agent, tc.req), tc.wantDecide)
			assertDecision(t, "DecideAccess", DecideAccess(tc.agent, tc.req.RequiredScope), tc.wantAccess)
		})
	}
}

// TestEveryCauseIsReachable proves the table above actually produces
// each declared cause. A cause no input reaches is a claim the code
// makes and cannot keep, and a table that quietly stopped covering one
// would otherwise read as though it still did.
func TestEveryCauseIsReachable(t *testing.T) {
	t.Parallel()

	fromDecide := map[Cause]bool{}
	fromAccess := map[Cause]bool{}
	for _, tc := range slices.Concat(pairwiseCases, boundaryCases) {
		fromDecide[Decide(tc.agent, tc.req).Cause] = true
		fromAccess[DecideAccess(tc.agent, tc.req.RequiredScope).Cause] = true
	}

	for _, c := range []Cause{
		CauseNone, CauseDisabled, CausePaused, CauseScopeNotAllowed,
		CauseCostCapExhausted, CauseCostCapWouldExceed,
	} {
		if !fromDecide[c] {
			t.Errorf("no input in the table makes Decide return %s", causeName(c))
		}
	}

	// DecideAccess evaluates the access rules only, so the cost causes
	// are correctly out of its reach; the access causes must all be in
	// it.
	for _, c := range []Cause{CauseNone, CauseDisabled, CausePaused, CauseScopeNotAllowed} {
		if !fromAccess[c] {
			t.Errorf("no input in the table makes DecideAccess return %s", causeName(c))
		}
	}
	for _, c := range []Cause{CauseCostCapExhausted, CauseCostCapWouldExceed} {
		if fromAccess[c] {
			t.Errorf("DecideAccess returned %s; the cost rules belong to Decide alone, and a "+
				"caller that granted the agent no billable work would be refused for a budget "+
				"it never draws on", causeName(c))
		}
	}
}

// causelessDecision is a Decision composite literal that returns a
// non-allow outcome without naming the rule that produced it.
type causelessDecision struct {
	File    string
	Line    int
	Outcome string
	Cause   string // the value as written; empty when the field is absent
}

// TestEveryNonAllowDecisionNamesItsCause reads the guard's own source
// and refuses a non-allow Decision literal that leaves Cause at its zero
// value or sets it to something other than one of the declared cause
// constants.
//
// A rule added tomorrow that returns a bare Decision{Outcome:
// OutcomePause} compiles, passes every behavioural test that looks only
// at Outcome, and reaches callers as CauseNone — which they read as "no
// rule fired" and answer with their safe fallback, so the new rule is
// invisible in exactly the place its answer was needed.
//
// Every non-test file in the package directory is read, found by reading
// the directory rather than by name: naming the one file that holds the
// rules today is an enumeration of one, and a rule split into a second
// file tomorrow would return decisions this check never looked at.
func TestEveryNonAllowDecisionNamesItsCause(t *testing.T) {
	t.Parallel()

	filenames, err := packageSources(".")
	if err != nil {
		t.Fatalf("list package sources: %v", err)
	}
	if len(filenames) == 0 {
		t.Fatal("no non-test .go file found in the package directory; the walk has stopped " +
			"matching rather than the sources having gone away")
	}

	fset := token.NewFileSet()
	files := make(map[string]*ast.File, len(filenames))
	for _, name := range filenames {
		src, readErr := os.ReadFile(name) //#nosec G304 -- package directory entry read at test time
		if readErr != nil {
			t.Fatalf("read %s: %v", name, readErr)
		}
		file, parseErr := parser.ParseFile(fset, name, src, parser.SkipObjectResolution)
		if parseErr != nil {
			t.Fatalf("parse %s: %v", name, parseErr)
		}
		files[name] = file
	}

	// The constants may be declared in a different file from the rules
	// that use them, so the harvest is the union over the package.
	causes := map[string]bool{}
	for _, file := range files {
		for name := range declaredCauses(file) {
			causes[name] = true
		}
	}
	if len(causes) == 0 {
		t.Fatal("the package declares no Cause constants; the harvest has stopped matching " +
			"rather than the constants having gone away")
	}

	var findings []causelessDecision
	inspected := 0
	for _, name := range filenames {
		f, n := scanDecisions(fset, files[name], name, causes)
		findings = append(findings, f...)
		inspected += n
	}
	if inspected == 0 {
		t.Fatalf("no Decision composite literal in any of the %d package sources; the scan has "+
			"stopped matching rather than the literals having gone away", len(filenames))
	}
	t.Logf("checked %d Decision literals across %d package sources (%s) against %d declared "+
		"cause constants", inspected, len(filenames), strings.Join(filenames, ", "), len(causes))

	for _, f := range findings {
		t.Errorf("%s:%d: this Decision returns %s but %s. Every non-allow return has to name "+
			"the rule that produced it, or callers read it as CauseNone, answer with their "+
			"fallback, and the rule never reaches the client",
			f.File, f.Line, f.Outcome, describeMissingCause(f.Cause))
	}
}

// packageSources returns the non-test .go files in dir, in a stable
// order.
func packageSources(dir string) ([]string, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	var out []string
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		out = append(out, name)
	}
	slices.Sort(out)
	return out, nil
}

// TestScanSeesACauselessDecision is the positive control. It runs the
// same detector over a source snippet that is known to contain the
// shape, pinned by line, so the repository check above cannot pass
// because the detector quietly stopped matching. The snippet carries the
// near misses too — a cause spelled CauseNone, a cause read from a
// variable, and the allow paths that legitimately name none.
func TestScanSeesACauselessDecision(t *testing.T) {
	t.Parallel()

	const src = `package p

type Cause int

const (
	CauseNone Cause = iota
	CauseDisabled
	CausePaused
)

// pauseWithoutCause names no rule at all.
func pauseWithoutCause() Decision {
	return Decision{Outcome: OutcomePause, Reason: "agent is disabled"}
}

// denyClaimingNoRuleFired sets the field to the zero value, which says
// the decision was an allow.
func denyClaimingNoRuleFired() Decision {
	return Decision{Outcome: OutcomeDeny, Cause: CauseNone, Reason: "scope is not allowed"}
}

// pauseWithVariableCause carries a value that is not one of the
// declared constants, so no caller can branch on it exhaustively.
func pauseWithVariableCause(c Cause) Decision {
	return Decision{Outcome: OutcomePause, Cause: c, Reason: "dynamic"}
}

// pauseWithCause is the shape every non-allow return has to have.
func pauseWithCause() Decision {
	return Decision{Outcome: OutcomePause, Cause: CauseDisabled, Reason: "agent is disabled"}
}

// allowPath names no rule and needs none.
func allowPath() Decision {
	return Decision{Outcome: OutcomeAllow, Cause: CauseNone, Reason: "within policy"}
}

// zeroValueAllow leaves the outcome at its zero value, which is an allow.
func zeroValueAllow() Decision {
	return Decision{Reason: "within policy"}
}
`

	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "sample.go", src, parser.SkipObjectResolution)
	if err != nil {
		t.Fatalf("parse sample: %v", err)
	}

	causes := declaredCauses(file)
	if want := []string{"CauseDisabled", "CauseNone", "CausePaused"}; !slices.Equal(sortedKeys(causes), want) {
		t.Errorf("declared causes = %v, want %v", sortedKeys(causes), want)
	}

	findings, inspected := scanDecisions(fset, file, "sample.go", causes)
	if want := 6; inspected != want {
		t.Errorf("inspected %d Decision literals, want %d", inspected, want)
	}

	var lines []int
	for _, f := range findings {
		lines = append(lines, f.Line)
	}
	// The pause with no Cause field, the deny that sets CauseNone, and
	// the pause whose cause is a variable rather than a declared
	// constant.
	if want := []int{13, 19, 25}; !slices.Equal(lines, want) {
		t.Fatalf("reported lines %v, want %v", lines, want)
	}
	if got, want := findings[0].Cause, ""; got != want {
		t.Errorf("findings[0].Cause = %q, want %q for a literal with no Cause field", got, want)
	}
	if got, want := findings[1].Cause, "CauseNone"; got != want {
		t.Errorf("findings[1].Cause = %q, want %q", got, want)
	}
	if got, want := findings[0].Outcome, "OutcomePause"; got != want {
		t.Errorf("findings[0].Outcome = %q, want %q", got, want)
	}
}

// declaredCauses returns the names in the file's `Cause` constant block,
// so the check follows the enum rather than a list kept next to it.
func declaredCauses(file *ast.File) map[string]bool {
	out := map[string]bool{}
	for _, decl := range file.Decls {
		gen, ok := decl.(*ast.GenDecl)
		if !ok || gen.Tok != token.CONST {
			continue
		}
		// Only the first spec of an iota block carries the type; the
		// ones after it inherit, so the type is tracked across specs.
		inBlock := false
		for _, spec := range gen.Specs {
			value, ok := spec.(*ast.ValueSpec)
			if !ok {
				continue
			}
			if value.Type != nil {
				id, isIdent := value.Type.(*ast.Ident)
				inBlock = isIdent && id.Name == "Cause"
			}
			if !inBlock {
				continue
			}
			for _, name := range value.Names {
				out[name.Name] = true
			}
		}
	}
	return out
}

// scanDecisions returns every Decision composite literal in the file
// that returns a non-allow outcome without naming one of causes, along
// with the number of Decision literals it looked at. The count is what
// tells a scan that found nothing apart from a scan that has stopped
// matching.
func scanDecisions(fset *token.FileSet, file *ast.File, relpath string, causes map[string]bool) ([]causelessDecision, int) {
	var out []causelessDecision
	inspected := 0

	ast.Inspect(file, func(n ast.Node) bool {
		lit, ok := n.(*ast.CompositeLit)
		if !ok || !isDecisionType(lit.Type) {
			return true
		}
		inspected++

		outcome, hasOutcome := fieldValue(lit, "Outcome")
		if !hasOutcome || outcome == "OutcomeAllow" {
			// An absent Outcome is the zero value, OutcomeAllow.
			return true
		}
		cause, hasCause := fieldValue(lit, "Cause")
		if hasCause && cause != "CauseNone" && causes[cause] {
			return true
		}
		if !hasCause {
			cause = ""
		}
		out = append(out, causelessDecision{
			File: relpath, Line: fset.Position(lit.Pos()).Line,
			Outcome: outcome, Cause: cause,
		})
		return true
	})
	return out, inspected
}

// isDecisionType reports whether a composite literal's type is Decision,
// written either bare inside the package or qualified from outside it.
func isDecisionType(e ast.Expr) bool {
	switch t := e.(type) {
	case *ast.Ident:
		return t.Name == "Decision"
	case *ast.SelectorExpr:
		return t.Sel.Name == "Decision"
	}
	return false
}

// fieldValue returns the value written for a keyed field, rendered as
// source text.
func fieldValue(lit *ast.CompositeLit, field string) (string, bool) {
	for _, elt := range lit.Elts {
		kv, ok := elt.(*ast.KeyValueExpr)
		if !ok {
			continue
		}
		key, ok := kv.Key.(*ast.Ident)
		if !ok || key.Name != field {
			continue
		}
		return renderExpr(kv.Value), true
	}
	return "", false
}

// renderExpr renders an expression as source text.
func renderExpr(e ast.Expr) string {
	var buf strings.Builder
	// format.Node fails only on a broken writer for an ast.Expr.
	_ = format.Node(&buf, token.NewFileSet(), e)
	return buf.String()
}

// describeMissingCause phrases the failure for the two ways a cause goes
// missing, which need different fixes.
func describeMissingCause(cause string) string {
	switch cause {
	case "":
		return "sets no Cause, leaving it at CauseNone"
	case "CauseNone":
		return "sets Cause to CauseNone"
	default:
		return fmt.Sprintf("sets Cause to %s, which is not one of the declared cause constants", cause)
	}
}

func sortedKeys(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	slices.Sort(out)
	return out
}

func assertDecision(t *testing.T, call string, got, want Decision) {
	t.Helper()
	if got != want {
		t.Errorf("%s = {Outcome: %s, Cause: %s, Reason: %q}, want {Outcome: %s, Cause: %s, Reason: %q}",
			call, outcomeName(got.Outcome), causeName(got.Cause), got.Reason,
			outcomeName(want.Outcome), causeName(want.Cause), want.Reason)
	}
}

func outcomeName(o Outcome) string {
	switch o {
	case OutcomeAllow:
		return "OutcomeAllow"
	case OutcomeDeny:
		return "OutcomeDeny"
	case OutcomePause:
		return "OutcomePause"
	default:
		return fmt.Sprintf("Outcome(%d)", int(o))
	}
}

func causeName(c Cause) string {
	switch c {
	case CauseNone:
		return "CauseNone"
	case CauseDisabled:
		return "CauseDisabled"
	case CausePaused:
		return "CausePaused"
	case CauseScopeNotAllowed:
		return "CauseScopeNotAllowed"
	case CauseCostCapExhausted:
		return "CauseCostCapExhausted"
	case CauseCostCapWouldExceed:
		return "CauseCostCapWouldExceed"
	default:
		return fmt.Sprintf("Cause(%d)", int(c))
	}
}
