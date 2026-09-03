package eventbus

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// appendFuncs are the entry points that put a row in the events log.
// Losing what any of them returns is what this guard rejects.
var appendFuncs = map[string]bool{
	"Append":             true,
	"AppendJudgeEvent":   true,
	"AppendReverseEvent": true,
}

// appendPackages are the packages those entry points live in. There are
// two appenders writing the same table — this one and the
// service-agnostic eventlog shared with auth-api — and a row lost through
// either leaves the same gap, so the guard does not distinguish them.
var appendPackages = map[string]bool{
	"eventbus": true,
	"eventlog": true,
}

// swallow is one place an append failure goes no further.
type swallow struct {
	// File is the path relative to the module root, slash-separated.
	File string
	// Line is where the offending statement starts.
	Line int
	// Entry is the append entry point whose failure was lost.
	Entry string
	// Why names the shape, so the message says what to change.
	Why string
}

func (s swallow) String() string {
	return fmt.Sprintf("%s:%d %s: %s", s.File, s.Line, s.Entry, s.Why)
}

// TestNoSwallowedAppends proves every event append in the module either
// propagates its failure or goes through [AppendBestEffort].
//
// The guard is a whole-module walk rather than a package-local check
// because the writers are spread across internal/mcp, internal/ai,
// internal/http/handlers and the workers, and the failure mode is not one
// call site: the same lost error turns up independently wherever an
// append is written, alongside neighbours that check it, which is what a
// review-time rule fails to hold. Dropping a row is not cosmetic — task
// state is derived from the event log (CLAUDE.md rule 8), so a missing
// row is a wrong state that nothing later corrects.
//
// Three shapes count as losing the failure, and the middle one is the
// reason the guard reads the control flow rather than the assignment:
//
//   - assigning the result to the blank identifier,
//   - checking the error and then continuing anyway, however loudly the
//     branch logs — a log line is not a repair, and the request goes on
//     to report success for a change the log does not describe,
//   - calling the entry point as a bare statement, which discards the
//     result without even naming it.
//
// The check parses each file rather than matching source text, which
// would be wrong in both directions: a commented-out example would fail
// the build for nothing, and reformatting a call across two lines would
// walk straight past it. The AST knows which spellings are the same
// statement.
//
// Choosing between propagation and [AppendBestEffort] is a real
// decision; see that function for the criterion.
func TestNoSwallowedAppends(t *testing.T) {
	t.Parallel()

	offenders, err := scanSwallowedAppends(flowAPIModuleRoot(t))
	if err != nil {
		t.Fatalf("scan module: %v", err)
	}
	if len(offenders) == 0 {
		return
	}
	lines := make([]string, 0, len(offenders))
	for _, o := range offenders {
		lines = append(lines, o.String())
	}
	t.Fatalf("an append failure must reach a return: propagate it, or call "+
		"eventbus.AppendBestEffort with a call site so the dropped row is recorded:\n  %s",
		strings.Join(lines, "\n  "))
}

// parsedFile is one source file kept around for both passes: the facade
// derivation reads every file before the offence walk can start.
type parsedFile struct {
	rel  string
	pkg  string
	fset *token.FileSet
	file *ast.File
}

// scanSwallowedAppends walks the module rooted at root and returns every
// place an append failure stops travelling.
func scanSwallowedAppends(root string) ([]swallow, error) {
	files, err := parseModule(root)
	if err != nil {
		return nil, err
	}
	facades := deriveFacades(files)

	var offenders []swallow
	for _, pf := range files {
		names := func(call *ast.CallExpr) (string, bool) {
			return appendEntryPoint(call, pf.pkg, facades)
		}
		report := func(pos token.Pos, entry, why string) {
			offenders = append(offenders, swallow{
				File:  pf.rel,
				Line:  pf.fset.Position(pos).Line,
				Entry: entry,
				Why:   why,
			})
		}
		ast.Inspect(pf.file, func(n ast.Node) bool {
			switch s := n.(type) {
			case *ast.AssignStmt:
				if !allBlank(s.Lhs) || len(s.Rhs) != 1 {
					return true
				}
				call, ok := s.Rhs[0].(*ast.CallExpr)
				if !ok {
					return true
				}
				if entry, ok := names(call); ok {
					report(s.Pos(), entry, "the result is assigned to the blank identifier")
				}
			case *ast.ExprStmt:
				call, ok := s.X.(*ast.CallExpr)
				if !ok {
					return true
				}
				if entry, ok := names(call); ok {
					report(s.Pos(), entry, "the result is discarded by calling it as a statement")
				}
			case *ast.IfStmt:
				init, ok := s.Init.(*ast.AssignStmt)
				if !ok || len(init.Rhs) != 1 {
					return true
				}
				call, ok := init.Rhs[0].(*ast.CallExpr)
				if !ok {
					return true
				}
				entry, ok := names(call)
				if !ok {
					return true
				}
				if !blockExits(s.Body) {
					report(s.Pos(), entry, "the failure is handled without leaving the function")
				}
			}
			return true
		})
	}
	sort.Slice(offenders, func(i, j int) bool {
		if offenders[i].File != offenders[j].File {
			return offenders[i].File < offenders[j].File
		}
		return offenders[i].Line < offenders[j].Line
	})
	return offenders, nil
}

