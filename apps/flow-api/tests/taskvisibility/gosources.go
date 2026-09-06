package taskvisibility

// The other half of the statement universe: a projection built in Go and
// executed directly, rather than declared in sql/queries.
//
// The premise of this package is that it holds *every* statement putting a
// task's own content on the wire. A derivation reading only sql/queries
// holds every such statement that follows the convention SQL lives there —
// which is not the same set, and the difference is invisible from inside:
// a projection moved into Go leaves the scope silently, taking its finding
// with it. That is coverage inversely proportional to how closely the code
// follows the repository's own rule.
//
// A Go projection is read the same way a query file's is. The text is
// recovered from the source, the exposures are derived from the select
// list, and the same canonical unit is required — because the reader on
// the other end of the response cannot tell which file the SQL was written
// in.
//
// What cannot be recovered is stated rather than assumed. A statement
// assembled at run time — a select list or a predicate substituted in, a
// column concatenated on — reaches the source as fragments. The fragments
// that are literals are kept and the rest becomes [UnreadableToken], so a
// projection whose content columns are visible is held to the rule even
// when its predicate is not, and the failure says which half could not be
// read instead of claiming the rule is absent.

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

// UnreadableToken stands in for a fragment of a statement that is not in
// the source: an argument substituted through a format verb, a value
// concatenated in. It is a token no SQL produces, so a predicate
// containing one can never be mistaken for the rule.
const UnreadableToken = "<unreadable>" //#nosec G101 -- a placeholder written into a statement's text, not a credential

// formatVerb matches the format verbs whose argument is substituted as
// text. Only the string-ish ones are read: a %d cannot carry a predicate.
var formatVerb = regexp.MustCompile(`%[+#]?[svq]`)

// queryCall matches the method names that put a statement's rows on the
// wire. A statement handed to anything else returns no rows, so it
// discloses nothing.
var queryCall = regexp.MustCompile(`^Query([A-Za-z]*)$`)

// GoStatements reads every projection executed from hand-written Go under
// apps/flow-api/internal, in a stable order.
//
// Generated queriers are skipped: they perform the statements sql/queries
// already declares, so reading them would report the same projection
// twice, once under a name nobody wrote.
func GoStatements(root string) ([]Statement, error) {
	fragment, err := ReadVisibilityFilterFragment(root)
	if err != nil {
		return nil, err
	}
	spliced := NormalizeFragment(fragment)

	base := filepath.Join(root, "apps", "flow-api", "internal")
	var paths []string
	err = filepath.WalkDir(base, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			if entry.Name() == "generated" {
				return fs.SkipDir
			}
			return nil
		}
		name := entry.Name()
		if !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			return nil
		}
		paths = append(paths, path)
		return nil
	})
	if err != nil {
		return nil, err
	}
	sort.Strings(paths)

	fset := token.NewFileSet()
	var out []Statement
	for _, path := range paths {
		file, parseErr := parser.ParseFile(fset, path, nil, parser.ParseComments)
		if parseErr != nil {
			return nil, fmt.Errorf("taskvisibility: parse %s: %w", path, parseErr)
		}
		rel, relErr := filepath.Rel(root, path)
		if relErr != nil {
			rel = path
		}
		out = append(out, goProjections(fset, file, filepath.ToSlash(rel), spliced)...)
	}
	return out, nil
}

// goProjections returns the projections one parsed file executes.
func goProjections(fset *token.FileSet, file *ast.File, path, spliced string) []Statement {
	fileConstants := packageStrings(file)
	imports := fileImports(file)

	var out []Statement
	for _, decl := range file.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok || fn.Body == nil {
			continue
		}
		bindings := resolveBindings(fn.Body, fileConstants)
		header := functionComments(file, fn)
		var fragments []string
		if splicesVisibilityFilter(fn.Body, imports) {
			fragments = []string{spliced}
		}

		ast.Inspect(fn.Body, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}
			sel, ok := call.Fun.(*ast.SelectorExpr)
			if !ok || !queryCall.MatchString(sel.Sel.Name) {
				return true
			}
			for _, arg := range call.Args {
				text, ok := evaluate(arg, bindings)
				if !ok || !startsStatement(text) {
					continue
				}
				out = append(out, Statement{
					Name:   fn.Name.Name,
					Path:   path,
					Line:   fset.Position(call.Pos()).Line,
					Header: header,
					Body:   text,
					// A Go statement binds the actor as a placeholder,
					// the way the spliced fragment does: the sqlc
					// spelling of the argument is not available to a
					// query the generator never sees.
					Normalized: NormalizeFragment(text),
					Spliced:    fragments,
				})
				break
			}
			return true
		})
	}
	return out
}

