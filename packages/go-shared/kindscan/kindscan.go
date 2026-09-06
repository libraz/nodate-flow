// Package kindscan finds string literals written where an event kind is
// expected.
//
// eventbus.Kind is a defined type, so a string *variable* cannot be used
// as an event kind — the compiler rejects it. An untyped constant can:
// Go converts a literal to the defined type implicitly, so
// `Event{Type: "calendar.subscribed"}` compiles exactly as if the
// constant existed. That is how a kind nobody subscribes to gets
// written, and it is the half of the rule the type system cannot state.
//
// This package closes it from the outside. It is weaker than a compiler
// error and it always will be: it runs as a test, so it reports rather
// than prevents, and a package it is never pointed at is not covered.
// It is here because the alternative is nothing.
//
// The second rule covers where the kind stops being one. An event is
// stored in a text column, so the params struct that carries it into the
// row holds a plain string, and at that field the type system has
// nothing left to say — a literal, a local constant and a name built at
// run time are all equally acceptable to it. The fields listed in
// [KindField] must therefore be written from a value that was a Kind, so
// the name goes through the registry on its way to the column.
//
// The scan resolves types rather than matching source text. Asking the
// type checker what a literal will become answers precisely — it sees
// through a helper's parameter list, and it does not mistake an
// unrelated dotted string for a kind. A text scan would need a list of
// every function that takes a Kind, and the entry missing from that list
// is exactly where the next stray literal goes.
//
// The third rule leaves Go entirely. A query can spell a kind itself —
// INSERT INTO events (..., type, ...) VALUES (..., 'agent.task.detached',
// ...) — and nothing in Go refers to that string, so renaming the
// constant leaves it stale with every build still green. [ScanSQL] holds
// those literals to the same registry.
package kindscan

import (
	"fmt"
	"go/ast"
	"go/build"
	"go/constant"
	"go/parser"
	"go/token"
	"go/types"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"

	"github.com/libraz/nodate-flow/packages/go-shared/payloadscan"
)

// KindTypeName is the fully qualified name of the event-kind type, as
// the type checker spells it.
const KindTypeName = "github.com/libraz/nodate-flow/packages/go-shared/eventbus.Kind"

// Finding is one place a kind is spelled out where a constant belongs:
// a string literal the type checker resolved to an event kind, an
// [Undeclared] call handed a kind that is in fact declared, or a
// [KindField] written from something that was never a kind.
type Finding struct {
	// Pos is the "file:line:col" of the literal.
	Pos string
	// Value is the literal as written, unquoted. For a [Field] finding it
	// is the offending expression as written instead — the value there is
	// not always a literal, and naming it as the source spells it is what
	// makes the report point at something the reader can find.
	Value string
	// ViaUndeclared marks the second case. The literal is typed string
	// there — it is an argument, not a kind — so nothing about the value
	// itself is wrong; what is wrong is claiming the kind does not exist
	// when it does.
	ViaUndeclared bool
	// Field marks the third case and names the field written to, e.g.
	// "AppendEventParams.Type". Empty for the other two.
	Field string
}

func (f Finding) String() string {
	if f.Field != "" {
		return fmt.Sprintf("%s: %s is set from %s, which was never an event kind; "+
			"write string(<constant from packages/go-shared/eventbus>) so the kind is declared and the name is traceable to the column", f.Pos, f.Field, f.Value)
	}
	if f.ViaUndeclared {
		return fmt.Sprintf("%s: kindscan.Undeclared(%q) names a declared event kind; "+
			"the escape is for kinds that deliberately do not exist — use the constant from packages/go-shared/eventbus", f.Pos, f.Value)
	}
	return fmt.Sprintf("%s: %q is written as a literal where an event kind is expected; "+
		"use the constant from packages/go-shared/eventbus, and declare one there first if the kind is new", f.Pos, f.Value)
}

