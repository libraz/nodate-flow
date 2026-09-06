package duplicaterefusal

// The Go half: where a duplicate-entry error is translated into a named
// refusal, and which write each translation speaks for.
//
// Attribution is the whole difficulty. The branch tests an error value; the
// write that produced it is an assignment somewhere above, and the two are
// connected only by that variable. So the binding is resolved the way a
// reader resolves it — the last place the variable was given a value before
// the branch reads it — and the value is then followed to the generated
// query method it came from, whose statement names the table.
//
// One indirection is followed beyond that: a binding whose value comes from
// a helper handed a function is resolved through the function values bound
// in the same body. The event log is written that way, with the INSERT
// closed over and the retry policy applied by the caller, and a reader who
// stopped at the helper would call the log unattributable while the
// statement is two lines up.
//
// Nothing else is guessed. A site whose write does not resolve — no
// binding, a value from something that is not a statement, or statements on
// more than one table — is reported by name. It is not skipped, because a
// site the derivation cannot see is indistinguishable from a site with
// nothing wrong, and that is the state this check exists to end.

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// appRoots are the trees searched for branches, relative to the repository
// root. Both services translate the same driver error into their own codes,
// and a check that read one of them would call the other clean.
var appRoots = [][]string{
	{"apps", "flow-api", "internal"},
	{"apps", "auth-api", "internal"},
}

// predicateNames are the spellings of the duplicate-entry test. The
// exported helper is shared, and several packages bind a package-local
// alias to it so their call sites read in their own vocabulary; both reach
// the same driver check, so both are branches.
var predicateNames = map[string]bool{
	"IsDuplicateEntry": true,
	"isDuplicateEntry": true,
}

// Branch is one place a duplicate-entry error becomes a named refusal.
type Branch struct {
	// App is the service the branch belongs to, so a failure says which
	// tree to open and the derivation can prove it read both.
	App string
	// File is the repository-relative file and Line the line of the test.
	File string
	Line int
	// Func is the function the branch sits in, qualified by its package
	// directory, which is what an exception names.
	Func string
	// ErrVar is the identifier the predicate was handed, or the expression
	// as written when it was not an identifier.
	ErrVar string
}

// Location renders the branch's position for a failure message.
func (b Branch) Location() string {
	return fmt.Sprintf("%s:%d", b.File, b.Line)
}

// Attribution is a branch together with the write it guards.
type Attribution struct {
	Branch Branch
	// Query is the sqlc statement whose error the branch reads.
	Query Statement
	// Table is the table that statement writes.
	Table string
	// Binding is where the error value was produced, so a reader can check
	// the derivation's answer against the source in one jump.
	Binding string
	// Indirect records that the statement was reached through a function
	// value rather than from the binding's own call.
	Indirect bool
}

// Unresolved is a branch whose write the derivation could not name,
// together with what stopped it.
type Unresolved struct {
	Branch Branch
	// Why states what the derivation could not do, in the terms of the
	// source rather than of the walk.
	Why string
}

// Source is the parsed service trees.
type Source struct {
	fset  *token.FileSet
	root  string
	files []*goFile
}

type goFile struct {
	app  string
	rel  string
	file *ast.File
}

