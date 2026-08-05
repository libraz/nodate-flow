package eventbus

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// appendFuncs are the entry points that put a row in the events log.
// Discarding what any of them returns is what this guard rejects.
var appendFuncs = map[string]bool{
	"Append":             true,
	"AppendJudgeEvent":   true,
	"AppendReverseEvent": true,
}

// TestNoSwallowedAppends proves every event append in the module either
// propagates its failure or goes through [AppendBestEffort].
//
// The guard is a whole-module walk rather than a package-local check
// because the writers are spread across internal/mcp, internal/ai,
// internal/http/handlers and the workers, and the defect was never one
// call site: the same discarded error appeared independently in two
// dozen places while fifty-odd neighbours checked it, so a review-time
// rule had already failed to hold. Dropping a row is not cosmetic —
// task state is derived from the event log (CLAUDE.md rule 8), so a
// missing row is a wrong state that nothing later corrects.
//
// The check parses each file and looks for an assignment of an append
// call to the blank identifier. Matching source text instead would be
// wrong in both directions: a commented-out example would fail the build
// for nothing, and reformatting the call across two lines, or inserting
// a second space, would walk straight past it. The AST knows which
// spellings are the same statement.
//
// Choosing between propagation and [AppendBestEffort] is a real
// decision; see that function for the criterion.
func TestNoSwallowedAppends(t *testing.T) {
	t.Parallel()

	root := flowAPIModuleRoot(t)
	var offenders []string

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
		file, perr := parser.ParseFile(fset, path, nil, 0)
		if perr != nil {
			// A file that does not parse cannot compile either, so the
			// build already rejects it and this guard has nothing to add.
			// Failing here instead would turn any half-written file in
			// the tree into a confusing failure of an unrelated check.
			return nil
		}
		rel, relErr := filepath.Rel(root, path)
		if relErr != nil {
			return relErr
		}
		rel = filepath.ToSlash(rel)
		inEventbus := file.Name.Name == "eventbus"

		ast.Inspect(file, func(n ast.Node) bool {
			assign, ok := n.(*ast.AssignStmt)
			if !ok || !allBlank(assign.Lhs) || len(assign.Rhs) != 1 {
				return true
			}
			call, ok := assign.Rhs[0].(*ast.CallExpr)
			if !ok {
				return true
			}
			if fn, ok := appendCallName(call, inEventbus); ok {
				offenders = append(offenders, fmt.Sprintf("%s:%d discards the result of %s",
					rel, fset.Position(assign.Pos()).Line, fn))
			}
			return true
		})
		return nil
	})
	if err != nil {
		t.Fatalf("walk module: %v", err)
	}

	if len(offenders) > 0 {
		t.Fatalf("an append error may not be discarded: propagate it, or call "+
			"eventbus.AppendBestEffort with a call site so the dropped row is recorded:\n  %s",
			strings.Join(offenders, "\n  "))
	}
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

// appendCallName reports whether call targets one of the append entry
// points, and under what name. A qualified call must name this package
// so an unrelated Append method on some other type is not mistaken for
// one of ours; inside the package itself the call is unqualified.
func appendCallName(call *ast.CallExpr, inEventbus bool) (string, bool) {
	switch fn := call.Fun.(type) {
	case *ast.SelectorExpr:
		pkg, ok := fn.X.(*ast.Ident)
		if !ok || pkg.Name != "eventbus" || !appendFuncs[fn.Sel.Name] {
			return "", false
		}
		return "eventbus." + fn.Sel.Name, true
	case *ast.Ident:
		if !inEventbus || !appendFuncs[fn.Name] {
			return "", false
		}
		return fn.Name, true
	}
	return "", false
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
