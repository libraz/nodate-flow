package agentguard

import "testing"

func capOf(v int64) *int64 { return &v }

func TestDecide_AllowsWithinCap(t *testing.T) {
	t.Parallel()
	d := Decide(Agent{Enabled: true, MonthlyCostCapCents: capOf(10_000)},
		Request{RequiredScope: "read:workspace", EstimatedCents: 50, SpentCentsMonth: 1_000})
	if d.Outcome != OutcomeAllow {
		t.Fatalf("expected allow, got %+v", d)
	}
}

func TestDecide_PausesWhenDisabled(t *testing.T) {
	t.Parallel()
	d := Decide(Agent{Enabled: false}, Request{})
	if d.Outcome != OutcomePause {
		t.Fatalf("expected pause for disabled agent, got %+v", d)
	}
}

func TestDecide_PausesWhenAlreadyPaused(t *testing.T) {
	t.Parallel()
	d := Decide(Agent{Enabled: true, Paused: true}, Request{})
	if d.Outcome != OutcomePause {
		t.Fatalf("expected pause, got %+v", d)
	}
}

func TestDecide_DeniesScopeNotInAllowList(t *testing.T) {
	t.Parallel()
	d := Decide(Agent{Enabled: true, AllowedScopes: []string{"read:workspace"}},
		Request{RequiredScope: "write:workspace"})
	if d.Outcome != OutcomeDeny {
		t.Fatalf("expected deny for scope mismatch, got %+v", d)
	}
}

func TestDecide_AllowsWhenScopeListIsNil(t *testing.T) {
	t.Parallel()
	d := Decide(Agent{Enabled: true}, Request{RequiredScope: "write:workspace"})
	if d.Outcome != OutcomeAllow {
		t.Fatalf("expected allow when allowlist nil, got %+v", d)
	}
}

func TestDecide_PausesWhenCapAlreadyExhausted(t *testing.T) {
	t.Parallel()
	d := Decide(Agent{Enabled: true, MonthlyCostCapCents: capOf(5_000)},
		Request{SpentCentsMonth: 5_000, EstimatedCents: 1})
	if d.Outcome != OutcomePause || d.Reason != "monthly cost cap exhausted" {
		t.Fatalf("expected exhausted pause, got %+v", d)
	}
}

func TestDecide_PausesWhenCallWouldExceedCap(t *testing.T) {
	t.Parallel()
	d := Decide(Agent{Enabled: true, MonthlyCostCapCents: capOf(10_000)},
		Request{SpentCentsMonth: 9_900, EstimatedCents: 200})
	if d.Outcome != OutcomePause || d.Reason != "call would exceed monthly cost cap" {
		t.Fatalf("expected would-exceed pause, got %+v", d)
	}
}
