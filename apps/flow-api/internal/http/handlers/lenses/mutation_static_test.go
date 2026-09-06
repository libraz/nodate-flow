package lenses

import (
	"testing"

	"github.com/libraz/nodate-flow/apps/flow-api/internal/mutationlog/mutationguard"
)

// mutationLogGates are the recorder entry points that count as recording
// a change, keyed by the method name a call site uses.
var mutationLogGates = map[string]bool{
	"Record":        true,
	"RecordStrict":  true,
	"RecordInTx":    true,
	"RecordTxAudit": true,
}

// eventOwnedElsewhereGates are the gates that write only the audit row
// because another writer appended the event. A literal handed to one of
// them must not name an event kind it will not append.
var eventOwnedElsewhereGates = map[string]bool{
	"RecordTxAudit": true,
}

// bannedRawAppends are the event-log entry points no file in this
// package may reach. Appending an event without the audit row is how
// half of a change comes to be recorded, and it is the half that looks
// complete to anyone reading the timeline.
var bannedRawAppends = []string{
	"eventbus.Append",
	"eventbus.AppendBestEffort",
	"eventbus.AppendJudgeEvent",
	"eventbus.AppendReverseEvent",
}

// bannedImports keeps the other half unreachable. With the audit
// recorder outside this package's import set, a call to a gate name can
// only be the mutation log's, which is what makes the name-based
// reachability check below mean what it says.
var bannedImports = []string{
	"github.com/libraz/nodate-flow/apps/flow-api/internal/audit",
}

// writeOperationsWithoutMutationLog lists the non-GET operations that
// record nothing because they change nothing a person could later find
// missing.
//
// The map is an allowlist rather than documentation: a newly registered
// write operation is absent from it and therefore fails
// [TestLensMutatingOperationsRecordBothHalves] until it either routes
// through the recorder or is added here with the reason it persists
// nothing.
var writeOperationsWithoutMutationLog = map[string]string{}

// readOperationsRequiringMutationLog names the GET operations that must
// still leave a trace. A read is not automatically uninteresting: bulk
// extraction of workspace data is precisely what an administrator
// investigating a leak needs to find.
var readOperationsRequiringMutationLog = map[string]string{}

func loadLensPackage(t *testing.T) *mutationguard.Analysis {
	t.Helper()
	a, err := mutationguard.Load(".")
	if err != nil {
		t.Fatalf("load package: %v", err)
	}
	return a
}

// TestLensMutatingOperationsRecordBothHalves walks the registered
// operations and proves every one that can change something reaches the
// one place that records it.
//
// Driven by the route registration rather than by a list of function
// names on purpose: the fix for an operation that is wrong today is not
// a fix, because the next one is written by somebody who never read this
// file. Both register.go functions are read, so the public share route
// is inside the inventory rather than outside it.
func TestLensMutatingOperationsRecordBothHalves(t *testing.T) {
	t.Parallel()

	a := loadLensPackage(t)
	ops := a.HumaOperations()
	if len(ops) == 0 {
		t.Fatal("no operations were read from the registration; the check is looking at nothing")
	}

	seenExempt := map[string]bool{}
	seenRequiredRead := map[string]bool{}
	for _, op := range ops {
		if op.Handler == "" || !a.HasFunc(op.Handler) {
			t.Errorf("%s:%d: operation %q names no handler this package declares", op.File, op.Line, op.ID)
			continue
		}
		required := op.Mutating()
		if _, ok := readOperationsRequiringMutationLog[op.ID]; ok {
			required = true
			seenRequiredRead[op.ID] = true
		}
		if !required {
			continue
		}
		if _, exempt := writeOperationsWithoutMutationLog[op.ID]; exempt {
			seenExempt[op.ID] = true
			continue
		}
		if !a.Reaches(op.Handler, mutationLogGates) {
			t.Errorf("%s:%d: operation %q (%s) can change the workspace but never reaches a recorder entry point; "+
				"route it through one of %v, or add it to writeOperationsWithoutMutationLog with the reason it persists nothing",
				op.File, op.Line, op.ID, op.Handler, mutationguard.SortedKeys(mutationLogGates))
		}
	}

	for id := range writeOperationsWithoutMutationLog {
		if !seenExempt[id] {
			t.Errorf("writeOperationsWithoutMutationLog lists %q, which is no longer a registered write operation; drop the stale entry", id)
		}
	}
	for id := range readOperationsRequiringMutationLog {
		if !seenRequiredRead[id] {
			t.Errorf("readOperationsRequiringMutationLog lists %q, which is no longer a registered operation; drop the stale entry", id)
		}
	}
}

// TestLensRecordsOnlyThroughTheMutationLog proves no handler reaches
// around the recorder to either log on its own.
//
// Without this half, the reachability test above is satisfiable by a
// handler that appends its event directly and skips the audit row. An
// append written beside its own audit entry is no better: two
// descriptions of one change drift, and a reader comparing the tables
// cannot then tell which is stale.
func TestLensRecordsOnlyThroughTheMutationLog(t *testing.T) {
	t.Parallel()

	a := loadLensPackage(t)
	for _, f := range a.Centralized("", bannedRawAppends) {
		t.Error(f.String())
	}
	for _, f := range a.Imports(bannedImports) {
		t.Error(f.String())
	}
}

// TestLensMutationLiteralsNameTheWholeChange reads every mutation
// literal in the package and proves it names what it records, plus the
// resource the audit query filters on and the call site the loss log
// reports.
//
// A mutation with one half filled in compiles, runs, and records half
// the change. Half is worse than none: the table that got a row reads as
// a complete answer to whoever queries it.
func TestLensMutationLiteralsNameTheWholeChange(t *testing.T) {
	t.Parallel()

	a := loadLensPackage(t)
	findings, count := a.Literals(mutationguard.LiteralSpec{
		TypeName:           "mutationlog.Mutation",
		Gates:              mutationLogGates,
		Required:           []string{"AuditAction", "ResourceType", "ResourceID", "CallSite"},
		EventOptionalGates: eventOwnedElsewhereGates,
		EventField:         "EventType",
	})
	for _, f := range findings {
		t.Error(f.String())
	}
	if count == 0 {
		t.Fatal("no mutation literals found; the guard is passing because it is looking at nothing")
	}
}
