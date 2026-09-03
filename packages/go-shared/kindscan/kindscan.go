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
	"go/parser"
	"go/token"
	"go/types"
	"os"
	"os/exec"
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

// Finding is one string literal the type checker resolved to an event
// kind.
type Finding struct {
	// Pos is the "file:line:col" of the literal.
	Pos string
	// Value is the literal as written, unquoted.
	Value string
}

func (f Finding) String() string {
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
}

// Scan type-checks the package in cfg.Dir and reports every string
// literal whose resolved type is the event-kind type.
//
// Test files are included: a literal in a test is how a kind that no
// production code can name gets asserted on, and a test asserting a
// spelling nothing emits is worse than no test at all.
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

	info := &types.Info{Types: make(map[ast.Expr]types.TypeAndValue)}
	conf := types.Config{
		Importer: payloadscan.NewExportImporter(fset),
		// Errors are collected by Check and returned below. A dependency
		// that will not resolve must not quietly downgrade the scan to
		// "found nothing", which is what a passing guard looks like.
		Error: func(error) {},
	}
	if _, err := conf.Check(bpkg.Name, fset, files, info); err != nil {
		return nil, fmt.Errorf("kindscan: type-check %s: %w", cfg.Dir, err)
	}

	var findings []Finding
	for _, f := range files {
		ast.Inspect(f, func(n ast.Node) bool {
			lit, ok := n.(*ast.BasicLit)
			if !ok || lit.Kind != token.STRING {
				return true
			}
			tv, ok := info.Types[lit]
			if !ok || tv.Type == nil {
				return true
			}
			named, ok := tv.Type.(*types.Named)
			if !ok || named.String() != KindTypeName {
				return true
			}
			if lit.Value == `""` {
				// The zero Kind, not a spelling of one. It is how a helper
				// says "this change has no event", and reporting it would
				// push callers into naming a kind they do not have.
				return true
			}
			pos := fset.Position(lit.Pos())
			if allow[filepath.Base(pos.Filename)] {
				return true
			}
			value, uerr := strconv.Unquote(lit.Value)
			if uerr != nil {
				value = lit.Value
			}
			findings = append(findings, Finding{Pos: pos.String(), Value: value})
			return true
		})
	}
	sort.Slice(findings, func(i, j int) bool { return findings[i].Pos < findings[j].Pos })
	return findings, nil
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
	if err := warmExportCache(root); err != nil {
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

			findings, serr := Scan(Config{Dir: dir, AllowFiles: allowFiles})
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

// warmExportCache builds the module's export data once, serially, before
// the concurrent scan starts.
//
// Without it the first scan to need a package races every other scan
// that needs the same one, and `go list -export` fails under the
// contention. The failure surfaces as a type-check error naming a symbol
// that plainly exists, which reads as a defect in the scanned code
// rather than in the scanner — so it is worth one subprocess to remove.
func warmExportCache(root string) error {
	cmd := exec.Command("go", "list", "-export", "-e", "./...")
	cmd.Dir = root
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("kindscan: warm export cache in %s: %w: %s", root, err, out)
	}
	return nil
}

// Packages lists the package directories under root worth type-checking:
// those whose source mentions the eventbus package at all.
//
// Narrowing by text first is a cost decision, not a correctness one — a
// package that never names the eventbus cannot hold a literal the
// checker would resolve to Kind. Discovering the rest by walking means a
// new package is covered the day it is written rather than the day
// somebody remembers to list it.
func Packages(root string) ([]string, error) {
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
		src, rerr := os.ReadFile(filepath.Clean(path))
		if rerr != nil {
			return rerr
		}
		if strings.Contains(string(src), "eventbus") {
			seen[filepath.Dir(path)] = struct{}{}
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
