package middleware_test

import (
	"go/ast"
	"go/parser"
	"go/token"
	"testing"
)

// The service-token guards compare a secret against a value an attacker
// chooses, once per request, on a surface reachable by anyone who can
// reach the port. That comparison has to be constant-time, and nothing
// about a request proves it is: a byte-at-a-time comparison answers
// every test in this repo exactly as the constant-time one does, and
// differs only in how long it takes to say so. Measuring the difference
// is worse than not checking — a wall-clock timing assertion on shared
// CI hardware fails on load and teaches people to re-run red runs.
//
// So the property is pinned where it is written instead. The check reads
// signals_token.go and requires that each guard compares the parsed
// bearer through crypto/subtle, and that no ordinary equality reaches a
// value derived from the Authorization header. Replacing the call with
// bytes.Equal fails here, which is the only place it can fail.

// signalsTokenSource is the file the guards live in, relative to this
// package directory.
const signalsTokenSource = "signals_token.go"

// serviceTokenGuards are the middlewares whose comparison is checked.
// Naming them means a guard that is renamed away, or a new one added
// without the check, is reported rather than skipped.
var serviceTokenGuards = []string{"RequireSignalsAuth", "RequireServiceTokenOnly"}

// bannedComparisons are calls that compare two values in time
// proportional to their common prefix. Any of them reaching a
// header-derived value is the defect this check exists for.
var bannedComparisons = map[string]bool{
	"bytes.Equal":       true,
	"bytes.EqualFold":   true,
	"bytes.Compare":     true,
	"bytes.HasPrefix":   true,
	"strings.EqualFold": true,
	"strings.Compare":   true,
	"strings.HasPrefix": true,
	"strings.Contains":  true,
	"reflect.DeepEqual": true,
	"slices.Equal":      true,
}

func TestServiceTokenGuardsCompareInConstantTime(t *testing.T) {
	t.Parallel()

	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, signalsTokenSource, nil, parser.ParseComments)
	if err != nil {
		t.Fatalf("parse %s: %v", signalsTokenSource, err)
	}

	funcs := map[string]*ast.FuncDecl{}
	for _, decl := range file.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok || fn.Body == nil || fn.Recv != nil {
			continue
		}
		funcs[fn.Name.Name] = fn
	}

	for _, name := range serviceTokenGuards {
		fn, ok := funcs[name]
		if !ok {
			t.Errorf("%s declares no %s; the guard this check protects is not there to protect", signalsTokenSource, name)
			continue
		}
		checkGuardComparison(t, name, fn)
	}
}

// checkGuardComparison resolves one guard: find what the Authorization
// header flows into, require that crypto/subtle is what reads it, and
// reject every other comparison that does.
func checkGuardComparison(t *testing.T, guard string, fn *ast.FuncDecl) {
	t.Helper()

	derived := headerDerivedNames(fn)
	if len(derived) == 0 {
		t.Errorf("%s: no value in the body comes from parsing the Authorization header, so this check cannot tell what the guard compares — has the parse moved, or the guard stopped reading the header?", guard)
		return
	}

	constantTime := 0
	ast.Inspect(fn.Body, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		qualified := qualifiedCallName(call)
		if qualified == "subtle.ConstantTimeCompare" {
			for _, arg := range call.Args {
				if mentionsAny(arg, derived) {
					constantTime++
					break
				}
			}
			return true
		}
		if !bannedComparisons[qualified] {
			return true
		}
		for _, arg := range call.Args {
			if mentionsAny(arg, derived) {
				t.Errorf("%s: compares the presented bearer with %s, which returns as soon as the values differ — a caller can then extend a guess one byte at a time; the comparison must go through subtle.ConstantTimeCompare",
					guard, qualified)
				break
			}
		}
		return true
	})

	if constantTime == 0 {
		t.Errorf("%s: nothing in the body passes the presented bearer to subtle.ConstantTimeCompare; the secret is compared some other way, or not at all", guard)
	}

	ast.Inspect(fn.Body, func(n ast.Node) bool {
		bin, ok := n.(*ast.BinaryExpr)
		if !ok || (bin.Op != token.EQL && bin.Op != token.NEQ) {
			return true
		}
		for _, side := range []ast.Expr{bin.X, bin.Y} {
			// The result of the constant-time comparison is an int and
			// has to be tested against 1 somehow; that operand mentions
			// the bearer only because the call it wraps does.
			if isConstantTimeCall(side) {
				continue
			}
			if mentionsAny(side, derived) {
				t.Errorf("%s: the presented bearer reaches an %s comparison, which stops at the first differing byte; only subtle.ConstantTimeCompare may read it",
					guard, bin.Op)
			}
		}
		return true
	})
}

// headerDerivedNames returns the identifiers the guard binds to
// something read out of the Authorization header, following the shared
// parser call. An expression mentioning one of these is a value an
// attacker supplied.
func headerDerivedNames(fn *ast.FuncDecl) map[string]bool {
	out := map[string]bool{}
	ast.Inspect(fn.Body, func(n ast.Node) bool {
		assign, ok := n.(*ast.AssignStmt)
		if !ok || len(assign.Rhs) != 1 {
			return true
		}
		if !readsAuthorizationHeader(assign.Rhs[0]) {
			return true
		}
		for _, lhs := range assign.Lhs {
			id, ok := lhs.(*ast.Ident)
			if !ok || id.Name == "_" {
				continue
			}
			out[id.Name] = true
		}
		return true
	})
	return out
}

// readsAuthorizationHeader reports whether the expression pulls the
// Authorization header value, directly or through the shared bearer
// parser.
func readsAuthorizationHeader(expr ast.Expr) bool {
	found := false
	ast.Inspect(expr, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		if qualifiedCallName(call) == "authn.BearerFromHeader" {
			found = true
			return false
		}
		sel, ok := call.Fun.(*ast.SelectorExpr)
		if !ok || sel.Sel.Name != "Get" {
			return true
		}
		for _, arg := range call.Args {
			lit, ok := arg.(*ast.BasicLit)
			if ok && lit.Kind == token.STRING && lit.Value == `"Authorization"` {
				found = true
				return false
			}
		}
		return true
	})
	return found
}

// isConstantTimeCall reports whether the expression is exactly a
// subtle.ConstantTimeCompare call.
func isConstantTimeCall(expr ast.Expr) bool {
	call, ok := expr.(*ast.CallExpr)
	return ok && qualifiedCallName(call) == "subtle.ConstantTimeCompare"
}

// mentionsAny reports whether the expression reads any of the named
// identifiers.
func mentionsAny(expr ast.Expr, names map[string]bool) bool {
	found := false
	ast.Inspect(expr, func(n ast.Node) bool {
		if id, ok := n.(*ast.Ident); ok && names[id.Name] {
			found = true
			return false
		}
		return true
	})
	return found
}

// qualifiedCallName renders a call's callee as "package.Function", or
// the bare name for a call within the file's own package. A method call
// on a value yields "", so a helper on some struct is never mistaken for
// a package function of the same name.
func qualifiedCallName(call *ast.CallExpr) string {
	switch fun := call.Fun.(type) {
	case *ast.Ident:
		return fun.Name
	case *ast.SelectorExpr:
		qualifier, ok := fun.X.(*ast.Ident)
		if !ok {
			return ""
		}
		return qualifier.Name + "." + fun.Sel.Name
	default:
		return ""
	}
}
