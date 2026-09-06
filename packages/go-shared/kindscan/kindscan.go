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
package kindscan

import (
	"fmt"
	"go/ast"
	"go/build"
	"go/constant"
	"go/parser"
	"go/token"
	"go/types"
	"os"
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
	// AllowFiles names the base filenames exempt from the rule. The file
	// that declares the constants is the only legitimate place a kind is
	// written as a literal.
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
// spelling nothing emits is worse than no test at all.
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
		allow[name] = true
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

	names := make([]string, 0, len(bpkg.GoFiles)+len(bpkg.TestGoFiles))
	names = append(names, bpkg.GoFiles...)
	names = append(names, bpkg.TestGoFiles...)
	if len(names) == 0 {
		return nil, nil
	}

	fset := token.NewFileSet()
	files := make([]*ast.File, 0, len(names))
	for _, name := range names {
		f, perr := parser.ParseFile(fset, filepath.Join(cfg.Dir, name), nil, 0)
		if perr != nil {
			return nil, fmt.Errorf("kindscan: parse %s: %w", name, perr)
		}
		files = append(files, f)
	}

	// A scan given no cache still memoises within itself; see the same
	// fallback in payloadscan.Scan.
	cache := cfg.Cache
	if cache == nil {
		cache = payloadscan.NewExportCache()
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
		Importer: cache.Importer(fset),
		// Errors are collected by Check and returned below. A dependency
		// that will not resolve must not quietly downgrade the scan to
		// "found nothing", which is what a passing guard looks like.
		Error: func(error) {},
	}
	if _, err := conf.Check(bpkg.Name, fset, files, info); err != nil {
		return nil, fmt.Errorf("kindscan: type-check %s: %w", cfg.Dir, err)
	}

	fields := cfg.Fields
	if fields == nil {
		fields = kindFields
	}

	var findings []Finding
	report := func(pos token.Position, value string, viaUndeclared bool) {
		if allow[filepath.Base(pos.Filename)] {
			return
		}
		findings = append(findings, Finding{Pos: pos.String(), Value: value, ViaUndeclared: viaUndeclared})
	}
	reportFieldWrites := func(writes []fieldWrite) {
		for _, w := range writes {
			pos := fset.Position(w.expr.Pos())
			if allow[filepath.Base(pos.Filename)] {
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
	sort.Slice(findings, func(i, j int) bool { return findings[i].Pos < findings[j].Pos })
	return findings, nil
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
// The module guards call this rather than each assembling its own walk,
// so a rule tightened in one module cannot be looser in the next.
//
// Packages are walked concurrently because the checker spends most of
// its time waiting on the build cache; serially this is slow enough that
// the guard becomes something people skip.
func ScanModule(root string, allowFiles ...string) ([]string, error) {
	dirs, err := Packages(root)
	if err != nil {
		return nil, err
	}
	if len(dirs) == 0 {
		return nil, fmt.Errorf("kindscan: no package under %s references the eventbus; a scan here would prove nothing", root)
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

			findings, serr := Scan(Config{Dir: dir, AllowFiles: allowFiles, Cache: cache})
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
func Packages(root string) ([]string, error) {
	markers := append([]string{"eventbus"}, fieldMarkers(kindFields)...)
	seen := map[string]struct{}{}
	err := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() {
			switch info.Name() {
			case "node_modules", "testdata", "generated":
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") {
			return nil
		}
		src, rerr := os.ReadFile(filepath.Clean(path)) //#nosec G122 -- the walk root is the repository source tree, fixed by the caller
		if rerr != nil {
			return rerr
		}
		for _, marker := range markers {
			if strings.Contains(string(src), marker) {
				seen[filepath.Dir(path)] = struct{}{}
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