// parseModule reads every non-test Go file under root. A file that does
// not parse is skipped rather than reported: it cannot compile either, so
// the build already rejects it and this guard has nothing to add.
// Failing here instead would turn any half-written file in the tree into
// a confusing failure of an unrelated check.
func parseModule(root string) ([]parsedFile, error) {
	var files []parsedFile
	err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		name := d.Name()
		if !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			return nil
		}
		fset := token.NewFileSet()
		parsed, perr := parser.ParseFile(fset, path, nil, 0)
		if perr != nil {
			return nil
		}
		rel, relErr := filepath.Rel(root, path)
		if relErr != nil {
			return relErr
		}
		files = append(files, parsedFile{
			rel:  filepath.ToSlash(rel),
			pkg:  parsed.Name.Name,
			fset: fset,
			file: parsed,
		})
		return nil
	})
	if err != nil {
		return nil, err
	}
	sort.Slice(files, func(i, j int) bool { return files[i].rel < files[j].rel })
	return files, nil
}

// deriveFacades returns the wrappers that are an append entry point by
// another name, keyed "package.Func".
//
// A wrapper counts only when it hands the append's error straight back —
// `return eventbus.Append(...)` and nothing else on that path. Such a
// function adds no failure of its own, so losing what it returns loses
// exactly the append, and a guard that stopped at the qualified call
// would have declared the tree clean while three dozen call sites threw
// the same error away one indirection further out. The derivation
// iterates to a fixed point so a wrapper around a wrapper is caught too.
//
// The bar is deliberately this narrow. A function that appends among
// other work owns a failure of its own, and its callers are answering a
// different question than this guard asks.
func deriveFacades(files []parsedFile) map[string]bool {
	facades := map[string]bool{}
	for {
		added := false
		for _, pf := range files {
			for _, decl := range pf.file.Decls {
				fn, ok := decl.(*ast.FuncDecl)
				if !ok || fn.Body == nil || fn.Recv != nil {
					continue
				}
				key := pf.pkg + "." + fn.Name.Name
				if facades[key] {
					continue
				}
				if !returnsAppendDirectly(fn.Body, pf.pkg, facades) {
					continue
				}
				facades[key] = true
				added = true
			}
		}
		if !added {
			return facades
		}
	}
}

// returnsAppendDirectly reports whether the body has a `return <append>`
// statement whose single result is the append call itself.
func returnsAppendDirectly(body *ast.BlockStmt, pkg string, facades map[string]bool) bool {
	found := false
	ast.Inspect(body, func(n ast.Node) bool {
		if found {
			return false
		}
		ret, ok := n.(*ast.ReturnStmt)
		if !ok || len(ret.Results) != 1 {
			return true
		}
		call, ok := ret.Results[0].(*ast.CallExpr)
		if !ok {
			return true
		}
		if _, ok := appendEntryPoint(call, pkg, facades); ok {
			found = true
			return false
		}
		return true
	})
	return found
}