// Parse reads every hand-written Go file under the service trees.
//
// Generated queriers are skipped: they declare the statement methods rather
// than call them, so a branch could never sit in one, and indexing them
// would only add names that match every call site. Test files are skipped
// because a test that hands a fabricated driver error to the predicate is
// exercising the predicate, not translating a write.
func Parse(root string) (*Source, error) {
	src := &Source{fset: token.NewFileSet(), root: root}
	for _, parts := range appRoots {
		app := parts[1]
		base := filepath.Join(append([]string{root}, parts...)...)
		if _, err := os.Stat(base); err != nil {
			continue
		}
		var paths []string
		err := filepath.WalkDir(base, func(path string, entry fs.DirEntry, walkErr error) error {
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
		for _, path := range paths {
			parsed, perr := parser.ParseFile(src.fset, path, nil, 0)
			if perr != nil {
				return nil, fmt.Errorf("parse %s: %w", path, perr)
			}
			rel, relErr := filepath.Rel(root, path)
			if relErr != nil {
				rel = path
			}
			src.files = append(src.files, &goFile{app: app, rel: filepath.ToSlash(rel), file: parsed})
		}
	}
	return src, nil
}

// Attribute resolves every duplicate-entry branch to the write it guards,
// returning the ones it could place and the ones it could not.
//
// Both halves are returned because only the pair is an answer. A list of
// placed branches on its own says nothing about how much of the source it
// covers, and a derivation that quietly stops matching returns a shorter
// list rather than an error.
func Attribute(src *Source, statements map[string]Statement) ([]Attribution, []Unresolved) {
	var placed []Attribution
	var missed []Unresolved

	for _, f := range src.files {
		for _, decl := range f.file.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok || fn.Body == nil {
				continue
			}
			scope := newFuncScope(src, f, fn)
			for _, call := range scope.predicateCalls() {
				branch := scope.branchAt(call)
				attribution, why := scope.resolve(branch, call, statements)
				if why != "" {
					missed = append(missed, Unresolved{Branch: branch, Why: why})
					continue
				}
				placed = append(placed, attribution)
			}
		}
	}
	sort.Slice(placed, func(i, j int) bool { return placed[i].Branch.less(placed[j].Branch) })
	sort.Slice(missed, func(i, j int) bool { return missed[i].Branch.less(missed[j].Branch) })
	return placed, missed
}

func (b Branch) less(other Branch) bool {
	if b.File != other.File {
		return b.File < other.File
	}
	return b.Line < other.Line
}

// funcScope is one function body indexed for the two questions attribution
// asks of it: where the predicate is called, and where a variable was last
// given a value.
type funcScope struct {
	src  *Source
	file *goFile
	fn   *ast.FuncDecl
	// bindings maps a variable name onto every place the body gives it a
	// value, in source order. Nested function literals are included: a
	// binding inside one still precedes a branch written below it, and
	// dropping them would let attribution reach past a value that is
	// visibly nearer.
	bindings map[string][]binding
}

// binding is one place a variable is given a value. Value is nil for a
// declaration that supplies none, which still counts: reaching past it to
// an earlier assignment would attribute the branch to a value the
// declaration replaced.
type binding struct {
	pos   token.Pos
	value ast.Expr
}

func newFuncScope(src *Source, file *goFile, fn *ast.FuncDecl) *funcScope {
	scope := &funcScope{src: src, file: file, fn: fn, bindings: map[string][]binding{}}
	ast.Inspect(fn.Body, func(n ast.Node) bool {
		switch node := n.(type) {
		case *ast.AssignStmt:
			for i, lhs := range node.Lhs {
				ident, ok := lhs.(*ast.Ident)
				if !ok || ident.Name == "_" {
					continue
				}
				var value ast.Expr
				switch {
				case len(node.Rhs) == len(node.Lhs):
					value = node.Rhs[i]
				case len(node.Rhs) == 1:
					// One call filling several names. Every name is bound by
					// the same call, so the call is the value for each.
					value = node.Rhs[0]
				}
				scope.bindings[ident.Name] = append(scope.bindings[ident.Name], binding{pos: ident.Pos(), value: value})
			}
		case *ast.ValueSpec:
			for i, ident := range node.Names {
				if ident.Name == "_" {
					continue
				}
				var value ast.Expr
				if i < len(node.Values) {
					value = node.Values[i]
				} else if len(node.Values) == 1 {
					value = node.Values[0]
				}
				scope.bindings[ident.Name] = append(scope.bindings[ident.Name], binding{pos: ident.Pos(), value: value})
			}
		}
		return true
	})
	for name := range scope.bindings {
		list := scope.bindings[name]
		sort.Slice(list, func(i, j int) bool { return list[i].pos < list[j].pos })
		scope.bindings[name] = list
	}
	return scope
}

// predicateCalls returns every call to the duplicate-entry test in the
// body, in source order.
func (s *funcScope) predicateCalls() []*ast.CallExpr {
	var out []*ast.CallExpr
	ast.Inspect(s.fn.Body, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		if predicateNames[calleeName(call.Fun)] {
			out = append(out, call)
		}
		return true
	})
	sort.Slice(out, func(i, j int) bool { return out[i].Pos() < out[j].Pos() })
	return out
}

