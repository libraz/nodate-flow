package reconciler

import (
	"context"
	"database/sql/driver"
	"encoding/json"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/types"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/eventbus"
)

// A heal is a change to a task made by nobody: the loop moves a deadline
// the user never touched, and the event it appends is the only place that
// change is visible from. What it must not do is claim one that did not
// happen — the heal's predicate carries `AND enabled`, so a task disabled
// between the scan and the write, or one another writer has already
// brought into line, matches no row. Nothing moved there, and an event
// saying otherwise lands on the task's timeline again on every pass.
//
// The two tests below drive the same drifted pair through the same scan,
// differing only in what the stub driver reports as the affected-row
// count. That pairing is the evidence: the absence of the event in the
// zero-row case says nothing on its own, and only means something because
// the one-row case shows the event is produced from these inputs.

// The pair the scan finds. The ids are the public ones because they are
// what a payload may carry; the internal ids stay in the events row's own
// columns.
var (
	driftTaskPublicID  = [16]byte{0x01, 0x94, 0x11, 0x11, 0x11, 0x11, 0x71, 0x11, 0x81, 0x11, 0x11, 0x11, 0x11, 0x11, 0x11, 0x11}
	driftEventPublicID = [16]byte{0x01, 0x94, 0x22, 0x22, 0x22, 0x22, 0x72, 0x22, 0x82, 0x22, 0x22, 0x22, 0x22, 0x22, 0x22, 0x22}
)

const driftWorkspaceID = 7

// onlyDriftScanReturnsARow keeps the pass to a single drifted pair: the
// other two scans page over their own tables and would otherwise report
// drift of their own, which is not what these tests are reading.
func onlyDriftScanReturnsARow(query string) int {
	if strings.Contains(query, "ce.timezone") {
		return 1
	}
	return 0
}

// insertBindings pairs an INSERT's column list with the values bound to
// it, read out of the statement itself so a column order that moves is a
// failure here rather than a silently wrong assertion.
func insertBindings(t *testing.T, q recordedQuery) map[string]driver.Value {
	t.Helper()

	open := strings.Index(q.sql, "(")
	shut := strings.Index(q.sql, ")")
	require.Greater(t, shut, open, "the statement names no column list: %s", q.sql)
	cols := strings.Split(q.sql[open+1:shut], ",")
	require.Len(t, q.args, len(cols), "the statement binds a different number of values than it names columns")

	out := make(map[string]driver.Value, len(cols))
	for i, c := range cols {
		out[strings.TrimSpace(c)] = q.args[i]
	}
	return out
}

// execContaining returns the single statement containing needle.
func execContaining(t *testing.T, d *stubDriver, needle string) recordedQuery {
	t.Helper()

	d.mu.Lock()
	defer d.mu.Unlock()
	var found []recordedQuery
	for _, q := range d.queries {
		if strings.Contains(q.sql, needle) {
			found = append(found, q)
		}
	}
	require.Len(t, found, 1, "want exactly one statement containing %q", needle)
	return found[0]
}

// countStatements reports how many statements contain needle.
func countStatements(d *stubDriver, needle string) int {
	d.mu.Lock()
	defer d.mu.Unlock()
	n := 0
	for _, q := range d.queries {
		if strings.Contains(q.sql, needle) {
			n++
		}
	}
	return n
}

// TestDateDriftHealRecordsTheReconciliationItPerformed is the positive
// control, and it is what makes the absence asserted below evidence
// rather than a test that could never have seen an event at all.
func TestDateDriftHealRecordsTheReconciliationItPerformed(t *testing.T) {
	t.Parallel()

	db, rec := newStubReconciler(t, onlyDriftScanReturnsARow)
	defer db.Close()
	rec.stub.driftDates = true
	rec.stub.healRows = 1

	rec.RunOnce(context.Background())

	require.Equal(t, 1, countStatements(rec.stub, "UPDATE tasks SET due_on"),
		"the drifted pair must be healed")
	require.Equal(t, 1, countStatements(rec.stub, "INSERT INTO events"),
		"a heal nobody is told about is a deadline that moved with no record of who moved it")

	bound := insertBindings(t, execContaining(t, rec.stub, "INSERT INTO events"))
	require.Equal(t, string(eventbus.ItemReconciled), bound["type"])
	require.EqualValues(t, driftWorkspaceID, bound["workspace_id"],
		"the event belongs to the workspace whose pair drifted")

	// A background loop is not a person. A user id here would put
	// somebody's name on a change they did not make, and the system source
	// column names worker ticks, which this is not.
	require.Nil(t, bound["actor_user_id"], "a self-heal has no human actor")
	require.Nil(t, bound["actor_system_source"], "a self-heal has no human actor")

	raw, ok := bound["payload_json"].([]byte)
	require.True(t, ok, "the payload must reach the driver as bytes")
	var payload map[string]any
	require.NoError(t, json.Unmarshal(raw, &payload))
	require.Equal(t, types.PublicID(driftTaskPublicID).String(), payload["taskPublicId"],
		"the payload names the task by its public id")
	require.Equal(t, types.PublicID(driftEventPublicID).String(), payload["eventPublicId"],
		"the payload names the linked event by its public id")
}

// TestDateDriftSaysNothingWhenTheHealMatchedNoRow is the case the count
// answers.
//
// Same pair, same scan, same statements — the write simply matched no
// row, because the task was disabled or brought into line before this
// pass reached it. Nothing here healed anything, so nothing may say it
// did.
func TestDateDriftSaysNothingWhenTheHealMatchedNoRow(t *testing.T) {
	t.Parallel()

	db, rec := newStubReconciler(t, onlyDriftScanReturnsARow)
	defer db.Close()
	rec.stub.driftDates = true
	rec.stub.healRows = 0

	rec.RunOnce(context.Background())

	require.Equal(t, 1, countStatements(rec.stub, "UPDATE tasks SET due_on"),
		"the pass must still attempt the heal; the count is what answers whether it landed")
	require.Equal(t, 0, countStatements(rec.stub, "INSERT INTO events"),
		"a pass that healed nothing recorded a reconciliation; the loop would add another every five minutes")
}
