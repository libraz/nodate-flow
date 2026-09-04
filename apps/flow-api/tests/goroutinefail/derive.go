package goroutinefail

import (
	"bufio"
	"errors"
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

// Roots are the directory trees scanned, repository-relative. Both hold
// Go test code, and a goroutine calling FailNow is the same fault in
// either, so neither is checked without the other.
var Roots = []string{"apps", "packages"}

// skippedDirs are trees a Go parse has no business entering. The web
// applications live under apps/, so a walk that does not skip their
// dependency trees spends its time there.
var skippedDirs = map[string]bool{
	"node_modules": true,
	"testdata":     true,
	"vendor":       true,
}

// Finding is one `go` statement whose body can reach a FailNow.
type Finding struct {
	File  string
	Line  int
	Chain []string
}

// Location renders the finding's position for a failure message.
func (f Finding) Location() string { return fmt.Sprintf("%s:%d", f.File, f.Line) }

// ChainString renders the route from the goroutine to the FailNow, which
// is what says where the fix belongs: the hop that should return an
// error instead of asserting.
func (f Finding) ChainString() string { return strings.Join(f.Chain, " -> ") }

// RootCounts is what one root yielded. A root that yielded nothing has
// reached no verdict, so these are checked rather than only printed.
type RootCounts struct {
	Root         string
	TestFiles    int
	GoStatements int
	FailNowFuncs int
}

// String renders the counts for the run's output.
func (c RootCounts) String() string {
	return fmt.Sprintf("%s: %d test files, %d go statements, %d FailNow-reaching functions",
		c.Root, c.TestFiles, c.GoStatements, c.FailNowFuncs)
}

// Result is one scan: what was found, and what was looked at to find it.
type Result struct {
	Findings []Finding
	Counts   []RootCounts
}

// CheckNonEmpty reports the first root that yielded nothing. A scan that
// parsed no files, saw no goroutines, or derived no FailNow-reaching
// functions has not cleared the tree — it has failed to read it, and
// must never be reported as a pass.
func (r Result) CheckNonEmpty() error {
	if len(r.Counts) == 0 {
		return errors.New("goroutinefail: no root was scanned at all")
	}
	for _, c := range r.Counts {
		switch {
		case c.TestFiles == 0:
			return fmt.Errorf("goroutinefail: %s yielded no *_test.go file; the walk has stopped "+
				"matching rather than the tests having gone away", c.Root)
		case c.GoStatements == 0:
			return fmt.Errorf("goroutinefail: %s yielded no go statement; the check ranged over "+
				"nothing and cleared nothing", c.Root)
		case c.FailNowFuncs == 0:
			return fmt.Errorf("goroutinefail: %s yielded no function that reaches FailNow; a test "+
				"tree with no require, t.Fatal or t.FailNow anywhere means the derivation broke", c.Root)
		}
	}
	return nil
}

// RepoRoot returns the repository root, found by walking up from the
// caller's working directory to the go.work that defines the workspace.
func RepoRoot() (string, error) {
	dir, err := os.Getwd()
	if err != nil {
		return "", err
	}
	for {
		if _, statErr := os.Stat(filepath.Join(dir, "go.work")); statErr == nil {
			return dir, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", errors.New("goroutinefail: go.work not found above the working directory")
		}
		dir = parent
	}
}

// File is one parsed source file, kept with the repository-relative path
// a failure message has to name.
type File struct {
	Rel  string
	Test bool
	AST  *ast.File
}

// Package is every Go file in one directory. A directory is the unit
// because that is the scope in which an unqualified call resolves: a
// goroutine in a test file reaches a helper written beside it without
// naming a package, whether or not that helper lives in a test file.
type Package struct {
	Dir        string
	ImportPath string
	Files      []File
}

// Scan parses every Go file under the given roots and returns the
// goroutines that can reach a FailNow, together with what each root
// yielded.
func Scan(root string, roots []string) (Result, error) {
	var result Result
	fset := token.NewFileSet()
	var all []Package
	for _, rel := range roots {
		pkgs, err := parseRoot(fset, root, rel)
		if err != nil {
			return Result{}, err
		}
		counts := RootCounts{Root: rel}
		for _, pkg := range pkgs {
			for _, f := range pkg.Files {
				if f.Test {
					counts.TestFiles++
				}
			}
		}
		result.Counts = append(result.Counts, counts)
		all = append(all, pkgs...)
	}

	findings, reach := analyze(fset, all)
	result.Findings = findings

	// Attribute the derived totals back to the root each package came
	// from, so a root that contributed nothing is visible as such rather
	// than hidden behind another root's numbers.
	for i, c := range result.Counts {
		prefix := c.Root + string(filepath.Separator)
		for key := range reach {
			if strings.HasPrefix(key.dir, prefix) {
				result.Counts[i].FailNowFuncs++
			}
		}
		for _, pkg := range all {
			if !strings.HasPrefix(pkg.Dir, prefix) {
				continue
			}
			for _, f := range pkg.Files {
				if f.Test {
					result.Counts[i].GoStatements += countGoStatements(f.AST)
				}
			}
		}
	}
	return result, nil
}

// parseRoot parses every Go file under one root, grouped by directory.
func parseRoot(fset *token.FileSet, root, rel string) ([]Package, error) {
	dir := filepath.Join(root, rel)
	if _, err := os.Stat(dir); err != nil {
		return nil, nil
	}
	byDir := map[string]*Package{}
	err := filepath.WalkDir(dir, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			name := entry.Name()
			if path != dir && (skippedDirs[name] || strings.HasPrefix(name, ".")) {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(entry.Name(), ".go") {
			return nil
		}
		relPath, relErr := filepath.Rel(root, path)
		if relErr != nil {
			relPath = path
		}
		src, readErr := os.ReadFile(path) //#nosec G304,G122 -- repository path walked at test time
		if readErr != nil {
			return readErr
		}
		parsed, parseErr := parser.ParseFile(fset, filepath.ToSlash(relPath), src, 0)
		if parseErr != nil {
			return fmt.Errorf("parse %s: %w", relPath, parseErr)
		}
		dirRel := filepath.Dir(relPath)
		pkg := byDir[dirRel]
		if pkg == nil {
			pkg = &Package{Dir: dirRel}
			byDir[dirRel] = pkg
		}
		pkg.Files = append(pkg.Files, File{
			Rel:  filepath.ToSlash(relPath),
			Test: strings.HasSuffix(entry.Name(), "_test.go"),
			AST:  parsed,
		})
		return nil
	})
	if err != nil {
		return nil, err
	}

	out := make([]Package, 0, len(byDir))
	for _, pkg := range byDir {
		sort.Slice(pkg.Files, func(i, j int) bool { return pkg.Files[i].Rel < pkg.Files[j].Rel })
		pkg.ImportPath = importPath(root, pkg.Dir)
		out = append(out, *pkg)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Dir < out[j].Dir })
	return out, nil
}