// visibilityFilterPackages are the packages whose TaskVisibilityFilter
// returns the shared fragment: the one that declares it and the middleware
// pass-through the handlers reach it through.
//
// A call is recognised by the package it resolves to rather than by the
// method name alone, so a same-named method on some other value is never
// read as the rule arriving. A fourth wrapper added tomorrow is not
// recognised, and the statement is reported as unreadable — which is the
// direction this has to fail in.
var visibilityFilterPackages = map[string]bool{
	"github.com/libraz/nodate-flow/apps/flow-api/internal/acl":             true,
	"github.com/libraz/nodate-flow/apps/flow-api/internal/http/middleware": true,
}

// splicesVisibilityFilter reports whether a function obtains the shared
// visibility fragment, which is how a runtime-assembled predicate carries
// the rule.
func splicesVisibilityFilter(body *ast.BlockStmt, imports map[string]string) bool {
	found := false
	ast.Inspect(body, func(n ast.Node) bool {
		if found {
			return false
		}
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		sel, ok := call.Fun.(*ast.SelectorExpr)
		if !ok || sel.Sel.Name != "TaskVisibilityFilter" {
			return true
		}
		qualifier, ok := sel.X.(*ast.Ident)
		if !ok {
			return true
		}
		if visibilityFilterPackages[imports[qualifier.Name]] {
			found = true
			return false
		}
		return true
	})
	return found
}

// fileImports maps the name a file refers to each import by onto that
// import's path.
func fileImports(file *ast.File) map[string]string {
	out := map[string]string{}
	for _, imp := range file.Imports {
		path := strings.Trim(imp.Path.Value, `"`)
		local := path[strings.LastIndex(path, "/")+1:]
		if imp.Name != nil {
			local = imp.Name.Name
		}
		if local == "_" || local == "." {
			continue
		}
		out[local] = path
	}
	return out
}

// functionComments returns the comments a marker may be written in: the
// function's doc comment and anything inside its body.
//
// Both are read for the same reason the other gates read both: a reason
// belongs where the decision is, and where that is depends on whether the
// whole function or one statement inside it is what was reasoned about.
func functionComments(file *ast.File, fn *ast.FuncDecl) string {
	start := fn.Pos()
	if fn.Doc != nil {
		start = fn.Doc.Pos()
	}
	var parts []string
	for _, group := range file.Comments {
		for _, c := range group.List {
			if c.Pos() >= start && c.End() <= fn.End() {
				parts = append(parts, c.Text)
			}
		}
	}
	return strings.Join(parts, "\n")
}

// evaluate recovers the text an expression carries, substituting
// [UnreadableToken] for anything the source does not state.
//
// It reports false only when the expression carries no text at all. An
// expression that is partly readable is returned partly read: the point is
// to hold what can be seen to the rule, not to discard a projection
// because its predicate is assembled elsewhere.
func evaluate(expr ast.Expr, bindings map[string]string) (string, bool) {
	switch node := expr.(type) {
	case *ast.BasicLit:
		if node.Kind != token.STRING {
			return "", false
		}
		text, err := strconv.Unquote(node.Value)
		if err != nil {
			return "", false
		}
		return text, true
	case *ast.Ident:
		text, ok := bindings[node.Name]
		return text, ok
	case *ast.ParenExpr:
		return evaluate(node.X, bindings)
	case *ast.BinaryExpr:
		if node.Op != token.ADD {
			return "", false
		}
		left, leftOK := evaluate(node.X, bindings)
		right, rightOK := evaluate(node.Y, bindings)
		if !leftOK && !rightOK {
			return "", false
		}
		if !leftOK {
			left = UnreadableToken
		}
		if !rightOK {
			right = UnreadableToken
		}
		return left + right, true
	case *ast.CallExpr:
		return evaluateFormat(node, bindings)
	default:
		return "", false
	}
}