// Config selects what a scan looks at.
type Config struct {
	// Dir is the package directory to scan.
	Dir string
	// ImportPath is the import path of the package in Dir.
	//
	// The external test package beside it imports it by that path, and
	// what it must resolve to is the package as the test build assembles
	// it — the source files plus the in-package test files, which is where
	// a test helper shared with the external package is declared. Export
	// data holds no such helper, so without the path the external package
	// does not type-check at all.
	//
	// Empty falls back to the package's own name, which is enough for a
	// directory whose external test package needs nothing from the
	// in-package tests.
	ImportPath string
	// Root is the directory AllowFiles are written relative to. Empty
	// means they are matched against the path as the scan spells it.
	// [ScanModule] sets it to the root it walks.
	Root string
	// AllowFiles names the files exempt from the rule, each as a slash
	// path relative to Root. The file that declares the constants is the
	// only legitimate place a kind is written as a literal.
	//
	// The path is what is matched, not the base name. An exemption has to
	// name one file: the declaring file is called kinds.go, and so is any
	// other file somebody names that, which would inherit the exemption
	// without anyone choosing it — and a second kinds.go is the likeliest
	// place for a stray literal to sit unreported.
	AllowFiles []string
	// Cache holds the export-data locations resolved so far, shared by
	// every package of one module scan. Nil means this scan resolves
	// everything itself.
	Cache *payloadscan.ExportCache
	// Fields are the struct fields held to the origin rule. Nil means the
	// set every module is held to; a scan naming its own is how this
	// package's tests point the rule at a fixture, since testdata cannot
	// import the generated structs the real set names.
	Fields []KindField
}

// Scan type-checks the package in cfg.Dir and reports every string
// literal whose resolved type is the event-kind type.
//
// Test files are included: a literal in a test is how a kind that no
// production code can name gets asserted on, and a test asserting a
// spelling nothing emits is worse than no test at all. That covers both
// packages a directory can hold — the in-package tests and the external
// `package foo_test` beside them. The external one is type-checked on
// its own because its package name differs, and skipping it for that
// reason would exempt the files whose whole job is to pin a spelling.
//
// It also reports the one sanctioned way around the rule being used
// wrongly. An [Undeclared] argument is typed string, so the literal rule
// cannot see it — which is what lets a test name a kind that does not
// exist, and would equally let one name a kind that does. The second
// pass here rules that out, so the escape widens to exactly the values
// no constant covers.
func Scan(cfg Config) ([]Finding, error) {
	allow := map[string]bool{}
	for _, name := range cfg.AllowFiles {
		allow[filepath.ToSlash(filepath.Clean(name))] = true
	}

	bpkg, err := build.ImportDir(cfg.Dir, 0)
	if err != nil {
		// A directory with no buildable Go files is not an error: the
		// caller walks a tree and cannot know in advance.
		if _, ok := err.(*build.NoGoError); ok {
			return nil, nil
		}
		return nil, fmt.Errorf("kindscan: read %s: %w", cfg.Dir, err)
	}

	// A scan given no cache still memoises within itself; see the same
	// fallback in payloadscan.Scan.
	cache := cfg.Cache
	if cache == nil {
		cache = payloadscan.NewExportCache()
	}

	fields := cfg.Fields
	if fields == nil {
		fields = kindFields
	}

	// One file set across both units so a position printed by either names
	// the same file the same way.
	fset := token.NewFileSet()
	// One importer too, and for a harder reason: an importer memoises the
	// packages it has built, so a second one resolves a shared dependency
	// to a second *types.Package. The package under test then carries
	// database/sql from the first while the external test package sees the
	// second, and the checker rejects the pair as unrelated types.
	base := cache.Importer(fset)

	var (
		findings []Finding
		// self is the package under test once it has been checked, handed
		// to the external test package as its own import.
		self *types.Package
	)
	for _, u := range units(bpkg, cfg.ImportPath) {
		checked, found, uerr := scanUnit(cfg, u, fset, base, self, fields, allow)
		if uerr != nil {
			return nil, uerr
		}
		if !u.external {
			self = checked
		}
		findings = append(findings, found...)
	}
	sort.Slice(findings, func(i, j int) bool { return findings[i].Pos < findings[j].Pos })
	return findings, nil
}

// unit is one package the type checker sees in a directory. A directory
// holds up to two: the package together with its in-package tests, and
// the external test package, which declares `package foo_test` and so
// has to be checked in a pass of its own.
type unit struct {
	// path is the import path the checked package is created under. It is
	// what the external test package's import of the package under test
	// is matched against, so the two units must not share it.
	path string
	// files are the file names within the scanned directory.
	files []string
	// external marks the `package foo_test` unit.
	external bool
}