// importPath resolves a directory to the path other packages import it
// by, so a goroutine calling a helper in another package of this
// workspace is followed rather than assumed harmless. A directory under
// no module resolves to nothing and is reachable only from beside it.
func importPath(root, dirRel string) string {
	dir := dirRel
	for {
		modPath := moduleName(filepath.Join(root, dir, "go.mod"))
		if modPath != "" {
			suffix, err := filepath.Rel(dir, dirRel)
			if err != nil || suffix == "." {
				return modPath
			}
			return modPath + "/" + filepath.ToSlash(suffix)
		}
		parent := filepath.Dir(dir)
		if parent == dir || parent == "." {
			return ""
		}
		dir = parent
	}
}

// moduleName reads the module path from a go.mod, or "" when the file is
// not there.
func moduleName(path string) string {
	f, err := os.Open(path) //#nosec G304,G122 -- repository path walked at test time
	if err != nil {
		return ""
	}
	defer func() { _ = f.Close() }()
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if rest, ok := strings.CutPrefix(line, "module "); ok {
			return strings.TrimSpace(rest)
		}
	}
	return ""
}

// funcKey identifies one function by the directory it is declared in and
// its name. Methods are keyed by name too: two types sharing a method
// name is rare, and merging them can only widen the set.
type funcKey struct {
	dir  string
	name string
}

// reachStep records why a function reaches FailNow: either it calls one
// of the primitives directly, or it calls something that does.
type reachStep struct {
	primitive string
	next      funcKey
	hasNext   bool
}

