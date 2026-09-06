package mutationguard_test

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/libraz/nodate-flow/apps/flow-api/internal/mutationlog/mutationguard"
)

var gates = map[string]bool{
	"Record":        true,
	"RecordStrict":  true,
	"RecordInTx":    true,
	"RecordTxAudit": true,
}

var eventOwnedElsewhere = map[string]bool{"RecordTxAudit": true}

var rawAppends = []string{"eventbus.Append", "eventbus.AppendBestEffort"}

const auditPkg = "github.com/libraz/nodate-flow/apps/flow-api/internal/audit"

func load(t *testing.T, fixture string) *mutationguard.Analysis {
	t.Helper()
	a, err := mutationguard.Load(filepath.Join("testdata", fixture))
	if err != nil {
		t.Fatalf("load %s: %v", fixture, err)
	}
	return a
}

// unrecorded returns the ids of the registered operations that may
// change state and reach no recorder entry point.
func unrecorded(a *mutationguard.Analysis) []string {
	var out []string
	for _, op := range a.HumaOperations() {
		if op.Mutating() && !a.Reaches(op.Handler, gates) {
			out = append(out, op.ID)
		}
	}
	return out
}

// TestReachabilityFindsTheWriteThatRecordsNothing is the check's
// negative and positive control in one: the two fixtures differ only in
// whether the write reaches the recorder, so a check that reported the
// same answer for both would be reporting nothing.
func TestReachabilityFindsTheWriteThatRecordsNothing(t *testing.T) {
	t.Parallel()

	if got := unrecorded(load(t, "unrecorded")); len(got) != 1 || got[0] != "thing-delete" {
		t.Errorf("a write operation that records nothing must be reported, got %v", got)
	}
	if got := unrecorded(load(t, "recorded")); len(got) != 0 {
		t.Errorf("writes that reach the recorder must not be reported, got %v", got)
	}
}

// TestPlainCallsOnlyExcludesMethodCalls proves the narrower graph is
// actually narrower. A package whose recorder is a package-level
// function takes it so that an unrelated method of the same name cannot
// stand in for the recorder, and a no-op option would hand it the wider
// graph's blind spot while reading as if it had opted out.
func TestPlainCallsOnlyExcludesMethodCalls(t *testing.T) {
	t.Parallel()

	wide, err := mutationguard.Load(filepath.Join("testdata", "recorded"))
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	narrow, err := mutationguard.Load(filepath.Join("testdata", "recorded"), mutationguard.PlainCallsOnly())
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if !wide.Reaches("Create", gates) {
		t.Fatal("the recorder is reached through a field, so the default graph must see it")
	}
	if narrow.Reaches("Create", gates) {
		t.Error("PlainCallsOnly must leave method calls out of the graph")
	}
}

// TestReachabilityIgnoresReads proves the check does not simply flag
// every registered operation. A read that records nothing is the normal
// case, and a guard that failed on it would be turned off.
func TestReachabilityIgnoresReads(t *testing.T) {
	t.Parallel()

	a := load(t, "unrecorded")
	for _, op := range a.HumaOperations() {
		if op.ID == "thing-list" && op.Mutating() {
			t.Error("a GET operation must not be treated as a change")
		}
	}
}

// TestCentralizedFindsTheAppendThatSkipsTheAuditRow covers both shapes
// that reach past the recorder — the direct call and the function taken
// as a value — and proves a mention in a comment is not one of them.
func TestCentralizedFindsTheAppendThatSkipsTheAuditRow(t *testing.T) {
	t.Parallel()

	findings := load(t, "rawappend").Centralized("", rawAppends)
	var lines []string
	for _, f := range findings {
		lines = append(lines, f.String())
	}
	joined := strings.Join(lines, "\n")
	if len(findings) != 2 {
		t.Fatalf("expected the direct append, the value form and nothing else, got:\n%s", joined)
	}
	if !strings.Contains(joined, "eventbus.AppendBestEffort") || !strings.Contains(joined, "eventbus.Append ") {
		t.Errorf("both appenders must be named in the findings, got:\n%s", joined)
	}
	if strings.Contains(joined, ":15:") {
		t.Errorf("a comment mentioning an appender is not a call and must not be reported, got:\n%s", joined)
	}

	if findings := load(t, "recorded").Centralized("", rawAppends); len(findings) != 0 {
		t.Errorf("a package that only records through the recorder must be clean, got %v", findings)
	}
}

