package signals

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

// writeOperationsWithoutMutationLog lists the registered non-GET
// operations that reach no recorder, each with the reason.
//
// The map is an allowlist rather than documentation: a newly registered
// write operation is absent from it and therefore fails
// [TestSignalsMutatingOperationsRecordBothHalves] until it either routes
// through the recorder or is added here with its reason.
var writeOperationsWithoutMutationLog = map[string]string{}

// readOperationsRequiringMutationLog names the GET operations that must
// still leave a trace. A read is not automatically uninteresting: bulk
// extraction of workspace data is precisely what an administrator
// investigating a leak needs to find.
var readOperationsRequiringMutationLog = map[string]string{}

// chiHandlersWithoutMutationLog lists the chi-level handlers that
// persist nothing, each with the reason. Same allowlist rule as the
// registered operations above.
var chiHandlersWithoutMutationLog = map[string]string{}

// chiHandlerResult is the result type a chi-level handler constructor in
// this package returns. The webhook receivers are written at the chi
// layer so they can verify a raw request body before unmarshalling it,
// which is also why no huma registration names them.
const chiHandlerResult = "http.HandlerFunc"

// chiHandlerParams is the parameter list of a handler written as the
// serving function itself rather than as a constructor. Both shapes are
// recognised, because which one a receiver is written in is a style
// choice and the rule it answers to is not.
var chiHandlerParams = []string{"http.ResponseWriter", "*http.Request"}

func loadSignalsPackage(t *testing.T) *mutationguard.Analysis {
	t.Helper()
	a, err := mutationguard.Load(".")
	if err != nil {
		t.Fatalf("load package: %v", err)
	}
	return a
}

// chiHandlers returns every exported function in this package that
// serves a chi route, sorted.
//
// It exists because [mutationguard.Analysis.HumaOperations] reads the
// huma registry and the webhook receivers are not in it: a guard driven
// by that registry alone would pass while saying nothing at all about
// the three inbound deliveries, which is worse than no guard, because it
// turns an unchecked path into one a reader believes was checked.
//
// The inventory is taken from this package's own declarations rather
// than from the router that wires them, so a receiver added here is
// covered before it is routed, and the guard does not depend on a file
// outside the package it guards.
func chiHandlers(t *testing.T) []string {
	t.Helper()
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatalf("read package dir: %v", err)
	}
	fset := token.NewFileSet()
	var out []string
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		file, perr := parser.ParseFile(fset, filepath.Join(".", name), nil, 0)
		if perr != nil {
			t.Fatalf("parse %s: %v", name, perr)
		}
		for _, decl := range file.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok || fn.Recv != nil || !fn.Name.IsExported() {
				continue
			}
			if returnsChiHandler(fn) || servesHTTPDirectly(fn) {
				out = append(out, fn.Name.Name)
			}
		}
	}
	sort.Strings(out)
	return out
}

// returnsChiHandler reports whether fn is a handler constructor.
func returnsChiHandler(fn *ast.FuncDecl) bool {
	results := fn.Type.Results
	if results == nil || len(results.List) != 1 {
		return false
	}
	return renderType(results.List[0].Type) == chiHandlerResult
}

// servesHTTPDirectly reports whether fn is itself the serving function.
func servesHTTPDirectly(fn *ast.FuncDecl) bool {
	params := fn.Type.Params
	if params == nil {
		return false
	}
	var written []string
	for _, field := range params.List {
		// One field can name several parameters of one type.
		names := len(field.Names)
		if names == 0 {
			names = 1
		}
		for i := 0; i < names; i++ {
			written = append(written, renderType(field.Type))
		}
	}
	if len(written) != len(chiHandlerParams) {
		return false
	}
	for i, want := range chiHandlerParams {
		if written[i] != want {
			return false
		}
	}
	return true
}

// renderType renders a type expression the way it is written, which is
// all the comparisons above need.
func renderType(e ast.Expr) string {
	switch v := e.(type) {
	case *ast.Ident:
		return v.Name
	case *ast.StarExpr:
		return "*" + renderType(v.X)
	case *ast.SelectorExpr:
		return renderType(v.X) + "." + v.Sel.Name
	}
	return ""
}

// TestSignalsMutatingOperationsRecordBothHalves walks the registered
// operations and proves every one that can change something reaches the
// one place that records it.
//
// Driven by the registration rather than by a list of function names on
// purpose: the fix for an operation that is wrong today is not a fix,
// because the next one is written by somebody who never read this file.
func TestSignalsMutatingOperationsRecordBothHalves(t *testing.T) {
	t.Parallel()

	a := loadSignalsPackage(t)
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

// TestSignalsWebhookHandlersRecordBothHalves holds the chi-level
// receivers to the rule the registered operations are held to.
//
// An inbound delivery persists a signals row, which is the same change
// the manual endpoint makes; the transport it arrived over is not a
// reason for it to appear on no timeline and in no audit query. Nothing
// enumerates these handlers for the huma-driven check above, so the
// inventory is built here — a guard that reported green over a path it
// never opened would be the worse outcome.
func TestSignalsWebhookHandlersRecordBothHalves(t *testing.T) {
	t.Parallel()

	a := loadSignalsPackage(t)
	handlers := chiHandlers(t)
	if len(handlers) == 0 {
		t.Fatalf("no chi handlers found; the check is looking at nothing. One is recognised by "+
			"returning exactly one %s, or by taking %v", chiHandlerResult, chiHandlerParams)
	}

	seenExempt := map[string]bool{}
	for _, h := range handlers {
		if _, exempt := chiHandlersWithoutMutationLog[h]; exempt {
			seenExempt[h] = true
			continue
		}
		if !a.Reaches(h, mutationLogGates) {
			t.Errorf("%s serves an inbound delivery that can change the workspace but never reaches a recorder "+
				"entry point; route it through one of %v, or add it to chiHandlersWithoutMutationLog with the "+
				"reason it persists nothing",
				h, mutationguard.SortedKeys(mutationLogGates))
		}
	}
	for h := range chiHandlersWithoutMutationLog {
		if !seenExempt[h] {
			t.Errorf("chiHandlersWithoutMutationLog lists %q, which this package no longer declares; drop the stale entry", h)
		}
	}
}

// TestSignalsRecordsOnlyThroughTheMutationLog proves no handler reaches
// around the recorder to either log on its own.
//
// Without this half, the reachability tests above are satisfiable by a
// handler that appends its event directly and skips the audit row, which
// is the half that looks complete to whoever reads the timeline and
// silent to whoever queries audit_logs by action name.
func TestSignalsRecordsOnlyThroughTheMutationLog(t *testing.T) {
	t.Parallel()

	a := loadSignalsPackage(t)
	// No owner file: within this package the recorder is the only
	// appender, and it lives in another package.
	for _, f := range a.Centralized("", bannedRawAppends) {
		t.Error(f.String())
	}
	for _, f := range a.Imports(bannedImports) {
		t.Error(f.String())
	}
}

// TestSignalsMutationLiteralsNameTheWholeChange reads every mutation
// literal in the package and proves it names what it records, plus the
// resource the audit query filters on and the call site the loss log
// reports.
//
// A mutation with one half filled in compiles, runs, and records half the
// change. Half is worse than none: the table that got a row reads as a
// complete answer to whoever queries it.
func TestSignalsMutationLiteralsNameTheWholeChange(t *testing.T) {
	t.Parallel()

	a := loadSignalsPackage(t)
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