// analyze derives the FailNow-reaching functions across the given
// packages and returns the `go` statements that can reach them.
func analyze(fset *token.FileSet, pkgs []Package) ([]Finding, map[funcKey]reachStep) {
	byImportPath := map[string]string{}
	for _, pkg := range pkgs {
		if pkg.ImportPath != "" {
			byImportPath[pkg.ImportPath] = pkg.Dir
		}
	}

	// Collect every function declaration, with the calls its body makes
	// resolved to the directory they land in.
	type funcBody struct {
		key   funcKey
		prims []string
		calls []funcKey
	}
	var bodies []funcBody
	for _, pkg := range pkgs {
		for _, f := range pkg.Files {
			imports := importAliases(f.AST, byImportPath)
			for _, decl := range f.AST.Decls {
				fn, ok := decl.(*ast.FuncDecl)
				if !ok || fn.Body == nil {
					continue
				}
				prims, calls := scanBody(fn.Body, testingIdents(fn), pkg.Dir, imports)
				bodies = append(bodies, funcBody{
					key:   funcKey{dir: pkg.Dir, name: fn.Name.Name},
					prims: prims,
					calls: calls,
				})
			}
		}
	}

	// Fixpoint: a function reaches FailNow if it calls a primitive, or
	// calls something that reaches. Iterating until nothing changes is
	// the whole point — a one-level walk clears a goroutine whose helper
	// only requires two hops down.
	reach := map[funcKey]reachStep{}
	for _, b := range bodies {
		if len(b.prims) > 0 {
			reach[b.key] = reachStep{primitive: b.prims[0]}
		}
	}
	for changed := true; changed; {
		changed = false
		for _, b := range bodies {
			if _, done := reach[b.key]; done {
				continue
			}
			for _, callee := range b.calls {
				if _, ok := reach[callee]; ok {
					reach[b.key] = reachStep{next: callee, hasNext: true}
					changed = true
					break
				}
			}
		}
	}

	var findings []Finding
	for _, pkg := range pkgs {
		for _, f := range pkg.Files {
			if !f.Test {
				continue
			}
			imports := importAliases(f.AST, byImportPath)
			for _, decl := range f.AST.Decls {
				fn, ok := decl.(*ast.FuncDecl)
				if !ok || fn.Body == nil {
					continue
				}
				idents := testingIdents(fn)
				ast.Inspect(fn.Body, func(n ast.Node) bool {
					goStmt, ok := n.(*ast.GoStmt)
					if !ok {
						return true
					}
					if chain := goChain(goStmt, idents, pkg.Dir, imports, reach); chain != nil {
						findings = append(findings, Finding{
							File:  f.Rel,
							Line:  fset.Position(goStmt.Pos()).Line,
							Chain: chain,
						})
					}
					return true
				})
			}
		}
	}
	sort.Slice(findings, func(i, j int) bool {
		if findings[i].File != findings[j].File {
			return findings[i].File < findings[j].File
		}
		return findings[i].Line < findings[j].Line
	})
	return findings, reach
}

// goChain returns the route from a `go` statement to a FailNow, or nil
// when it cannot reach one.
func goChain(goStmt *ast.GoStmt, idents map[string]bool, dir string, imports map[string]string, reach map[funcKey]reachStep) []string {
	// `go someHelper(...)`: the callee is the whole goroutine.
	if lit, isLit := goStmt.Call.Fun.(*ast.FuncLit); isLit {
		prims, calls := scanBody(lit.Body, idents, dir, imports)
		if len(prims) > 0 {
			return []string{"go func", prims[0]}
		}
		for _, callee := range calls {
			if _, ok := reach[callee]; ok {
				return append([]string{"go func"}, chainFrom(callee, reach)...)
			}
		}
		return nil
	}
	callee, name, ok := resolveCall(goStmt.Call, dir, imports)
	if !ok {
		return nil
	}
	if _, reaches := reach[callee]; !reaches {
		return nil
	}
	return append([]string{"go " + name}, chainFrom(callee, reach)[1:]...)
}

// chainFrom follows the reach map from one function to the primitive it
// ends at.
func chainFrom(key funcKey, reach map[funcKey]reachStep) []string {
	chain := []string{key.name}
	seen := map[funcKey]bool{key: true}
	for {
		step, ok := reach[key]
		if !ok {
			return chain
		}
		if !step.hasNext {
			return append(chain, step.primitive)
		}
		key = step.next
		if seen[key] {
			// Mutual recursion between two reaching functions; the
			// chain already names the route worth reading.
			return chain
		}
		seen[key] = true
		chain = append(chain, key.name)
	}
}

// scanBody returns the FailNow primitives a body calls directly and the
// functions it calls that might reach one. Function literals inside the
// body count as part of it: a closure defined here and called here runs
// on this goroutine.
func scanBody(body *ast.BlockStmt, idents map[string]bool, dir string, imports map[string]string) (prims []string, calls []funcKey) {
	ast.Inspect(body, func(n ast.Node) bool {
		// A nested `go` statement's body runs on its own goroutine and
		// is judged as its own finding, not as part of this one.
		if _, ok := n.(*ast.GoStmt); ok {
			return false
		}
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		if prim, ok := failNowPrimitive(call, idents); ok {
			prims = append(prims, prim)
			return true
		}
		if key, _, ok := resolveCall(call, dir, imports); ok {
			calls = append(calls, key)
		}
		return true
	})
	return prims, calls
}

