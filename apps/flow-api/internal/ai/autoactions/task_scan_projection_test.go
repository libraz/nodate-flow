package autoactions

import (
	"strings"
	"testing"
	"time"
)

// The auto-action pass runs on a timer. There is no request behind it and
// no actor whose visibility could scope what it reads, so the only thing
// keeping a private task's words out of reach is that the pass does not
// ask for them.
//
// That is a property of one SELECT and nothing enforces it from outside:
// adding `t.title` back would compile, and every behavioural test in this
// package drives the rule engine through fixtures rather than through the
// statement. So the projection is held to its inputs here.

// contentColumns are the tasks columns whose value says what a task is
// about. Kept as a literal rather than imported from the visibility gate:
// this package must not depend on the test tree, and a column added there
// and not here shows up as the gate reporting a projection this test
// passed.
var contentColumns = []string{"title", "description", "notes"}

// selectList returns the part of the scan before its first FROM, with SQL
// line comments removed — the comments explain the statement and would
// otherwise be searched as though they were projected columns.
func selectList(t *testing.T, stmt string) string {
	t.Helper()
	cut := strings.Index(stmt, "FROM tasks t")
	if cut < 0 {
		t.Fatalf("the scan no longer reads `FROM tasks t`; this check cannot find its select list:\n%s", stmt)
	}
	var kept []string
	for _, line := range strings.Split(stmt[:cut], "\n") {
		if at := strings.Index(line, "--"); at >= 0 {
			line = line[:at]
		}
		kept = append(kept, line)
	}
	return strings.Join(kept, "\n")
}

// TestTaskScanAsksForNoTaskProse is the check.
func TestTaskScanAsksForNoTaskProse(t *testing.T) {
	t.Parallel()

	list := selectList(t, taskScanTemplate)
	for _, column := range contentColumns {
		if strings.Contains(list, column) {
			t.Errorf("the auto-action scan projects tasks.%s. The rule engine decides on "+
				"state, dates and actor facts, so this reaches no reader — but the pass has no "+
				"actor to scope it by either, which makes an unused projection of a task's own "+
				"words the one thing standing between a private task and whatever reads this "+
				"row next:\n%s", column, list)
		}
	}
	// agent_memo is the deliberate exception, and the reason the statement
	// stays in scope of the visibility gate at all. Losing it here would
	// mean the exception was removed and the marker above the scan has
	// outlived what it excuses.
	if !strings.Contains(list, "t.agent_memo") {
		t.Errorf("the scan no longer reads agent_memo; the handoff rule's attempt counters "+
			"come from it, and the visibility marker on the scan is written about it:\n%s", list)
	}
}

// TestTaskScanIsBoundToOneWorkspace pins the bound that stands in for the
// actor the pass does not have.
func TestTaskScanIsBoundToOneWorkspace(t *testing.T) {
	t.Parallel()

	if !strings.Contains(taskScanTemplate, "WHERE t.workspace_id = ?") {
		t.Fatalf("the auto-action scan is not bound to a single workspace. It runs for every "+
			"tenant in turn with no actor to scope it, so the workspace is the only thing "+
			"separating one tenant's rows from another's:\n%s", taskScanTemplate)
	}
}

// TestDecodeAgentMemoIgnoresTheProseAroundTheCounters is what makes the
// scan's one content column defensible.
//
// The existing decoder test feeds a memo containing nothing but the two
// fields the rule reads, which cannot show whether anything else in the
// blob survives the decode. A real memo is written by an agent runtime
// and carries free text; the marker above the scan claims none of it is
// carried forward, so the case with prose in it is the one that says so.
func TestDecodeAgentMemoIgnoresTheProseAroundTheCounters(t *testing.T) {
	t.Parallel()

	memo := []byte(`{"attempts":3,"last_finished_at":1700000000,` +
		`"handoff_reason":"blocked on the private contract review thread"}`)

	attempts, finished := decodeAgentMemo(memo)
	if attempts != 3 {
		t.Errorf("attempts = %d, want 3", attempts)
	}
	if finished.Unix() != 1_700_000_000 {
		t.Errorf("last_finished_at = %d, want 1700000000", finished.Unix())
	}
	if finished.Location() != time.UTC {
		t.Errorf("last_finished_at is in %s; the rule compares it against a UTC clock",
			finished.Location())
	}
}
