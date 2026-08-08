package mcp

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"reflect"
	"runtime"
	"sort"
	"strings"
	"testing"
)

func TestMCPResolveTaskUsesSharedVisibilityACL(t *testing.T) {
	t.Parallel()

	src := readMCPSource(t, "acl.go")
	for _, want := range []string{
		"acl.AuthorizeTaskAccess",
	} {
		if !strings.Contains(src, want) {
			t.Fatalf("resolveTask must keep using shared ACL helper %s", want)
		}
	}
}

func TestMCPListTasksUsesVisibilityFilter(t *testing.T) {
	t.Parallel()

	src := readMCPSource(t, "tools.go")
	for _, want := range []string{
		"listMCPTasks",
		"acl.TaskVisibilityFilter",
	} {
		if !strings.Contains(src, want) {
			t.Fatalf("MCP list_tasks must keep applying task visibility filter %s", want)
		}
	}
}

// TestMCPCalendarResolverRequiresMembership proves the MCP calendar
// resolver gates on calendar_members. Reading calendar_subscriptions here
// would be a hole rather than a rename: a subscription is a display
// preference, so anyone who ever toggled a sidebar colour would keep
// access to a calendar they were never granted.
func TestMCPCalendarResolverRequiresMembership(t *testing.T) {
	t.Parallel()

	src := readMCPSource(t, "acl.go")
	if !strings.Contains(src, "FindCalendarMember") {
		t.Fatalf("resolveCalendar must require a calendar_members grant")
	}
	if strings.Contains(src, "FindCalendarSubscription") {
		t.Fatalf("resolveCalendar must not gate on calendar_subscriptions, which grants nothing")
	}
}

// TestMCPFindTaskByPublicIdCentralized proves that no MCP tool bypasses the
// Layer-4 task-visibility ACL by loading a task row directly. Every
// tasks-by-public-id lookup must go through the resolveTask / resolveTaskRow
// helpers in acl.go, so a tool can never read or mutate a task the caller is
// not permitted to see. The guard fails if any non-test file other than
// acl.go references FindTaskByPublicId.
func TestMCPFindTaskByPublicIdCentralized(t *testing.T) {
	t.Parallel()

	const owner = "acl.go"
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatalf("read package dir: %v", err)
	}
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") || name == owner {
			continue
		}
		b, err := os.ReadFile(name)
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		if strings.Contains(string(b), "FindTaskByPublicId") {
			t.Fatalf("%s calls FindTaskByPublicId directly; route task lookups through resolveTask/resolveTaskRow in %s so Layer-4 task visibility cannot be bypassed", name, owner)
		}
	}

	// The centralized helpers must exist and authorize through the shared ACL.
	src := readMCPSource(t, owner)
	for _, want := range []string{
		"func resolveTaskRow(",
		"func loadTaskRow(",
		"acl.AuthorizeTaskAccess",
	} {
		if !strings.Contains(src, want) {
			t.Fatalf("%s must define %s so task lookups stay behind the visibility ACL", owner, want)
		}
	}
}

// projectRoleWriteGates are the only resolvers that apply the Layer-3
// project-role floor. A mutating tool must reach one of them, directly or
// through a helper in this package.
var projectRoleWriteGates = map[string]bool{
	"resolveProjectForWrite": true,
	"resolveTaskForWrite":    true,
	"resolveTaskRowForWrite": true,
}

// writeToolsWithoutProjectGate lists every write-scoped tool whose write
// target is not a task or a project, together with the REST gate it mirrors.
// Tools here are exempt from the project-role floor because REST does not
// impose one on the equivalent route either — the floor must match the HTTP
// transport, not exceed it.
//
// This map is an allowlist, not documentation: a newly registered
// write:workspace tool is absent from it and therefore fails
// [TestMCPWriteToolsPassProjectRoleGate] until it either routes through a
// write resolver or is added here with its REST justification.
var writeToolsWithoutProjectGate = map[string]string{
	"propose_tasks_from":    "LLM proposal that persists nothing",
	"propose_priority":      "read-only proposal behind the Layer-4 visibility ACL",
	"propose_steps":         "read-only proposal behind the Layer-4 visibility ACL; write-scoped because it bills the model",
	"propose_duplicates":    "read-only similarity search behind the Layer-4 visibility ACL; write-scoped because it bills the embedder",
	"propose_relations":     "read-only similarity search behind the Layer-4 visibility ACL; write-scoped because it bills the embedder",
	"propose_lens":          "compiles a query and persists nothing; write-scoped because it bills the model",
	"create_label":          "workspace-level row; REST labels are workspace-member gated",
	"create_page":           "workspace-level row; REST pages are workspace-member gated",
	"update_page":           "workspace-level row; REST pages are workspace-member gated",
	"generate_page":         "workspace-level row; REST pages are workspace-member gated",
	"create_timebox":        "workspace-level row; REST timeboxes are workspace-member gated",
	"add_task_to_timebox":   "timebox membership row; REST timeboxes are workspace-member gated",
	"create_import_job":     "workspace-level row; REST imports are workspace-member gated",
	"triage_intake_item":    "workspace-level queue; REST intake triage is workspace-member gated",
	"add_favorite":          "per-user row that grants nothing and is invisible to others",
	"create_calendar_event": "calendar write; gated by the calendar_members grant",
	"update_calendar_event": "calendar write; gated by calendar_members plus canEditCalendarEvent",
	"delete_calendar_event": "calendar write; gated by calendar_members plus canEditCalendarEvent",
	"toggle_calendar_memo":  "calendar write; gated by the calendar_members grant",
}

