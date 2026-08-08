package calendars

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"strings"
	"testing"
)

// timezoneValidators are the two functions that put a client-supplied
// timezone through region.ValidateTimezone. requireValidTimezone is the
// direct check; resolveEffectiveTimezone is the fallback chain, which
// checks its explicit argument through requireValidTimezone.
var timezoneValidators = map[string]bool{
	"requireValidTimezone":     true,
	"resolveEffectiveTimezone": true,
}

// TestEveryTimezoneWriteIsValidated proves no handler in this package
// can put a timezone into a sqlc write without checking it first.
//
// region.ValidateTimezone existed, and was called — from the share
// endpoints and from the fallback resolver. Event create and patch
// simply did not call it, and stored "JST" or "GMT+9" verbatim: the
// event came back from GET looking fine and never appeared on a grid,
// because every renderer resolves the zone and gets nothing. That is
// the shape this repository keeps producing — a shared helper exists,
// and one write path does not reach it — so a check that only covers
// the three call sites known today would be a check on the wrong thing.
//
// The rule is therefore about the write, not about the endpoint: a
// function that assigns Timezone into a `...Params` value has to
// mention one of [timezoneValidators]. That is coarse on purpose. It
// cannot tell whether the checked value is the assigned one, so it will
// not catch a deliberate bypass; what it catches is the omission, which
// is what actually happened four times. A new endpoint that forgets
// fails here rather than in a bug report about events that do not show
// up.
//
// Read paths are untouched: a Timezone copied out of a row into a
// response DTO is a value the database already accepted, and the
// literal it lands in is a response type, not a `...Params`.
func TestEveryTimezoneWriteIsValidated(t *testing.T) {
	t.Parallel()

	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatalf("read package dir: %v", err)
	}

	var offenders []string
	fset := token.NewFileSet()
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		file, perr := parser.ParseFile(fset, name, nil, 0)
		if perr != nil {
			// A file that does not parse cannot compile, so the build
			// already rejects it and this guard has nothing to add.
			continue
		}
		for _, decl := range file.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok || fn.Body == nil {
				continue
			}
			lines := timezoneWriteLines(fset, fn)
			if len(lines) == 0 || callsAny(fn, timezoneValidators) {
				continue
			}
			for _, line := range lines {
				offenders = append(offenders,
					fmt.Sprintf("%s:%d %s writes Timezone into a query parameter", name, line, fn.Name.Name))
			}
		}
	}

	if len(offenders) > 0 {
		t.Fatalf("a timezone reaching a column must be validated first: call "+
			"requireValidTimezone, or resolveEffectiveTimezone when the field is "+
			"optional and falls back to the user / workspace preference:\n  %s",
			strings.Join(offenders, "\n  "))
	}
}

// timezoneWriteLines returns the lines in fn that put a Timezone into a
// sqlc query parameter, in either spelling: a `Timezone:` field of a
// `...Params` composite literal, or an assignment to `.Timezone` on a
// value built from one.
func timezoneWriteLines(fset *token.FileSet, fn *ast.FuncDecl) []int {
	var lines []int
	ast.Inspect(fn.Body, func(n ast.Node) bool {
		switch node := n.(type) {
		case *ast.CompositeLit:
			if !isParamsType(node.Type) {
				return true
			}
			for _, elt := range node.Elts {
				kv, ok := elt.(*ast.KeyValueExpr)
				if !ok {
					continue
				}
				if key, ok := kv.Key.(*ast.Ident); ok && key.Name == "Timezone" {
					lines = append(lines, fset.Position(kv.Pos()).Line)
				}
			}
		case *ast.AssignStmt:
			for _, lhs := range node.Lhs {
				sel, ok := lhs.(*ast.SelectorExpr)
				if ok && sel.Sel.Name == "Timezone" {
					lines = append(lines, fset.Position(sel.Pos()).Line)
				}
			}
		}
		return true
	})
	return lines
}

// isParamsType reports whether a composite literal's type is one of the
// sqlc-generated argument structs, whose names all end in Params. The
// package qualifier is not checked: nothing else in this package names
// a type that way, and requiring `calendar.` would miss a params struct
// reached through an alias.
func isParamsType(expr ast.Expr) bool {
	switch t := expr.(type) {
	case *ast.SelectorExpr:
		return strings.HasSuffix(t.Sel.Name, "Params")
	case *ast.Ident:
		return strings.HasSuffix(t.Name, "Params")
	}
	return false
}

// callsAny reports whether fn calls one of the named functions, by any
// spelling — bare, or qualified through a package or receiver.
func callsAny(fn *ast.FuncDecl, names map[string]bool) bool {
	found := false
	ast.Inspect(fn.Body, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		switch f := call.Fun.(type) {
		case *ast.Ident:
			if names[f.Name] {
				found = true
			}
		case *ast.SelectorExpr:
			if names[f.Sel.Name] {
				found = true
			}
		}
		return !found
	})
	return found
}
