package bgloop

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

// unsupervisedInMain are the goroutines cmd/api/main.go is allowed to
// start without this package, by the name of the function they call.
//
// Both are HTTP listeners, not tick loops. Restarting a listener that
// failed is wrong — a bind error repeats forever, and a panic inside a
// request is already recovered by the router middleware — so they
// report and let the process shut down instead. The list is two entries
// long and expected to stay that way; anything else that runs for the
// life of the process belongs under [StartManaged].
var unsupervisedInMain = map[string]string{
	"ListenAndServe": "http listener: a failed bind must not be retried in a loop",
}

// TestEveryResidentLoopIsSupervised checks that the loops actually go
// through this package, not merely that this package exists.
//
// A reviewer cannot see the absence of a call, so the check looks for
// unmanaged goroutines and ensures main uses waitable resident handles.
//
// Two shapes are checked, matching how a resident loop actually gets
// started in this codebase:
//
//   - managed handles wired in cmd/api/main.go, where shutdown must
//     retain both cancellation and whole-supervisor completion;
//   - `go x.loop(...)` / `go x.Loop(...)` anywhere under internal/,
//     which is how a component starts its own loop from Start, and
//     `go bgloop.Run(...)` there, which supervises a loop nobody can wait
//     for.
func TestEveryResidentLoopIsSupervised(t *testing.T) {
	t.Parallel()

	root := flowAPIModuleRoot(t)
	var offenders []string

	offenders = append(offenders, unsupervisedGoStmtsInMain(t, root)...)
	offenders = append(offenders, unmanagedStartsInMain(t, root)...)
	offenders = append(offenders, unsupervisedLoopStarts(t, root)...)

	if len(offenders) > 0 {
		t.Fatalf("every resident loop must be supervised and registered for completion waiting, "+
			"so a panic cannot end the process and a loop that stops on its own "+
			"is reported instead of simply ceasing:\n  %s", strings.Join(offenders, "\n  "))
	}
}

// unsupervisedGoStmtsInMain reports `go` statements in main.go that do
// not appear in the allowlist.
//
// `go Run(...)` counts as an offender here, unlike under internal/: it
// leaves cancellation separate from the supervisor. Main registers each
// managed handle with a whole-run Wait in the shutdown coordinator.
func unsupervisedGoStmtsInMain(t *testing.T, root string) []string {
	t.Helper()
	path := filepath.Join(root, "cmd", "api", "main.go")
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, path, nil, 0)
	if err != nil {
		t.Fatalf("parse main.go: %v", err)
	}

	var out []string
	ast.Inspect(file, func(n ast.Node) bool {
		goStmt, ok := n.(*ast.GoStmt)
		if !ok {
			return true
		}
		line := fset.Position(goStmt.Pos()).Line
		switch {
		case callsRun(goStmt.Call):
			out = append(out, fmt.Sprintf("cmd/api/main.go:%d supervises a loop with `go bgloop.Run`, "+
				"which leaves its cancel separate from its stop; use bgloop.Start", line))
		case startsAllowedListener(goStmt.Call):
		default:
			out = append(out, fmt.Sprintf("cmd/api/main.go:%d starts a goroutine outside bgloop.Run", line))
		}
		return true
	})
	return out
}

// unmanagedStartsInMain requires main to start residents through
// StartManaged and rejects the legacy non-waitable Start API. The cmd/api
// shutdown guard verifies that every handle is registered and waited.
func unmanagedStartsInMain(t *testing.T, root string) []string {
	t.Helper()
	path := filepath.Join(root, "cmd", "api", "main.go")
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, path, nil, 0)
	if err != nil {
		t.Fatalf("parse main.go: %v", err)
	}

	managed, legacy := 0, 0
	ast.Inspect(file, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		sel, ok := call.Fun.(*ast.SelectorExpr)
		if !ok {
			return true
		}
		pkg, ok := sel.X.(*ast.Ident)
		if !ok || pkg.Name != "bgloop" {
			return true
		}
		switch sel.Sel.Name {
		case "StartManaged":
			managed++
		case "Start":
			legacy++
		}
		return true
	})

	var out []string
	if managed == 0 {
		out = append(out, "cmd/api/main.go starts no resident through bgloop.StartManaged")
	}
	if legacy > 0 {
		out = append(out, "cmd/api/main.go uses bgloop.Start without whole-supervisor completion")
	}
	return out
}

// unsupervisedLoopStarts reports `go x.loop(...)` statements under
// internal/, the shape a component uses to start its own resident loop,
// and `go bgloop.Run(...)`, whose supervisor no shutdown can wait for.
func unsupervisedLoopStarts(t *testing.T, root string) []string {
	t.Helper()
	var out []string
	err := filepath.WalkDir(filepath.Join(root, "internal"), func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		fset := token.NewFileSet()
		file, perr := parser.ParseFile(fset, path, nil, 0)
		if perr != nil {
			// Unparseable means uncompilable; the build owns that.
			return nil
		}
		rel, _ := filepath.Rel(root, path)
		rel = filepath.ToSlash(rel)
		ast.Inspect(file, func(n ast.Node) bool {
			goStmt, ok := n.(*ast.GoStmt)
			if !ok {
				return true
			}
			if callsRun(goStmt.Call) {
				out = append(out, fmt.Sprintf("%s:%d supervises a loop with `go bgloop.Run`, which nothing can wait for; "+
					"return a bgloop.StartManaged handle", rel, fset.Position(goStmt.Pos()).Line))
				return true
			}
			sel, ok := goStmt.Call.Fun.(*ast.SelectorExpr)
			if !ok {
				return true
			}
			if name := sel.Sel.Name; name == "loop" || name == "Loop" {
				out = append(out, fmt.Sprintf("%s:%d starts %s outside bgloop.Run",
					rel, fset.Position(goStmt.Pos()).Line, name))
			}
			return true
		})
		return nil
	})
	if err != nil {
		t.Fatalf("walk internal: %v", err)
	}
	return out
}

// callsRun reports whether the call is bgloop.Run(...) — or Run(...)
// from inside this package.
func callsRun(call *ast.CallExpr) bool {
	switch fn := call.Fun.(type) {
	case *ast.SelectorExpr:
		pkg, ok := fn.X.(*ast.Ident)
		return ok && pkg.Name == "bgloop" && fn.Sel.Name == "Run"
	case *ast.Ident:
		return fn.Name == "Run"
	}
	return false
}

// startsAllowedListener reports whether the goroutine body only starts
// one of the allowlisted non-loop servers. The body is inspected rather
// than the call target because these are wrapped in a closure that also
// logs.
func startsAllowedListener(call *ast.CallExpr) bool {
	lit, ok := call.Fun.(*ast.FuncLit)
	if !ok {
		return false
	}
	allowed := false
	ast.Inspect(lit.Body, func(n ast.Node) bool {
		inner, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		sel, ok := inner.Fun.(*ast.SelectorExpr)
		if !ok {
			return true
		}
		if _, ok := unsupervisedInMain[sel.Sel.Name]; ok {
			allowed = true
			return false
		}
		return true
	})
	return allowed
}

// flowAPIModuleRoot returns the apps/flow-api directory. Tests run in
// the package directory, so the module root is two levels up from
// internal/bgloop.
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