// failNowPrimitive reports whether a call ends the test on the spot:
// any require.* assertion, or Fatal / Fatalf / FailNow / Skip / Skipf /
// SkipNow on a testing handle. Skipping is here for the same reason
// failing is — SkipNow runs runtime.Goexit as well, so from a goroutine
// it abandons that goroutine rather than the test, and panics once the
// test has returned. assert.*, t.Errorf and t.Logf record an outcome
// without unwinding and are legal from any goroutine, so they are not
// primitives.
func failNowPrimitive(call *ast.CallExpr, idents map[string]bool) (string, bool) {
	sel, ok := call.Fun.(*ast.SelectorExpr)
	if !ok {
		return "", false
	}
	recv, ok := sel.X.(*ast.Ident)
	if !ok {
		return "", false
	}
	// require.New builds an assertion handle rather than asserting; the
	// call it is handed to is what fails, and that call is a method on
	// the handle rather than one of these.
	if recv.Name == "require" && sel.Sel.Name != "New" {
		return "require." + sel.Sel.Name, true
	}
	if idents[recv.Name] {
		switch sel.Sel.Name {
		case "Fatal", "Fatalf", "FailNow", "Skip", "Skipf", "SkipNow":
			return recv.Name + "." + sel.Sel.Name, true
		}
	}
	return "", false
}

// resolveCall maps a call to the function it names, within this
// directory or in another scanned package.
func resolveCall(call *ast.CallExpr, dir string, imports map[string]string) (funcKey, string, bool) {
	switch fun := call.Fun.(type) {
	case *ast.Ident:
		return funcKey{dir: dir, name: fun.Name}, fun.Name, true
	case *ast.SelectorExpr:
		pkgIdent, ok := fun.X.(*ast.Ident)
		if !ok {
			return funcKey{}, "", false
		}
		if otherDir, ok := imports[pkgIdent.Name]; ok {
			return funcKey{dir: otherDir, name: fun.Sel.Name},
				pkgIdent.Name + "." + fun.Sel.Name, true
		}
		// A method call on a value: the receiver's package is unknown
		// without type information, so it is matched by name within
		// this directory, where the receiver most often lives.
		return funcKey{dir: dir, name: fun.Sel.Name}, fun.Sel.Name, true
	}
	return funcKey{}, "", false
}

// importAliases maps the name a file refers to an imported package by
// onto the directory that package was parsed from. Imports outside the
// scanned roots have no entry and are left alone.
func importAliases(file *ast.File, byImportPath map[string]string) map[string]string {
	out := map[string]string{}
	for _, spec := range file.Imports {
		path := strings.Trim(spec.Path.Value, `"`)
		dir, ok := byImportPath[path]
		if !ok {
			continue
		}
		name := path[strings.LastIndex(path, "/")+1:]
		if spec.Name != nil {
			name = spec.Name.Name
		}
		if name == "_" || name == "." {
			continue
		}
		out[name] = dir
	}
	return out
}

// testingIdents returns the identifiers inside a declaration that hold a
// testing handle, so `t.Fatalf` is told apart from a Fatal on something
// else. Parameters anywhere in the declaration count, which covers the
// subtest and helper closures that take their own handle.
func testingIdents(fn *ast.FuncDecl) map[string]bool {
	out := map[string]bool{}
	collect := func(fields *ast.FieldList) {
		if fields == nil {
			return
		}
		for _, field := range fields.List {
			if !isTestingHandle(field.Type) {
				continue
			}
			for _, name := range field.Names {
				out[name.Name] = true
			}
		}
	}
	collect(fn.Type.Params)
	ast.Inspect(fn, func(n ast.Node) bool {
		if lit, ok := n.(*ast.FuncLit); ok {
			collect(lit.Type.Params)
		}
		return true
	})
	return out
}

// isTestingHandle reports whether a type expression is *testing.T,
// *testing.B, *testing.F or testing.TB.
func isTestingHandle(expr ast.Expr) bool {
	if star, ok := expr.(*ast.StarExpr); ok {
		expr = star.X
	}
	sel, ok := expr.(*ast.SelectorExpr)
	if !ok {
		return false
	}
	pkg, ok := sel.X.(*ast.Ident)
	if !ok || pkg.Name != "testing" {
		return false
	}
	switch sel.Sel.Name {
	case "T", "B", "F", "TB":
		return true
	}
	return false
}

// countGoStatements counts the `go` statements in one file, including
// the ones nested inside another goroutine's body.
func countGoStatements(file *ast.File) int {
	n := 0
	ast.Inspect(file, func(node ast.Node) bool {
		if _, ok := node.(*ast.GoStmt); ok {
			n++
		}
		return true
	})
	return n
}
