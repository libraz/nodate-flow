package kindscan_test

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/libraz/nodate-flow/packages/go-shared/kindscan"
)

// scanSQLFixture runs the query rule over the fixture tree.
func scanSQLFixture(t *testing.T) []string {
	t.Helper()

	msgs, err := kindscan.ScanSQL(filepath.Join("testdata", "sqlkinds"))
	if err != nil {
		t.Fatalf("scan testdata: %v", err)
	}
	return msgs
}

// TestScanSQLReportsAKindNothingDeclares covers what the Go rules cannot
// reach. A kind spelled inside a query is a string to the compiler and to
// sqlc alike, so renaming the constant leaves it stale with the build
// green; each form below is a place that can happen.
func TestScanSQLReportsAKindNothingDeclares(t *testing.T) {
	t.Parallel()

	msgs := scanSQLFixture(t)
	for _, want := range []string{
		"task.invented",         // written by an INSERT into events.type
		"notification.invented", // written by an INSERT into notifications.event_type
		"task.imagined",         // compared with !=
		"task.dreamt",           // one member of an IN list
		"task.hallucinated.",    // the fixed prefix of a LIKE pattern
		"task.misfiled",         // assigned by an UPDATE ... SET
	} {
		if !containsValue(msgs, want) {
			t.Errorf("a query naming %q must be reported, got %v", want, msgs)
		}
	}
}

// TestScanSQLAcceptsWhatTheRegistryDeclares is the other side, and the
// half that decides whether the rule is usable: a guard that reports a
// correct query is one people turn off.
func TestScanSQLAcceptsWhatTheRegistryDeclares(t *testing.T) {
	t.Parallel()

	msgs := scanSQLFixture(t)
	for _, unwanted := range []string{
		"task.created",          // written by an INSERT
		"task.updated",          // compared with =
		"task.disabled",         // one member of an IN list
		"task.transition.",      // a LIKE pattern that covers declared kinds
		"nothing.declares.this", // named in a comment, which is no column position
		"resource.invented",     // compared against resource_type, which is not a kind column
		"action.invented",       // compared against action, likewise
		"fixture.deliberate",    // outside the registry on purpose, and marked as such
		"\"task\"",              // the resource_type beside a kind on the same statement
	} {
		if containsValue(msgs, unwanted) {
			t.Errorf("%q must pass, got %v", unwanted, msgs)
		}
	}
}

// TestScanSQLPinsTheCount stops the rule from drifting into reporting
// every string it meets. The fixture holds exactly six wrong names beside
// six right ones, and a rule that answers "six" for the wrong six is the
// only one worth running.
func TestScanSQLPinsTheCount(t *testing.T) {
	t.Parallel()

	msgs := scanSQLFixture(t)
	if len(msgs) != 6 {
		t.Fatalf("want the six undeclared names reported, got %d: %v", len(msgs), msgs)
	}
}

// TestScanSQLNamesTheColumnAndThePlace covers what the message has to
// carry to be actionable: the file and line to open, and which column the
// literal sits against.
func TestScanSQLNamesTheColumnAndThePlace(t *testing.T) {
	t.Parallel()

	for _, msg := range scanSQLFixture(t) {
		if !strings.Contains(msg, "queries.sql:") {
			t.Errorf("the message must name the file and line, got %q", msg)
		}
		if !strings.Contains(msg, "type") {
			t.Errorf("the message must name the column, got %q", msg)
		}
	}
}

// TestScanSQLReportsAnEscapeThatCoversNothing covers the escape's own
// upkeep. A marker outlives the literal that earned it — the query gets
// fixed, the comment stays — and from then on it covers whatever is
// written on that line next, without anyone deciding that.
func TestScanSQLReportsAnEscapeThatCoversNothing(t *testing.T) {
	t.Parallel()

	msgs, err := kindscan.ScanSQL(filepath.Join("testdata", "sqlescape"))
	if err != nil {
		t.Fatalf("scan testdata: %v", err)
	}
	if len(msgs) != 1 {
		t.Fatalf("want only the marker that covers nothing reported, got %d: %v", len(msgs), msgs)
	}
	if !strings.Contains(msgs[0], "stale.sql:11") {
		t.Errorf("the finding must point at the marker that covers nothing, got %q", msgs[0])
	}
	if !strings.Contains(msgs[0], "kindscan:undeclared") {
		t.Errorf("the message must name the marker, got %q", msgs[0])
	}
}

// TestScanSQLRefusesARootWithNoQueries covers the reading a passing guard
// is easiest to confuse with: a scan pointed somewhere with nothing in it
// reports the same empty list as a tree that is in order.
func TestScanSQLRefusesARootWithNoQueries(t *testing.T) {
	t.Parallel()

	if _, err := kindscan.ScanSQL(t.TempDir()); err == nil {
		t.Fatal("a root holding no query must be an error, not an empty result")
	}
}

// containsValue reports whether any message quotes want.
func containsValue(msgs []string, want string) bool {
	for _, msg := range msgs {
		if strings.Contains(msg, want) {
			return true
		}
	}
	return false
}