// evaluateFormat expands a fmt.Sprintf whose template is in the source,
// filling each string verb with the argument's own text where that is
// readable and with [UnreadableToken] where it is not.
func evaluateFormat(call *ast.CallExpr, bindings map[string]string) (string, bool) {
	sel, ok := call.Fun.(*ast.SelectorExpr)
	if !ok || sel.Sel.Name != "Sprintf" {
		return "", false
	}
	if qualifier, ok := sel.X.(*ast.Ident); !ok || qualifier.Name != "fmt" {
		return "", false
	}
	if len(call.Args) == 0 {
		return "", false
	}
	template, ok := evaluate(call.Args[0], bindings)
	if !ok {
		return "", false
	}

	args := call.Args[1:]
	next := 0
	out := formatVerb.ReplaceAllStringFunc(template, func(string) string {
		if next >= len(args) {
			return UnreadableToken
		}
		text, ok := evaluate(args[next], bindings)
		next++
		if !ok {
			return UnreadableToken
		}
		return text
	})
	return out, true
}

// resolveBindings maps every identifier a function binds to a string onto
// that string's text, repeating until nothing new resolves — which is what
// lets a statement assembled from a constant declared above it be read.
func resolveBindings(body *ast.BlockStmt, outer map[string]string) map[string]string {
	out := map[string]string{}
	for name, text := range outer {
		out[name] = text
	}

	type binding struct {
		name string
		expr ast.Expr
	}
	var pending []binding
	ambiguous := map[string]bool{}
	seen := map[string]bool{}
	add := func(name string, expr ast.Expr) {
		if name == "" || name == "_" {
			return
		}
		if seen[name] {
			ambiguous[name] = true
			return
		}
		seen[name] = true
		pending = append(pending, binding{name: name, expr: expr})
	}

	ast.Inspect(body, func(n ast.Node) bool {
		switch node := n.(type) {
		case *ast.ValueSpec:
			for i, ident := range node.Names {
				if i < len(node.Values) {
					add(ident.Name, node.Values[i])
				}
			}
		case *ast.AssignStmt:
			if len(node.Lhs) != len(node.Rhs) {
				return true
			}
			for i, lhs := range node.Lhs {
				if ident, ok := lhs.(*ast.Ident); ok {
					add(ident.Name, node.Rhs[i])
				}
			}
		}
		return true
	})

	// A name bound twice in one function is dropped rather than guessed
	// at: crediting one binding's SQL to the other's call site would report
	// a projection at a position that does not perform it.
	for changed := true; changed; {
		changed = false
		for _, b := range pending {
			if ambiguous[b.name] {
				continue
			}
			if _, done := out[b.name]; done {
				continue
			}
			if text, ok := evaluate(b.expr, out); ok {
				out[b.name] = text
				changed = true
			}
		}
	}
	for name := range ambiguous {
		delete(out, name)
	}
	return out
}

// packageStrings returns a file's top-level string bindings. Only the top
// level is read: a scan of the whole file would collect every function's
// locals into one namespace, where two functions each declaring `const q`
// would credit one function's SQL to the other's call site.
func packageStrings(file *ast.File) map[string]string {
	out := map[string]string{}
	ambiguous := map[string]bool{}
	for _, decl := range file.Decls {
		gen, ok := decl.(*ast.GenDecl)
		if !ok {
			continue
		}
		for _, spec := range gen.Specs {
			value, ok := spec.(*ast.ValueSpec)
			if !ok {
				continue
			}
			for i, ident := range value.Names {
				if i >= len(value.Values) || ident.Name == "_" {
					continue
				}
				text, resolved := evaluate(value.Values[i], out)
				if !resolved {
					continue
				}
				if _, seen := out[ident.Name]; seen {
					ambiguous[ident.Name] = true
					continue
				}
				out[ident.Name] = text
			}
		}
	}
	for name := range ambiguous {
		delete(out, name)
	}
	return out
}
