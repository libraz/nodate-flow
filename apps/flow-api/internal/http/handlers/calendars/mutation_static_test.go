package calendars

import (
	"testing"

	"github.com/libraz/nodate-flow/apps/flow-api/internal/mutationlog/mutationguard"
)

// mutationLogGates are the entry points that count as recording a
// change, keyed by the name a call site uses.
//
// The recorder's own four are here because deps.go reaches them, and the
// two package adapters because every handler reaches those: the adapters
// take the calendar id as a parameter, which is what a call site cannot
// omit, and hand the rest through as a literal, which is what
// [TestCalendarMutationLiteralsNameTheWholeChange] reads.
var mutationLogGates = map[string]bool{
	"Record":               true,
	"RecordStrict":         true,
	"RecordInTx":           true,
	"RecordTxAudit":        true,
	"recordCalendarChange": true,
	"recordCalendarAudit":  true,
}

// eventOwnedElsewhereGates are the gates that write only the audit row
// because a shared transactional helper appended the event. A literal
// handed to one of them must not name an event kind it will not append.
var eventOwnedElsewhereGates = map[string]bool{
	"RecordTxAudit":       true,
	"recordCalendarAudit": true,
}

// bannedRawAppends are the event-log entry points no handler in this
// package may reach. Appending the event without the audit row is how
// half of a change comes to be recorded, and it is the half that looks
// complete to anyone reading the timeline — which is the state most of
// this package's operations were in, with 37 appends behind 19 audit
// rows.
var bannedRawAppends = []string{
	"eventbus.Append",
	"eventbus.AppendBestEffort",
	"eventbus.AppendJudgeEvent",
	"eventbus.AppendReverseEvent",
}

// bannedImports keeps the other half unreachable. With the audit
// recorder outside this package's import set, a call to Record can only
// be the mutation log's, which is what makes the name-based reachability
// check below mean what it says.
var bannedImports = []string{
	"github.com/libraz/nodate-flow/apps/flow-api/internal/audit",
}

// routerDir holds the huma registrations for every calendar operation.
// Unlike intake, this package registers no routes of its own, so the
// operation inventory is read from there and filtered by the package
// qualifier each registration names.
const routerDir = "../../router"

// handlerPkg is that qualifier. Filtering on it is what keeps another
// package's operation out of this guard: the registry names every
// handler in the application, and the bare function name is ambiguous
// across packages that both declare, say, SmartCreate.
const handlerPkg = "calendars"

// writeOperationsWithoutMutationLog lists the non-GET operations that
// record nothing because they change nothing a person could later find
// missing.
//
// The map is an allowlist rather than documentation: a newly registered
// write operation is absent from it and therefore fails
// [TestCalendarMutatingOperationsRecordBothHalves] until it either routes
// through the recorder or is added here with the reason it persists
// nothing.
var writeOperationsWithoutMutationLog = map[string]string{
	"events-smart-create": "parses natural-language text into an event proposal and returns it; events-create is what persists",
}

// readOperationsRequiringMutationLog names the GET operations that must
// still leave a trace. A read is not automatically uninteresting: bulk
// extraction of workspace data is precisely what an administrator
// investigating a leak needs to find.
//
// An entry here is a read that hands out more than it shows, so the
// operation is held to the same reachability rule as a write even though
// its method exempts it.
var readOperationsRequiringMutationLog = map[string]string{
	"attachments-download": "mints a presigned URL carrying the stored bytes to whoever holds it; the audit row is the only trace that the file left the workspace",
}

func loadCalendarPackage(t *testing.T) *mutationguard.Analysis {
	t.Helper()
	a, err := mutationguard.Load(".")
	if err != nil {
		t.Fatalf("load package: %v", err)
	}
	return a
}

// calendarOperations returns the registered operations this package
// handles.
func calendarOperations(t *testing.T) []mutationguard.Operation {
	t.Helper()
	router, err := mutationguard.Load(routerDir)
	if err != nil {
		t.Fatalf("load router package: %v", err)
	}
	var out []mutationguard.Operation
	for _, op := range router.HumaOperations() {
		if op.HandlerPkg == handlerPkg {
			out = append(out, op)
		}
	}
	return out
}

// TestCalendarMutatingOperationsRecordBothHalves walks the registered
// operations and proves every one that can change something reaches the
// one place that records it in both logs.
//
// Driven by the router registration rather than by a list of function
// names on purpose: the fix for the operations that are wrong today is
// not a fix, because the next one is written by somebody who never read
// this file.
func TestCalendarMutatingOperationsRecordBothHalves(t *testing.T) {
	t.Parallel()

	a := loadCalendarPackage(t)
	ops := calendarOperations(t)
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
				"route it through recordCalendarChange, or through recordCalendarAudit when a shared transactional "+
				"helper already appended the event, or add it to writeOperationsWithoutMutationLog with the reason "+
				"it persists nothing (gates: %v)",
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

// TestCalendarRecordsOnlyThroughTheMutationLog proves no handler reaches
// around the recorder to either log on its own.
//
// Without this half, the reachability test above is satisfiable by a
// handler that appends its event directly and skips the audit row —
// which is the state this package was in for calendar membership,
// checklist, comment, attachment, attendee and subscription changes.
// Keeping both writes behind one call is what makes "event and audit row
// together" a property of the package rather than a habit.
func TestCalendarRecordsOnlyThroughTheMutationLog(t *testing.T) {
	t.Parallel()

	a := loadCalendarPackage(t)
	// No owner file: within this package the recorder is the only
	// appender, and it lives in another package.
	for _, f := range a.Centralized("", bannedRawAppends) {
		t.Error(f.String())
	}
	for _, f := range a.Imports(bannedImports) {
		t.Error(f.String())
	}
}

// TestCalendarMutationLiteralsNameTheWholeChange reads every mutation
// literal in the package and proves it names both halves of what it
// records, plus the resource the audit query filters on and the call
// site the loss log reports.
//
// A mutation with one half filled in compiles, runs, and records half
// the change. Half is worse than none: the table that got a row reads as
// a complete answer to whoever queries it.
//
// The literals live in deps.go, where the two adapters assemble them
// from required parameters, so this check covers the shape and the
// parameter lists cover each call site. That is why the audit action is
// a parameter rather than a field a caller may leave out.
func TestCalendarMutationLiteralsNameTheWholeChange(t *testing.T) {
	t.Parallel()

	a := loadCalendarPackage(t)
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