// branchAt describes one call site.
func (s *funcScope) branchAt(call *ast.CallExpr) Branch {
	at := s.src.fset.Position(call.Pos())
	name := ""
	if len(call.Args) == 1 {
		if ident, ok := call.Args[0].(*ast.Ident); ok {
			name = ident.Name
		} else {
			name = exprText(call.Args[0])
		}
	}
	return Branch{
		App:    s.file.app,
		File:   s.file.rel,
		Line:   at.Line,
		Func:   packageDir(s.file.rel) + "." + s.fn.Name.Name,
		ErrVar: name,
	}
}

// resolve names the write a branch guards, or says what stopped it.
func (s *funcScope) resolve(branch Branch, call *ast.CallExpr, statements map[string]Statement) (Attribution, string) {
	if len(call.Args) != 1 {
		return Attribution{}, fmt.Sprintf("the predicate is called with %d arguments, so there is no error value to trace", len(call.Args))
	}
	ident, ok := call.Args[0].(*ast.Ident)
	if !ok {
		return Attribution{}, fmt.Sprintf("the predicate is handed %s rather than a variable, so no assignment names the write behind it", branch.ErrVar)
	}

	bound, ok := s.lastBindingBefore(ident.Name, call.Pos())
	if !ok {
		return Attribution{}, fmt.Sprintf("nothing in %s assigns %s before the branch reads it; the value reaches the branch from outside the function", s.fn.Name.Name, ident.Name)
	}
	if bound.value == nil {
		return Attribution{}, fmt.Sprintf("%s is declared without a value at %s and the branch reads it before anything assigns one", ident.Name, s.position(bound.pos))
	}

	indirect := false
	names := statementNamesIn(bound.value, statements)
	if len(names) == 0 {
		names = s.statementNamesBehindFunctionValues(bound.value, statements)
		indirect = len(names) > 0
	}
	if len(names) == 0 {
		return Attribution{}, fmt.Sprintf("%s is bound at %s to %s, which reaches no statement declared in sql/queries",
			ident.Name, s.position(bound.pos), exprText(bound.value))
	}

	byTable := map[string]Statement{}
	var tables, reads []string
	for _, name := range names {
		statement := statements[name]
		table, isWrite := statement.WriteTarget()
		if !isWrite {
			reads = append(reads, name)
			continue
		}
		if _, seen := byTable[table]; !seen {
			tables = append(tables, table)
		}
		byTable[table] = statement
	}
	sort.Strings(tables)

	switch len(tables) {
	case 0:
		return Attribution{}, fmt.Sprintf("%s is bound at %s to %s, which performs only %s — a read raises no duplicate key, so the branch guards no write",
			ident.Name, s.position(bound.pos), exprText(bound.value), strings.Join(reads, " / "))
	case 1:
		return Attribution{
			Branch:   branch,
			Query:    byTable[tables[0]],
			Table:    tables[0],
			Binding:  s.position(bound.pos),
			Indirect: indirect,
		}, ""
	default:
		return Attribution{}, fmt.Sprintf("%s is bound at %s to %s, which writes %s; the branch names one refusal and the derivation cannot say which write it is about",
			ident.Name, s.position(bound.pos), exprText(bound.value), strings.Join(tables, " and "))
	}
}

