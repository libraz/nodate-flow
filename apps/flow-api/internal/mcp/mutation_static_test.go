package mcp

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"regexp"
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
// nothing because they change nothing. Both ask a model a question and
// hand the answer back; neither writes a row.
//
// The map is an allowlist rather than documentation: a newly registered
// write:workspace tool is absent from it and therefore fails
// [TestMCPMutatingToolsRecordBothHalves] until it either routes through
// a mutation-log gate or is added here with the reason it persists
// nothing.
var writeToolsWithoutMutationLog = map[string]string{
	"propose_tasks_from": "asks the model for task candidates and returns them; persists nothing",
	"propose_priority":   "asks the model for a priority and returns it; persists nothing",
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
		entry := mcpRunFuncName(t, name, tl.run)
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

// restActionSources maps each audit action an MCP tool records onto the
// REST handler that records the same action for the same change.
//
// The point of the mapping is that the two transports must be
// indistinguishable to whoever queries audit_logs. An administrator
// asking "who exported this workspace's tasks" filters by action name;
// if MCP wrote export.mcp.create and REST wrote export.create, the query
// would answer with half the truth and look complete.
var restActionSources = map[string]string{
	"calendar.event.create": "../http/handlers/calendars/events.go",
	"calendar.event.update": "../http/handlers/calendars/events.go",
	"calendar.event.delete": "../http/handlers/calendars/events.go",
	"calendar.memo.update":  "../http/handlers/calendars/memos.go",
	"export.create":         "../http/handlers/export/handler.go",
	"import.create":         "../http/handlers/imports/crud.go",
	"task.create":           "../http/handlers/tasks/crud.go",
	"task.update":           "../http/handlers/tasks/crud.go",
	"task.transition":       "../http/handlers/tasks/transitions.go",
	"task.archived":         "../http/handlers/tasks/archive.go",
	"task.unarchived":       "../http/handlers/tasks/archive.go",
	"comment.create":        "../http/handlers/tasks/comments.go",
	"label.create":          "../http/handlers/labels/crud.go",
	"task.label.remove":     "../http/handlers/labels/crud.go",
	"page.create":           "../http/handlers/pages/handlers.go",
	"page.update":           "../http/handlers/pages/handlers.go",
	"page.generate":         "../http/handlers/pages/handlers.go",
	"timebox.create":        "../http/handlers/timeboxes/handlers.go",
	"timebox.task.add":      "../http/handlers/timeboxes/handlers.go",
	"favorite.create":       "../http/handlers/favorites/crud.go",
	"reaction.create":       "../http/handlers/reactions/crud.go",
	"intake.triage":         "../http/handlers/intake/crud.go",
	"intake.convert":        "../http/handlers/intake/crud.go",
	"task.smart_create":     "../http/handlers/tasks/smart_create.go",
	"task.apply_steps":      "../http/handlers/tasks/steps.go",

	"description_version.restore": "../http/handlers/tasks/description_versions.go",
}

// mcpOnlyActions are the audit actions MCP records for a change REST has
// no equivalent route for. Each needs a reason, because "REST does not
// do this" is usually a sign the action name was invented rather than
// borrowed.
var mcpOnlyActions = map[string]string{
	"task.label.add": "REST attaches a label through the task-labels route, which records no audit entry; the name mirrors task.label.remove",
}

// TestMCPMutationActionsMatchREST proves every audit action an MCP tool
// writes is spelled the same way the REST handler for that change spells
// it.
func TestMCPMutationActionsMatchREST(t *testing.T) {
	t.Parallel()

	actions := mcpAuditActions(t)
	if len(actions) == 0 {
		t.Fatal("no audit actions found; the guard is passing because it is looking at nothing")
	}

	sources := map[string]string{}
	for action := range actions {
		if _, ok := mcpOnlyActions[action]; ok {
			continue
		}
		path, ok := restActionSources[action]
		if !ok {
			t.Errorf("MCP records audit action %q with no REST counterpart declared; add it to restActionSources, "+
				"or to mcpOnlyActions with the reason REST has no equivalent", action)
			continue
		}
		src, ok := sources[path]
		if !ok {
			b, err := os.ReadFile(path)
			if err != nil {
				t.Errorf("restActionSources points %q at %s, which cannot be read: %v", action, path, err)
				continue
			}
			src = string(b)
			sources[path] = src
		}
		// Whitespace-insensitive: gofmt aligns the key to whatever else
		// the audit.Entry literal sets, so a fixed run of spaces would
		// make this guard depend on an unrelated field being present.
		declared := regexp.MustCompile(`Action:\s*"` + regexp.QuoteMeta(action) + `"`)
		if !declared.MatchString(src) {
			t.Errorf("MCP records audit action %q but %s no longer does; the two transports must be "+
				"indistinguishable to an audit query, so follow the rename or correct the mapping",
				action, path)
		}
	}

	for action := range mcpOnlyActions {
		if !actions[action] {
			t.Errorf("mcpOnlyActions lists %q, which no MCP tool records any more; drop the stale entry", action)
		}
	}
}

// mcpAuditActions collects the AuditAction string literals set on every
// mutation literal in the package.
func mcpAuditActions(t *testing.T) map[string]bool {
	t.Helper()
	fset := token.NewFileSet()
	out := map[string]bool{}
	for _, name := range mcpPackageSourceFiles(t) {
		file, err := parser.ParseFile(fset, name, nil, 0)
		if err != nil {
			t.Fatalf("parse %s: %v", name, err)
		}
		ast.Inspect(file, func(n ast.Node) bool {
			lit, ok := n.(*ast.CompositeLit)
			if !ok {
				return true
			}
			if id, ok := lit.Type.(*ast.Ident); !ok || id.Name != "mutation" {
				return true
			}
			for _, elt := range lit.Elts {
				kv, ok := elt.(*ast.KeyValueExpr)
				if !ok {
					continue
				}
				key, ok := kv.Key.(*ast.Ident)
				if !ok || key.Name != "AuditAction" {
					continue
				}
				val, ok := kv.Value.(*ast.BasicLit)
				if !ok || val.Kind != token.STRING {
					continue
				}
				out[strings.Trim(val.Value, `"`)] = true
			}
			return true
		})
	}
	return out
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
