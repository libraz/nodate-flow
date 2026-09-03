package mcp

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"strings"
	"testing"
)

// mutationLogGates are the entry points that record a change. Every one
// of them lives in mutationlog.go and writes the audit row; the first
// two additionally append the event.
var mutationLogGates = map[string]bool{
	"recordMutation":        true,
	"recordMutationStrict":  true,
	"recordTxMutationAudit": true,
}

// writeToolsWithoutMutationLog lists the write-scoped tools that record
// nothing because they change nothing a person could later find missing.
//
// write:workspace is not a synonym for "mutates": the scope also covers
// spending the workspace's AI budget, because a read-only token's holder
// cannot undo a charge (see the scope vocabulary on session.hasScope).
// Every entry below is in the scope for that second reason, so the
// justification each one needs is that it writes no workspace state — not
// that it is harmless.
//
// The map is an allowlist rather than documentation: a newly registered
// write:workspace tool is absent from it and therefore fails
// [TestMCPMutatingToolsRecordBothHalves] until it either routes through
// a mutation-log gate or is added here with the reason it persists
// nothing.
var writeToolsWithoutMutationLog = map[string]string{
	"propose_tasks_from": "asks the model for task candidates and returns them; persists nothing",
	"propose_priority":   "asks the model for a priority and returns it; persists nothing",
	"propose_steps":      "asks the model for a task breakdown and returns it; apply_steps is what persists",
	"propose_lens":       "compiles a query into a lens definition and returns it; persists nothing",
	"propose_duplicates": "similarity search; the only write is the task's own embedding, a derived cache",
	"propose_relations":  "similarity search; the only write is the task's own embedding, a derived cache",
}

// readToolsRequiringMutationLog names the read-scoped tools that must
// still leave a trace. A read is not automatically uninteresting: an
// export takes a workspace's task data out in bulk, which is precisely
// the event an administrator investigating a leak needs to find, and it
// is the operation an automated caller reaches for most.
var readToolsRequiringMutationLog = map[string]string{
	"export_tasks": "bulk extraction of task data; REST records the same export",
}

// TestMCPMutatingToolsRecordBothHalves walks the registered tool table
// and proves every tool that changes something, or takes something out
// in bulk, reaches the one place that records it.
//
// Driven by the registry rather than by a list of functions on purpose:
// the audit that prompted this found three tools missing the record and
// the fix for three tools is not a fix, because the fourth is written
// later by somebody who never read this file.
func TestMCPMutatingToolsRecordBothHalves(t *testing.T) {
	t.Parallel()

	h := NewHandler(Deps{})
	if len(h.tools) == 0 {
		t.Fatal("no tools registered")
	}
	graph := mcpPackageCallGraph(t)
	registry := mcpRegisteredTools(t)

	seenExempt := map[string]bool{}
	seenRequiredRead := map[string]bool{}
	for name, tl := range h.tools {
		required := tl.requiredScope == ScopeWriteWorkspace
		if _, ok := readToolsRequiringMutationLog[name]; ok {
			required = true
			seenRequiredRead[name] = true
		}
		if !required {
			continue
		}
		if _, exempt := writeToolsWithoutMutationLog[name]; exempt {
			seenExempt[name] = true
			continue
		}
		entry := mcpRunFuncName(t, registry, name)
		if !reachesAny(graph, entry, mutationLogGates) {
			t.Errorf("tool %q (%s) changes the workspace but never reaches a mutation-log gate (%s); "+
				"route it through recordMutation / recordMutationStrict, or through recordTxMutationAudit "+
				"when a shared transactional helper already appended the event, or add it to "+
				"writeToolsWithoutMutationLog with the reason it persists nothing",
				name, entry, strings.Join(sortedKeys(mutationLogGates), " / "))
		}
	}

	for name := range writeToolsWithoutMutationLog {
		if !seenExempt[name] {
			t.Errorf("writeToolsWithoutMutationLog lists %q, which is no longer a registered write:workspace tool; drop the stale entry", name)
		}
	}
	for name := range readToolsRequiringMutationLog {
		if !seenRequiredRead[name] {
			t.Errorf("readToolsRequiringMutationLog lists %q, which is no longer a registered tool; drop the stale entry", name)
		}
	}
}