// TestCentralizedReportsAnOwnerThatStoppedAppending covers the way this
// check silently stops working: the owner file is skipped, so once it no
// longer appends anything the rule is enforced against a call that has
// moved somewhere else.
func TestCentralizedReportsAnOwnerThatStoppedAppending(t *testing.T) {
	t.Parallel()

	findings := load(t, "recorded").Centralized("handlers.go", rawAppends)
	if len(findings) != len(rawAppends) {
		t.Fatalf("an owner that references none of the appenders must be reported once per appender, got %v", findings)
	}
	for _, f := range findings {
		if !strings.Contains(f.Message, "no longer references") {
			t.Errorf("finding must say the owner stopped appending, got %q", f.Message)
		}
	}
}

// TestBannedImportFindsTheOtherHalf proves the audit recorder is
// unreachable from a guarded package. Without it a handler can write the
// audit row alone, which the reachability check cannot see because it
// matches on the method name.
func TestBannedImportFindsTheOtherHalf(t *testing.T) {
	t.Parallel()

	if findings := load(t, "rawappend").Imports([]string{auditPkg}); len(findings) != 1 {
		t.Errorf("importing the audit recorder must be reported once, got %v", findings)
	}
	if findings := load(t, "recorded").Imports([]string{auditPkg}); len(findings) != 0 {
		t.Errorf("a package that does not import it must be clean, got %v", findings)
	}
}

// TestLiteralsFindEveryHalfDescribedChange drives the fixture whose
// every operation reaches the recorder and still records half a change,
// which is what the reachability and centralisation checks cannot see.
func TestLiteralsFindEveryHalfDescribedChange(t *testing.T) {
	t.Parallel()

	spec := mutationguard.LiteralSpec{
		TypeName:           "mutationlog.Mutation",
		Gates:              gates,
		Required:           []string{"AuditAction", "ResourceType", "ResourceID", "CallSite"},
		EventOptionalGates: eventOwnedElsewhere,
		EventField:         "EventType",
	}

	findings, count := load(t, "halfliteral").Literals(spec)
	if count != 3 {
		t.Fatalf("every literal must be examined, examined %d", count)
	}
	var joined string
	for _, f := range findings {
		joined += f.String() + "\n"
	}
	switch {
	case !strings.Contains(joined, "has no AuditAction"):
		t.Errorf("a literal naming no audit action must be reported, got:\n%s", joined)
	case !strings.Contains(joined, "has no EventType"):
		t.Errorf("a literal naming no event kind must be reported, got:\n%s", joined)
	case !strings.Contains(joined, "sets EventType, which it will not append"):
		t.Errorf("an event kind at the audit-only entry point must be reported, got:\n%s", joined)
	}
	if len(findings) != 3 {
		t.Errorf("expected exactly the three defects the fixture carries, got:\n%s", joined)
	}

	findings, count = load(t, "recorded").Literals(spec)
	if len(findings) != 0 {
		t.Errorf("complete literals must be clean, got %v", findings)
	}
	if count != 2 {
		t.Errorf("both literals in the clean fixture must be examined, examined %d", count)
	}
}

// TestLiteralsReportNothingExamined guards the way a literal check
// passes for the wrong reason forever: pointed at a type name nothing
// uses, it has no findings and no subject.
func TestLiteralsReportNothingExamined(t *testing.T) {
	t.Parallel()

	_, count := load(t, "recorded").Literals(mutationguard.LiteralSpec{
		TypeName: "renamed.Mutation",
		Gates:    gates,
		Required: []string{"AuditAction"},
	})
	if count != 0 {
		t.Fatalf("the fixture declares no literal of that type, examined %d", count)
	}
}
