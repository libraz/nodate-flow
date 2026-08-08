package calendars

import (
	"go/ast"
	"go/parser"
	"go/token"
	"testing"
)

// TestSmartCreatePatternsAreCompiledOnce guards where the natural-language
// parser's regular expressions are built.
//
// Compiling a pattern is the expensive part of using one, and this file
// keeps fifteen of them. Thirteen live in the package-level var block and
// are compiled once for the life of the process; two were being rebuilt
// inside cleanTitle, so every smart-create request paid to compile them
// again to strip trailing particles and collapse whitespace.
//
// The rule is positional rather than a timing, because that is what the
// defect was: a MustCompile reachable from a function body runs per call
// no matter how fast the pattern is. Moving either one back inside a
// function fails here.
func TestSmartCreatePatternsAreCompiledOnce(t *testing.T) {
	t.Parallel()

	const file = "smart_create.go"
	fset := token.NewFileSet()
	parsed, err := parser.ParseFile(fset, file, nil, parser.SkipObjectResolution)
	if err != nil {
		t.Fatalf("parse %s: %v", file, err)
	}

	for _, decl := range parsed.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok || fn.Body == nil {
			continue
		}
		ast.Inspect(fn.Body, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}
			sel, ok := call.Fun.(*ast.SelectorExpr)
			if !ok {
				return true
			}
			pkg, ok := sel.X.(*ast.Ident)
			if !ok || pkg.Name != "regexp" {
				return true
			}
			if sel.Sel.Name != "MustCompile" && sel.Sel.Name != "Compile" {
				return true
			}
			t.Errorf("%s: regexp.%s inside %s recompiles the pattern on every request; "+
				"declare it in the package-level var block beside the other patterns",
				fset.Position(sel.Pos()), sel.Sel.Name, fn.Name.Name)
			return true
		})
	}
}
