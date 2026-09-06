package inbox

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"sort"
	"strings"
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

// bannedRawAppends are the event-log entry points no file in this package
// may reach. Appending an event without the audit row is how half of a
// change comes to be recorded, and it is the half that looks complete to
// anyone reading the timeline.
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

// writeOperationsWithoutMutationLog lists the non-GET operations that
// reach no recorder in this package, each with the reason.
//
// The map is an allowlist rather than documentation: a newly registered
// write operation is absent from it and therefore fails
// [TestInboxMutatingOperationsRecordBothHalves] until it either routes
// through the recorder or is added here with its reason.
var writeOperationsWithoutMutationLog = map[string]string{
	"inbox-triage": "the suggestions it proposes are persisted inside internal/ai by ProposeInboxTriage, which records them where it writes them; the handler itself changes nothing",
}

// readOperationsRequiringMutationLog names the GET operations that must
// still leave a trace. A read is not automatically uninteresting: bulk
// extraction of workspace data is precisely what an administrator
// investigating a leak needs to find.
var readOperationsRequiringMutationLog = map[string]string{}

// inboxEventKinds are the event-kind constants this package may name,
// each with what the id filed under it identifies.
//
// The kind is the only thing telling a subscriber which table to resolve
// that id against, and a lookup in the wrong one comes back empty rather
// than failing — so the subscriber sees a change it can make nothing of,
// on every row the operation produces. The inbox is a view over
// `signals`: archiving disables a signal and snoozing moves its
// received_at, so a kind naming any other table's rows is one no
// consumer of this package's events can follow.
var inboxEventKinds = map[string]string{
	"SignalArchived":        "a row of `signals`, taken off the queue",
	"SignalSnoozed":         "a row of `signals`, deferred to a later moment",
	"AiSuggestionApplied":   "the suggestion a reader accepted",
	"AiSuggestionDismissed": "the suggestion a reader turned down",
}

// eventKindQualifier is the package identifier the kinds are reached
// through here. Within this package it carries kind constants and the
// Kind type and nothing else, because [bannedRawAppends] keeps the
// append entry points out of reach.
const eventKindQualifier = "eventbus"

// eventKindTypeName is the one name under that qualifier that is not a
// kind.
const eventKindTypeName = "Kind"

// TestInboxNamesOnlyKindsForItsOwnRows reads every event kind the
// package names and holds it to that list.
//
// It reads the kinds the package names rather than the ones written on a
// record, because a kind reaches a record more than one way: the queue
// operations write the constant inline, and the suggestion reactions
// carry it on a value declared once and shared by two handlers. A guard
// that only read record literals would see the first pair and report the
// second as naming nothing at all.
//
// The constant is read as written rather than by its value. The kinds
// are declared once in the shared package and re-exported, so the name
// is what a call site can get wrong, and a comparison by value would
// pass a string spelled to match.
func TestInboxNamesOnlyKindsForItsOwnRows(t *testing.T) {
	t.Parallel()

	fset := token.NewFileSet()
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatalf("read package directory: %v", err)
	}

	named := map[string]bool{}
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		file, perr := parser.ParseFile(fset, filepath.Join(".", name), nil, 0)
		if perr != nil {
			t.Fatalf("parse %s: %v", name, perr)
		}
		ast.Inspect(file, func(n ast.Node) bool {
			sel, ok := n.(*ast.SelectorExpr)
			if !ok {
				return true
			}
			pkg, ok := sel.X.(*ast.Ident)
			if !ok || pkg.Name != eventKindQualifier || sel.Sel.Name == eventKindTypeName {
				return true
			}
			named[sel.Sel.Name] = true
			if _, allowed := inboxEventKinds[sel.Sel.Name]; !allowed {
				t.Errorf("%s:%d: names %s, which identifies a row this package files no id for; record under one of %v, or add it here with the row it names",
					name, fset.Position(sel.Pos()).Line, sel.Sel.Name, sortedKindNames(inboxEventKinds))
			}
			return true
		})
	}

	if len(named) == 0 {
		t.Fatal("the package names no event kind; the guard is passing because it is looking at nothing")
	}
	for kind := range inboxEventKinds {
		if !named[kind] {
			t.Errorf("inboxEventKinds allows %s, which nothing in this package names; drop the stale entry rather than leave a kind allowed that no code writes", kind)
		}
	}
}

// sortedKindNames renders the allowed kinds for a failure message.
func sortedKindNames(kinds map[string]string) []string {
	out := make([]string, 0, len(kinds))
	for name := range kinds {
		out = append(out, name)
	}
	sort.Strings(out)
	return out
}

func loadInboxPackage(t *testing.T) *mutationguard.Analysis {
	t.Helper()
	a, err := mutationguard.Load(".")
	if err != nil {
		t.Fatalf("load package: %v", err)
	}
	return a
}

// TestInboxMutatingOperationsRecordBothHalves walks the registered
// operations and proves every one that can change something reaches the
// one place that records it.
//
// Driven by the registration rather than by a list of function names on
// purpose: the fix for an operation that is wrong today is not a fix,
// because the next one is written by somebody who never read this file.
//
// Every route this package serves is a huma registration written in this
// package, so the registry read here is the whole inventory. A chi
// handler would be invisible to it, which is why one may not be added
// here without extending the inventory to cover it.
func TestInboxMutatingOperationsRecordBothHalves(t *testing.T) {
	t.Parallel()

	a := loadInboxPackage(t)
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
				"route it through one of %v, or add it to writeOperationsWithoutMutationLog with the reason it records nothing",
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

// TestInboxRecordsOnlyThroughTheMutationLog proves no handler reaches
// around the recorder to either log on its own.
//
// Without this half, the reachability test above is satisfiable by a
// handler that appends its event directly and skips the audit row, which
// is the half that looks complete to whoever reads the timeline and
// silent to whoever queries audit_logs by action name.
func TestInboxRecordsOnlyThroughTheMutationLog(t *testing.T) {
	t.Parallel()

	a := loadInboxPackage(t)
	// No owner file: within this package the recorder is the only
	// appender, and it lives in another package.
	for _, f := range a.Centralized("", bannedRawAppends) {
		t.Error(f.String())
	}
	for _, f := range a.Imports(bannedImports) {
		t.Error(f.String())
	}
}

// TestInboxMutationLiteralsNameTheWholeChange reads every mutation
// literal in the package and proves it names what it records, plus the
// resource the audit query filters on and the call site the loss log
// reports.
//
// A mutation with one half filled in compiles, runs, and records half the
// change. Half is worse than none: the table that got a row reads as a
// complete answer to whoever queries it.
func TestInboxMutationLiteralsNameTheWholeChange(t *testing.T) {
	t.Parallel()

	a := loadInboxPackage(t)
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