// appendEntryPoint reports whether call targets something that appends to
// the event log, and under what name.
//
// A qualified call must name one of the appender packages, or a package
// that declares a facade under that name, so an unrelated Append method
// on some other type is not mistaken for one of ours. Inside an appender
// package the call is unqualified; so is a call to a facade from its own
// package.
func appendEntryPoint(call *ast.CallExpr, pkg string, facades map[string]bool) (string, bool) {
	switch fn := call.Fun.(type) {
	case *ast.SelectorExpr:
		qualifier, ok := fn.X.(*ast.Ident)
		if !ok {
			return "", false
		}
		if appendPackages[qualifier.Name] && appendFuncs[fn.Sel.Name] {
			return qualifier.Name + "." + fn.Sel.Name, true
		}
		key := qualifier.Name + "." + fn.Sel.Name
		if facades[key] {
			return key, true
		}
	case *ast.Ident:
		if appendPackages[pkg] && appendFuncs[fn.Name] {
			return fn.Name, true
		}
		key := pkg + "." + fn.Name
		if facades[key] {
			return key, true
		}
	}
	return "", false
}

// allBlank reports whether every expression on the left of an
// assignment is the blank identifier, i.e. the whole result is thrown
// away.
func allBlank(lhs []ast.Expr) bool {
	if len(lhs) == 0 {
		return false
	}
	for _, e := range lhs {
		id, ok := e.(*ast.Ident)
		if !ok || id.Name != "_" {
			return false
		}
	}
	return true
}

// blockExits reports whether control leaves the enclosing function at the
// end of b.
//
// Only the forms an error branch actually uses are recognised: a return,
// a panic, and an if/else where both arms leave. Anything else — logging
// and falling through, `continue`, a bare `break` — keeps the function
// running past a failure it decided not to act on, which is the shape
// this guard exists to name. A branch that leaves by some form not listed
// here is reported too; the analysis errs toward asking for proof.
func blockExits(b *ast.BlockStmt) bool {
	if b == nil {
		return false
	}
	for i := len(b.List) - 1; i >= 0; i-- {
		if _, empty := b.List[i].(*ast.EmptyStmt); empty {
			continue
		}
		return stmtExits(b.List[i])
	}
	return false
}

// stmtExits reports whether s is a terminating statement.
func stmtExits(s ast.Stmt) bool {
	switch s := s.(type) {
	case *ast.ReturnStmt:
		return true
	case *ast.ExprStmt:
		call, ok := s.X.(*ast.CallExpr)
		if !ok {
			return false
		}
		id, ok := call.Fun.(*ast.Ident)
		return ok && id.Name == "panic"
	case *ast.BlockStmt:
		return blockExits(s)
	case *ast.IfStmt:
		return s.Else != nil && blockExits(s.Body) && stmtExits(s.Else)
	case *ast.LabeledStmt:
		return stmtExits(s.Stmt)
	}
	return false
}

// TestAppendBestEffortStaysAccountable pins the two things that make
// [AppendBestEffort] an acceptable alternative to propagation. Reducing
// it to a silent swallow would leave the guard above green while
// restoring exactly the behaviour it exists to prevent.
//
// The keys are looked for as string literals in the parsed function
// body, not in its source text: a doc comment mentioning them reads the
// same to a text search and proves nothing.
func TestAppendBestEffortStaysAccountable(t *testing.T) {
	t.Parallel()

	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "append.go", nil, 0)
	if err != nil {
		t.Fatalf("parse append.go: %v", err)
	}

	var body *ast.BlockStmt
	for _, decl := range file.Decls {
		if fn, ok := decl.(*ast.FuncDecl); ok && fn.Name.Name == "AppendBestEffort" {
			body = fn.Body
			break
		}
	}
	if body == nil {
		t.Fatal("could not locate AppendBestEffort")
	}

	literals := map[string]bool{}
	ast.Inspect(body, func(n ast.Node) bool {
		if lit, ok := n.(*ast.BasicLit); ok && lit.Kind == token.STRING {
			literals[strings.Trim(lit.Value, `"`)] = true
		}
		return true
	})

	for _, want := range []string{
		"call_site", // who dropped it
		"payload",   // and what the row would have said
	} {
		if !literals[want] {
			t.Errorf("AppendBestEffort must log %s: without it a dropped event cannot be replayed", want)
		}
	}
}

// flowAPIModuleRoot returns the apps/flow-api directory. Tests run in
// the package directory, so the module root is two levels up from
// internal/eventbus.
func flowAPIModuleRoot(t *testing.T) string {
	t.Helper()
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatalf("resolve module root: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "go.mod")); err != nil {
		t.Fatalf("expected the flow-api module root at %s: %v", root, err)
	}
	return root
}
