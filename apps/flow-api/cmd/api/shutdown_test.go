package main

import (
	"go/ast"
	"go/parser"
	"go/token"
	"strings"
	"testing"
)

// TestShutdownStopsEverySupervisedLoop pins the ordering the shutdown
// sequence depends on: every loop started through bgloop.Start is
// stopped inside that sequence, not on return from main.
//
// The distinction is the whole point. A supervisor restarts a loop that
// returns while its context is still live, so a loop whose stopper only
// runs on return from main comes back up in the middle of the shutdown
// and ticks against a database pool the sequence is closing, for the
// rest of the drain window. A deferred stopper reads as correct and is
// not: it fires after every line that tears the process down.
//
// main is not callable from a test — it binds ports, opens a database
// and blocks on a signal — so this asserts the wiring rather than the
// behaviour it produces. What the loops do once stopped is pinned in
// internal/bgloop.
func TestShutdownStopsEverySupervisedLoop(t *testing.T) {
	t.Parallel()

	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "main.go", nil, 0)
	if err != nil {
		t.Fatalf("parse main.go: %v", err)
	}
	body := mainBody(t, file)

	stoppers := supervisedStoppers(body)
	if len(stoppers) == 0 {
		t.Fatal("main.go starts no supervised loop through bgloop.Start; either the wiring moved or this guard no longer covers it")
	}

	shutdown := shutdownStatements(t, body)
	var missing []string
	for _, name := range stoppers {
		if !usedIn(shutdown, name) {
			missing = append(missing, name)
		}
	}
	if len(missing) > 0 {
		t.Fatalf("these supervised loops are never stopped in the shutdown sequence, so their supervisors "+
			"restart them into a process that is shutting down (a deferred stopper does not count: it runs "+
			"after the drain, not before it): %s", strings.Join(missing, ", "))
	}
}

// mainBody returns the body of func main.
func mainBody(t *testing.T, file *ast.File) *ast.BlockStmt {
	t.Helper()
	for _, decl := range file.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if ok && fn.Recv == nil && fn.Name.Name == "main" {
			return fn.Body
		}
	}
	t.Fatal("main.go has no func main")
	return nil
}

// supervisedStoppers returns the names the stoppers of bgloop.Start are
// assigned to, anywhere in main — including the slice a group of loops
// appends into.
func supervisedStoppers(body *ast.BlockStmt) []string {
	seen := map[string]bool{}
	var out []string
	ast.Inspect(body, func(n ast.Node) bool {
		assign, ok := n.(*ast.AssignStmt)
		if !ok || !containsStartCall(assign.Rhs) {
			return true
		}
		for _, lhs := range assign.Lhs {
			ident, ok := lhs.(*ast.Ident)
			if !ok || ident.Name == "_" || seen[ident.Name] {
				continue
			}
			seen[ident.Name] = true
			out = append(out, ident.Name)
		}
		return true
	})
	return out
}

// containsStartCall reports whether any expression calls bgloop.Start.
func containsStartCall(exprs []ast.Expr) bool {
	found := false
	for _, expr := range exprs {
		ast.Inspect(expr, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}
			sel, ok := call.Fun.(*ast.SelectorExpr)
			if !ok {
				return true
			}
			pkg, ok := sel.X.(*ast.Ident)
			if ok && pkg.Name == "bgloop" && sel.Sel.Name == "Start" {
				found = true
				return false
			}
			return true
		})
	}
	return found
}

// shutdownStatements returns the statements of main that follow the
// select on the shutdown signal — the sequence that tears the process
// down.
func shutdownStatements(t *testing.T, body *ast.BlockStmt) []ast.Stmt {
	t.Helper()
	for i, stmt := range body.List {
		if _, ok := stmt.(*ast.SelectStmt); ok {
			return body.List[i+1:]
		}
	}
	t.Fatal("main no longer waits on a select before shutting down; this guard needs to be pointed at the new shape")
	return nil
}

// usedIn reports whether name is called, or ranged over, by one of the
// statements. Deferred statements do not count: a stopper reached only
// by a defer runs after the whole shutdown sequence, which is the fault
// this guard exists to catch.
func usedIn(stmts []ast.Stmt, name string) bool {
	for _, stmt := range stmts {
		if _, ok := stmt.(*ast.DeferStmt); ok {
			continue
		}
		used := false
		ast.Inspect(stmt, func(n ast.Node) bool {
			switch node := n.(type) {
			case *ast.DeferStmt:
				return false
			case *ast.CallExpr:
				if ident, ok := node.Fun.(*ast.Ident); ok && ident.Name == name {
					used = true
					return false
				}
			case *ast.RangeStmt:
				if ident, ok := node.X.(*ast.Ident); ok && ident.Name == name {
					used = true
					return false
				}
			}
			return true
		})
		if used {
			return true
		}
	}
	return false
}