// lastBindingBefore returns the value a variable holds where the branch
// reads it: the nearest assignment above, which is the one a reader
// follows.
func (s *funcScope) lastBindingBefore(name string, pos token.Pos) (binding, bool) {
	list := s.bindings[name]
	var found binding
	ok := false
	for _, b := range list {
		if b.pos >= pos {
			break
		}
		found, ok = b, true
	}
	return found, ok
}

// statementNamesIn returns the sqlc statements an expression performs,
// matched by the generated method name.
//
// Only a method call matches. A generated querier is a value, so every
// statement is issued as a selector on one; a package-level function that
// happens to share a statement's name is not that statement, and crediting
// it would attribute a branch to a write that never ran.
func statementNamesIn(expr ast.Node, statements map[string]Statement) []string {
	seen := map[string]bool{}
	var out []string
	ast.Inspect(expr, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		sel, ok := call.Fun.(*ast.SelectorExpr)
		if !ok {
			return true
		}
		name := sel.Sel.Name
		if _, known := statements[name]; !known || seen[name] {
			return true
		}
		seen[name] = true
		out = append(out, name)
		return true
	})
	sort.Strings(out)
	return out
}

// statementNamesBehindFunctionValues follows the function values an
// expression hands to a helper.
//
// This is the one indirection worth following, because it is the shape the
// retry helpers are built in: the statement is closed over and the helper
// decides whether to re-issue it. The closure is bound in the same body, so
// the statement is still in front of the reader — it is the call that is
// one level away, not the write.
func (s *funcScope) statementNamesBehindFunctionValues(expr ast.Node, statements map[string]Statement) []string {
	seen := map[string]bool{}
	var out []string
	ast.Inspect(expr, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		for _, arg := range call.Args {
			ident, isIdent := arg.(*ast.Ident)
			if !isIdent {
				continue
			}
			for _, b := range s.bindings[ident.Name] {
				lit, isFunc := b.value.(*ast.FuncLit)
				if !isFunc {
					continue
				}
				for _, name := range statementNamesIn(lit.Body, statements) {
					if !seen[name] {
						seen[name] = true
						out = append(out, name)
					}
				}
			}
		}
		return true
	})
	sort.Strings(out)
	return out
}

// position renders a position as a repository-relative file and line, so
// what a failure reports and what an exception names are the same string on
// every machine.
func (s *funcScope) position(pos token.Pos) string {
	at := s.src.fset.Position(pos)
	rel := at.Filename
	if s.src.root != "" {
		if r, err := filepath.Rel(s.src.root, at.Filename); err == nil {
			rel = r
		}
	}
	return fmt.Sprintf("%s:%d", filepath.ToSlash(rel), at.Line)
}

// calleeName returns the name a call is spelled with, whether it is called
// directly or through a package qualifier.
func calleeName(fun ast.Expr) string {
	switch e := fun.(type) {
	case *ast.Ident:
		return e.Name
	case *ast.SelectorExpr:
		return e.Sel.Name
	default:
		return ""
	}
}

// exprText renders an expression the way a failure quotes it: the call
// chain without its arguments, which is what identifies the write.
func exprText(expr ast.Expr) string {
	switch e := expr.(type) {
	case *ast.CallExpr:
		return exprText(e.Fun) + "(...)"
	case *ast.SelectorExpr:
		return exprText(e.X) + "." + e.Sel.Name
	case *ast.Ident:
		return e.Name
	case *ast.IndexExpr:
		return exprText(e.X) + "[...]"
	case *ast.FuncLit:
		return "a function literal"
	case *ast.UnaryExpr:
		return e.Op.String() + exprText(e.X)
	case *ast.BasicLit:
		return e.Value
	default:
		return "an expression"
	}
}

// packageDir returns the package directory of a repository-relative file,
// which is how a function is named without depending on the module path.
func packageDir(rel string) string {
	if at := strings.LastIndex(rel, "/"); at >= 0 {
		return rel[:at]
	}
	return rel
}
