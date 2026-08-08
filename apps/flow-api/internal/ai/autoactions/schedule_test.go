package autoactions

import (
	"testing"
	"time"
)

// The interval a workspace configures has to be the interval it gets.
// It was read out of ai_settings and then never used: a tenant who set
// sixty minutes saw sixty minutes in the UI, got a 200 back, and had
// their tasks changed every five.
func TestWorkspaceIntervalIsHonoured(t *testing.T) {
	t.Parallel()
	e := &Executor{}
	target := workspaceTarget{id: 1, interval: time.Hour}
	base := time.Unix(1_700_000_000, 0)

	if !e.due(target, base) {
		t.Fatal("a workspace that has never been evaluated is due")
	}
	for _, elapsed := range []time.Duration{time.Minute, 5 * time.Minute, 30 * time.Minute, 58 * time.Minute} {
		if e.due(target, base.Add(elapsed)) {
			t.Fatalf("evaluated again after %s of a 1h interval", elapsed)
		}
	}
	if !e.due(target, base.Add(time.Hour)) {
		t.Fatal("not evaluated after the configured interval elapsed")
	}
}

// Zero is documented on the column as "disables". It used to mean the
// opposite in practice — the value was ignored, so the workspace ran on
// every tick.
func TestWorkspaceIntervalZeroDisablesEvaluation(t *testing.T) {
	t.Parallel()
	e := &Executor{}
	target := workspaceTarget{id: 1, interval: 0}
	base := time.Unix(1_700_000_000, 0)

	if e.due(target, base) {
		t.Fatal("interval 0 must disable the workspace, not run it")
	}
	if e.due(target, base.Add(24*time.Hour)) {
		t.Fatal("interval 0 must stay disabled")
	}
}

// Each workspace keeps its own schedule; one tenant's pass must not
// consume another's.
func TestWorkspaceSchedulesAreIndependent(t *testing.T) {
	t.Parallel()
	e := &Executor{}
	base := time.Unix(1_700_000_000, 0)
	fast := workspaceTarget{id: 1, interval: 5 * time.Minute}
	slow := workspaceTarget{id: 2, interval: time.Hour}

	if !e.due(fast, base) || !e.due(slow, base) {
		t.Fatal("both workspaces are due on the first pass")
	}
	at := base.Add(6 * time.Minute)
	if !e.due(fast, at) {
		t.Error("the 5m workspace is due again after 6 minutes")
	}
	if e.due(slow, at) {
		t.Error("the 1h workspace is not due after 6 minutes")
	}
}

// Ticks are not perfectly spaced. Without slack, a pass arriving a
// hair early would push the workspace out by a whole further interval,
// so a five-minute setting would intermittently behave like ten.
func TestASlightlyEarlyPassStillCounts(t *testing.T) {
	t.Parallel()
	e := &Executor{}
	target := workspaceTarget{id: 1, interval: 5 * time.Minute}
	base := time.Unix(1_700_000_000, 0)

	if !e.due(target, base) {
		t.Fatal("first pass is due")
	}
	if !e.due(target, base.Add(5*time.Minute-time.Second)) {
		t.Fatal("a pass one second early was dropped, costing a whole interval")
	}
}

// The schedule is keyed by workspace, so a disabled or deleted tenant
// must not keep an entry for the life of the process.
func TestForgottenWorkspacesLeaveTheSchedule(t *testing.T) {
	t.Parallel()
	e := &Executor{}
	base := time.Unix(1_700_000_000, 0)
	kept := workspaceTarget{id: 1, interval: time.Minute}
	gone := workspaceTarget{id: 2, interval: time.Minute}

	e.due(kept, base)
	e.due(gone, base)
	e.forgetWorkspacesOutside([]workspaceTarget{kept})

	e.lastRunMu.Lock()
	_, keptHeld := e.lastRun[kept.id]
	_, goneHeld := e.lastRun[gone.id]
	e.lastRunMu.Unlock()

	if !keptHeld {
		t.Error("a workspace still in the pass lost its schedule")
	}
	if goneHeld {
		t.Error("a workspace no longer in the pass kept its schedule")
	}
}