// units splits a directory's Go files into the packages the checker can
// be handed. importPath may be empty; see [Config.ImportPath].
func units(bpkg *build.Package, importPath string) []unit {
	if importPath == "" {
		importPath = bpkg.Name
	}
	own := make([]string, 0, len(bpkg.GoFiles)+len(bpkg.TestGoFiles))
	own = append(own, bpkg.GoFiles...)
	own = append(own, bpkg.TestGoFiles...)

	out := make([]unit, 0, 2)
	if len(own) > 0 {
		out = append(out, unit{path: importPath, files: own})
	}
	if len(bpkg.XTestGoFiles) > 0 {
		out = append(out, unit{path: importPath + "_test", files: bpkg.XTestGoFiles, external: true})
	}
	return out
}

// selfImporter resolves the package under test to the copy already
// checked in this scan, and everything else through the export data.
type selfImporter struct {
	base types.Importer
	self *types.Package
}

func (i selfImporter) Import(path string) (*types.Package, error) {
	if i.self != nil && path == i.self.Path() {
		return i.self, nil
	}
	return i.base.Import(path)
}

// scanUnit type-checks one package and reports what its files write. It
// returns the checked package so the external test unit can import it.
func scanUnit(
	cfg Config,
	u unit,
	fset *token.FileSet,
	base types.Importer,
	self *types.Package,
	fields []KindField,
	allow map[string]bool,
) (*types.Package, []Finding, error) {
	files := make([]*ast.File, 0, len(u.files))
	for _, name := range u.files {
		f, perr := parser.ParseFile(fset, filepath.Join(cfg.Dir, name), nil, 0)
		if perr != nil {
			return nil, nil, fmt.Errorf("kindscan: parse %s: %w", name, perr)
		}
		files = append(files, f)
	}

	info := &types.Info{
		Types: make(map[ast.Expr]types.TypeAndValue),
		// Uses resolves a call's callee to the function it names, which is
		// how the Undeclared pass recognises the escape by identity rather
		// than by the text "Undeclared" appearing in the source.
		Uses: make(map[*ast.Ident]types.Object),
		// Selections resolves `x.Field = v` to the struct the field belongs
		// to, which is what lets the origin rule cover a params struct
		// filled in after it is declared.
		Selections: make(map[*ast.SelectorExpr]*types.Selection),
	}
	conf := types.Config{
		Importer: selfImporter{base: base, self: self},
		// Errors are collected by Check and returned below. A dependency
		// that will not resolve must not quietly downgrade the scan to
		// "found nothing", which is what a passing guard looks like.
		Error: func(error) {},
	}
	pkg, err := conf.Check(u.path, fset, files, info)
	if err != nil {
		return nil, nil, fmt.Errorf("kindscan: type-check %s (%s): %w", cfg.Dir, u.path, err)
	}

	exempt := func(filename string) bool {
		return len(allow) > 0 && allow[relSlash(cfg.Root, filename)]
	}

	var findings []Finding
	report := func(pos token.Position, value string, viaUndeclared bool) {
		if exempt(pos.Filename) {
			return
		}
		findings = append(findings, Finding{Pos: pos.String(), Value: value, ViaUndeclared: viaUndeclared})
	}
	reportFieldWrites := func(writes []fieldWrite) {
		for _, w := range writes {
			pos := fset.Position(w.expr.Pos())
			if exempt(pos.Filename) {
				continue
			}
			findings = append(findings, Finding{
				Pos:   pos.String(),
				Value: types.ExprString(w.expr),
				Field: w.field.String(),
			})
		}
	}
	for _, f := range files {
		ast.Inspect(f, func(n ast.Node) bool {
			if call, ok := n.(*ast.CallExpr); ok {
				if value, pos, bad := badUndeclaredCall(fset, info, call); bad {
					report(pos, value, true)
				}
				return true
			}
			if composite, ok := n.(*ast.CompositeLit); ok {
				reportFieldWrites(compositeFieldWrites(info, fields, composite))
				return true
			}
			if assign, ok := n.(*ast.AssignStmt); ok {
				reportFieldWrites(assignFieldWrites(info, fields, assign))
				return true
			}
			lit, ok := n.(*ast.BasicLit)
			if !ok || lit.Kind != token.STRING {
				return true
			}
			tv, ok := info.Types[lit]
			if !ok || tv.Type == nil {
				return true
			}
			// Unalias first. A module that re-exports the kind type —
			// `type Kind = eventbus.Kind`, which is exactly what a package
			// does to spare its call sites a second import — makes every
			// literal typed through that spelling a types.Alias rather than
			// a types.Named. Asserting straight to *types.Named skipped
			// them, so the guard was blind to the one spelling the
			// re-export exists to encourage.
			named, ok := types.Unalias(tv.Type).(*types.Named)
			if !ok || named.String() != KindTypeName {
				return true
			}
			if lit.Value == `""` {
				// The zero Kind, not a spelling of one. It is how a helper
				// says "this change has no event", and reporting it would
				// push callers into naming a kind they do not have.
				return true
			}
			value, uerr := strconv.Unquote(lit.Value)
			if uerr != nil {
				value = lit.Value
			}
			report(fset.Position(lit.Pos()), value, false)
			return true
		})
	}
	return pkg, findings, nil
}