// TestMCPEventbusAppendCentralized proves no tool reaches around the
// mutation log straight to the event bus.
//
// Without this half, the reachability test above is satisfiable by a
// tool that appends its event directly and skips the audit row — which
// is the exact state most of these tools were in. Keeping the append in
// one file is what makes "event and audit row together" a property of
// the package rather than a habit.
func TestMCPEventbusAppendCentralized(t *testing.T) {
	t.Parallel()

	const owner = "mutationlog.go"
	banned := []string{"eventbus.Append(", "eventbus.AppendBestEffort("}
	for _, name := range mcpPackageSourceFiles(t) {
		if name == owner {
			continue
		}
		b, err := os.ReadFile(name)
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		for _, call := range banned {
			if strings.Contains(string(b), call) {
				t.Errorf("%s calls %s directly; append through %s so the audit row cannot go missing", name, call, owner)
			}
		}
	}

	src := readMCPSource(t, owner)
	for _, want := range []string{"eventbus.Append(", "eventbus.AppendBestEffort("} {
		if !strings.Contains(src, want) {
			t.Errorf("%s must be the file that calls %s", owner, want)
		}
	}
}

// TestMCPMutationLiteralsNameBothHalves reads every mutation literal in
// the package and proves it names the audit action, and — except at the
// one gate that exists for changes whose event a shared transactional
// helper already appended — the event kind too.
//
// A mutation with one half filled in compiles, runs, and records half
// the change. Half is worse than none: the table that got a row reads
// as a complete answer to whoever queries it.
func TestMCPMutationLiteralsNameBothHalves(t *testing.T) {
	t.Parallel()

	fset := token.NewFileSet()
	found := 0
	for _, name := range mcpPackageSourceFiles(t) {
		file, err := parser.ParseFile(fset, name, nil, 0)
		if err != nil {
			t.Fatalf("parse %s: %v", name, err)
		}
		ast.Inspect(file, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}
			fn, ok := call.Fun.(*ast.Ident)
			if !ok || !mutationLogGates[fn.Name] {
				return true
			}
			lit := mutationLiteralArg(call)
			if lit == nil {
				// A value assembled elsewhere; the runtime guard in
				// mutationlog.go is what covers that shape.
				return true
			}
			found++
			keys := compositeKeys(lit)
			pos := fset.Position(call.Pos())
			if !keys["AuditAction"] {
				t.Errorf("%s:%d: %s literal has no AuditAction; name the audit_logs action REST uses for this change",
					name, pos.Line, fn.Name)
			}
			if fn.Name != "recordTxMutationAudit" && !keys["EventType"] {
				t.Errorf("%s:%d: %s literal has no EventType; name the eventbus kind, or use recordTxMutationAudit if a shared helper already appended it",
					name, pos.Line, fn.Name)
			}
			if fn.Name == "recordTxMutationAudit" && keys["EventType"] {
				t.Errorf("%s:%d: recordTxMutationAudit literal sets EventType, which it will not append; use recordMutation if this change needs its own event",
					name, pos.Line)
			}
			return true
		})
	}
	if found == 0 {
		t.Fatal("no mutation literals found; the guard is passing because it is looking at nothing")
	}
}

// mutationLiteralArg returns the mutation composite literal passed to a
// mutation-log gate, or nil when the argument is not written inline.
func mutationLiteralArg(call *ast.CallExpr) *ast.CompositeLit {
	for _, arg := range call.Args {
		lit, ok := arg.(*ast.CompositeLit)
		if !ok {
			continue
		}
		if id, ok := lit.Type.(*ast.Ident); ok && id.Name == "mutation" {
			return lit
		}
	}
	return nil
}

// compositeKeys returns the field names a keyed composite literal sets.
func compositeKeys(lit *ast.CompositeLit) map[string]bool {
	out := map[string]bool{}
	for _, elt := range lit.Elts {
		kv, ok := elt.(*ast.KeyValueExpr)
		if !ok {
			continue
		}
		if id, ok := kv.Key.(*ast.Ident); ok {
			out[id.Name] = true
		}
	}
	return out
}