// TestMCPWriteToolsPassProjectRoleGate walks the registered tool table and
// proves that every mutating tool whose target lives inside a project passes
// the Layer-3 project-role floor.
//
// Adding the shared resolver was never the hard part; keeping every call site
// on it is. The check is therefore driven by the tool registry rather than by
// a hand-maintained list of functions: a new write:workspace tool that forgets
// the gate fails here the moment it is registered.
func TestMCPWriteToolsPassProjectRoleGate(t *testing.T) {
	t.Parallel()

	h := NewHandler(Deps{})
	if len(h.tools) == 0 {
		t.Fatal("no tools registered")
	}
	graph := mcpPackageCallGraph(t)

	seenExempt := map[string]bool{}
	for name, tl := range h.tools {
		if tl.requiredScope != ScopeWriteWorkspace {
			continue
		}
		if _, exempt := writeToolsWithoutProjectGate[name]; exempt {
			seenExempt[name] = true
			continue
		}
		entry := mcpRunFuncName(t, name, tl.run)
		if !reachesAny(graph, entry, projectRoleWriteGates) {
			t.Errorf("write tool %q (%s) never reaches a project-role gate (%s); "+
				"route it through resolveTaskForWrite / resolveTaskRowForWrite / resolveProjectForWrite, "+
				"or add it to writeToolsWithoutProjectGate with the REST route that justifies the exemption",
				name, entry, strings.Join(sortedKeys(projectRoleWriteGates), " / "))
		}
	}

	for name := range writeToolsWithoutProjectGate {
		if !seenExempt[name] {
			t.Errorf("writeToolsWithoutProjectGate lists %q, which is no longer a registered write:workspace tool; drop the stale entry", name)
		}
	}
}

// TestMCPProjectRoleFloorCentralized proves the role comparison itself lives
// in one place. A tool that re-derives the floor from acl.LookupProjectMembership
// on its own would satisfy the reachability check above while quietly picking a
// different minimum role, so the membership lookups stay confined to acl.go.
func TestMCPProjectRoleFloorCentralized(t *testing.T) {
	t.Parallel()

	const owner = "acl.go"
	for _, name := range mcpPackageSourceFiles(t) {
		if name == owner {
			continue
		}
		b, err := os.ReadFile(name)
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		for _, banned := range []string{"acl.LookupProjectMembership", "acl.CheckProjectMembership"} {
			if strings.Contains(string(b), banned) {
				t.Errorf("%s calls %s directly; the project-role floor belongs in %s so every tool applies the same minimum role", name, banned, owner)
			}
		}
	}

	src := readMCPSource(t, owner)
	for _, want := range []string{
		"func resolveProjectForWrite(",
		"func resolveTaskForWrite(",
		"func resolveTaskRowForWrite(",
		"acl.LookupProjectMembership",
	} {
		if !strings.Contains(src, want) {
			t.Errorf("%s must define %s so MCP writes reuse the REST project-role decision", owner, want)
		}
	}
}

// mcpRunFuncName maps a registered tool's run field back to its top-level
// function name so the AST call graph can be entered at the right node.
func mcpRunFuncName(t *testing.T, tool string, run any) string {
	t.Helper()
	v := reflect.ValueOf(run)
	if v.Kind() != reflect.Func || v.IsNil() {
		t.Fatalf("tool %q has no run function", tool)
	}
	full := runtime.FuncForPC(v.Pointer()).Name()
	return full[strings.LastIndex(full, ".")+1:]
}

// mcpPackageCallGraph parses every non-test file in this package and returns
// the intra-package call graph keyed by function name. Only plain identifier
// calls are recorded, which is exactly the shape the tool implementations and
// their helpers use.
func mcpPackageCallGraph(t *testing.T) map[string]map[string]bool {
	t.Helper()
	fset := token.NewFileSet()
	graph := map[string]map[string]bool{}
	for _, name := range mcpPackageSourceFiles(t) {
		file, err := parser.ParseFile(fset, name, nil, 0)
		if err != nil {
			t.Fatalf("parse %s: %v", name, err)
		}
		for _, decl := range file.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok || fn.Body == nil {
				continue
			}
			callees := graph[fn.Name.Name]
			if callees == nil {
				callees = map[string]bool{}
				graph[fn.Name.Name] = callees
			}
			ast.Inspect(fn.Body, func(n ast.Node) bool {
				call, ok := n.(*ast.CallExpr)
				if !ok {
					return true
				}
				if id, ok := call.Fun.(*ast.Ident); ok {
					callees[id.Name] = true
				}
				return true
			})
		}
	}
	return graph
}

// mcpPackageSourceFiles lists the non-test Go files in this package.
func mcpPackageSourceFiles(t *testing.T) []string {
	t.Helper()
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatalf("read package dir: %v", err)
	}
	var out []string
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		out = append(out, name)
	}
	sort.Strings(out)
	return out
}

// reachesAny reports whether any function reachable from entry is in targets.
func reachesAny(graph map[string]map[string]bool, entry string, targets map[string]bool) bool {
	visited := map[string]bool{}
	var walk func(string) bool
	walk = func(fn string) bool {
		if targets[fn] {
			return true
		}
		if visited[fn] {
			return false
		}
		visited[fn] = true
		for callee := range graph[fn] {
			if walk(callee) {
				return true
			}
		}
		return false
	}
	return walk(entry)
}

func sortedKeys(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func readMCPSource(t *testing.T, name string) string {
	t.Helper()
	b, err := os.ReadFile(name)
	if err != nil {
		t.Fatalf("read %s: %v", name, err)
	}
	return string(b)
}