// relSlash spells filename the way an allowlist entry is written: as a
// slash path relative to root. A filename outside root, or a scan with no
// root, is compared as it stands.
func relSlash(root, filename string) string {
	if root != "" {
		if rel, err := filepath.Rel(root, filename); err == nil && !strings.HasPrefix(rel, "..") {
			return filepath.ToSlash(rel)
		}
	}
	return filepath.ToSlash(filepath.Clean(filename))
}

// badUndeclaredCall reports whether call is [Undeclared] applied to a
// constant that names a declared kind, and where.
//
// Only constant arguments are decided here. A value assembled at run
// time is left to Undeclared's own check: the scanner would have to
// evaluate the program to say anything about it, and answering "looks
// fine" would be worse than not answering.
func badUndeclaredCall(fset *token.FileSet, info *types.Info, call *ast.CallExpr) (string, token.Position, bool) {
	if len(call.Args) != 1 {
		return "", token.Position{}, false
	}
	var id *ast.Ident
	switch fun := ast.Unparen(call.Fun).(type) {
	case *ast.Ident:
		id = fun
	case *ast.SelectorExpr:
		id = fun.Sel
	default:
		return "", token.Position{}, false
	}
	fn, ok := info.Uses[id].(*types.Func)
	if !ok || fn.FullName() != UndeclaredFuncName {
		return "", token.Position{}, false
	}
	tv, ok := info.Types[call.Args[0]]
	if !ok || tv.Value == nil || tv.Value.Kind() != constant.String {
		return "", token.Position{}, false
	}
	value := constant.StringVal(tv.Value)
	if !IsDeclaredKind(value) {
		return "", token.Position{}, false
	}
	return value, fset.Position(call.Pos()), true
}

// ScanModule type-checks every eventbus-referencing package under root
// and returns one message per literal found, sorted.
//
// Each allowFiles entry is a slash path relative to root and must name a
// file that exists. An exemption pointing at a moved or renamed file
// exempts nothing, which is invisible — the guard keeps passing, and the
// file it was meant to cover is now reported or, worse, some other file
// by the same name is not.
//
// The module guards call this rather than each assembling its own walk,
// so a rule tightened in one module cannot be looser in the next.
//
// Packages are walked concurrently because the checker spends most of
// its time waiting on the build cache; serially this is slow enough that
// the guard becomes something people skip.
func ScanModule(root string, allowFiles ...string) ([]string, error) {
	for _, name := range allowFiles {
		if _, err := os.Stat(filepath.Join(root, name)); err != nil {
			return nil, fmt.Errorf("kindscan: allowlisted %s names no file under %s: %w", name, root, err)
		}
	}
	dirs, err := Packages(root)
	if err != nil {
		return nil, err
	}
	if len(dirs) == 0 {
		return nil, fmt.Errorf("kindscan: no package under %s references the eventbus; a scan here would prove nothing", root)
	}
	modPath, err := modulePath(root)
	if err != nil {
		return nil, err
	}
	// One cache for the whole module. Every package here imports much of
	// what its neighbours import, and each resolved import costs a
	// subprocess, so a cache per package makes the scan cost the packages
	// times the import graph instead of the sum of the two.
	cache := payloadscan.NewExportCache()
	if err := cache.Warm(root); err != nil {
		return nil, err
	}

	var (
		mu   sync.Mutex
		wg   sync.WaitGroup
		sem  = make(chan struct{}, runtime.GOMAXPROCS(0))
		msgs []string
	)
	for _, dir := range dirs {
		wg.Add(1)
		go func(dir string) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()

			findings, serr := Scan(Config{
				Dir:        dir,
				ImportPath: importPathOf(modPath, root, dir),
				Root:       root,
				AllowFiles: allowFiles,
				Cache:      cache,
			})
			mu.Lock()
			defer mu.Unlock()
			if serr != nil {
				msgs = append(msgs, fmt.Sprintf("scan %s: %v", dir, serr))
				return
			}
			for _, f := range findings {
				msgs = append(msgs, f.String())
			}
		}(dir)
	}
	wg.Wait()

	sort.Strings(msgs)
	return msgs, nil
}

// modulePath reads the module path declared by root/go.mod.
//
// It is read rather than asked of `go list` because every package's
// import path is the module path plus its directory, so one file answers
// for the whole walk and a subprocess per package answers for one.
func modulePath(root string) (string, error) {
	dir, err := os.OpenRoot(root)
	if err != nil {
		return "", fmt.Errorf("kindscan: open %s: %w", root, err)
	}
	defer func() { _ = dir.Close() }()

	src, err := dir.ReadFile("go.mod")
	if err != nil {
		return "", fmt.Errorf("kindscan: read go.mod under %s: %w", root, err)
	}
	for _, line := range strings.Split(string(src), "\n") {
		if rest, ok := strings.CutPrefix(strings.TrimSpace(line), "module "); ok {
			return strings.TrimSpace(rest), nil
		}
	}
	return "", fmt.Errorf("kindscan: %s/go.mod declares no module path", root)
}

// importPathOf spells the import path of a package directory inside the
// module rooted at root.
func importPathOf(modPath, root, dir string) string {
	rel, err := filepath.Rel(root, dir)
	if err != nil || rel == "." {
		return modPath
	}
	return path.Join(modPath, filepath.ToSlash(rel))
}

// Packages lists the package directories under root worth type-checking:
// those whose source mentions the eventbus package, or one of the structs
// that carry a kind into a row.
//
// Narrowing by text first is a cost decision, not a correctness one — a
// package that names none of these cannot hold a write either rule would
// report. The second half of the marker set is not an optimisation
// detail: the scheduler that appended `calendar.reminder` never mentioned
// the eventbus, so the walk did not reach it, and a scan that never looks
// at a file reports the same nothing as one that finds it clean.
// Discovering the rest by walking means a new package is covered the day
// it is written rather than the day somebody remembers to list it.
//
// Reading goes through an [os.Root], as it does in [ScanSQL]: the walk
// produces the paths and the read consumes them, and between those two
// steps a path can stop meaning what it meant. Scoping the reads to the
// root is what keeps the walk from handing this a file outside the tree
// the caller named, rather than a comment saying it will not.
func Packages(root string) ([]string, error) {
	dir, err := os.OpenRoot(root)
	if err != nil {
		return nil, fmt.Errorf("kindscan: open %s: %w", root, err)
	}
	defer func() { _ = dir.Close() }()

	markers := append([]string{"eventbus"}, fieldMarkers(kindFields)...)
	seen := map[string]struct{}{}
	err = fs.WalkDir(dir.FS(), ".", func(name string, entry fs.DirEntry, werr error) error {
		if werr != nil {
			return werr
		}
		if entry.IsDir() {
			switch entry.Name() {
			case "node_modules", "testdata", "generated":
				return fs.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(name, ".go") {
			return nil
		}
		src, rerr := dir.ReadFile(name)
		if rerr != nil {
			return rerr
		}
		for _, marker := range markers {
			if strings.Contains(string(src), marker) {
				// The caller gets the directory as it would open it; the read
				// that classified it was scoped to the root.
				seen[filepath.Join(root, path.Dir(name))] = struct{}{}
				break
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	dirs := make([]string, 0, len(seen))
	for dir := range seen {
		dirs = append(dirs, dir)
	}
	sort.Strings(dirs)
	return dirs, nil
}
