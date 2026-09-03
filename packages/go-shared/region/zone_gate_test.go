package region

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
	"testing"
)

// Every zone in the product is resolved here, and this is what says so.
//
// The rule the audit found broken was not that anybody wrote a wrong
// conversion. It was that "which zone applies" had six answers and
// "which day is this instant on" had ten, each defensible where it
// stood, and no two of them agreeing about an empty or unresolvable
// name. A stored event therefore dated one way on the calendar, another
// on the agent surface and a third in the reminder about it.
//
// Zone and Day close that by type: nothing outside this package can
// build a location or take a day boundary without a Zone in hand. What a
// type cannot express is "and do not go around it", which is this file —
// a call to time.LoadLocation, a read of time.Local, or a zero-value
// composite literal of either type, anywhere in the non-test tree
// outside this package, is the way around and fails here.
//
// The scan is derived, not listed. It walks every Go file under the
// repository root, so a package created after this test was written is
// in scope the moment its first file lands. A list of packages would
// have stopped covering the tree at the moment it was typed, which is
// the failure mode the rule itself is about.

// bannedSelectors are the `time` package members that answer a zone
// question without being asked one.
//
//   - LoadLocation is the resolution this package owns. A second caller
//     is a second policy for a name that does not resolve, and there
//     were four.
//   - Local is the machine's zone. It is nobody's preference: it is
//     whatever the container was started with, so a boundary taken in it
//     moves when the deployment does.
var bannedSelectors = map[string]string{
	"LoadLocation": "resolve the zone through region.Resolve, which applies the " +
		"preference chain and refuses a name the zoneinfo database does not know",
	"Local": "the process timezone is the deployment's, not the actor's; take " +
		"the zone from the request or the stored row through region.Resolve",
}

// bannedLiterals are the zero-value composite literals that would hand a
// caller a Zone or a Day it never resolved. The zero Zone reads as UTC
// so that a partially-built value cannot panic in a handler, which is
// exactly why writing one has to be refused here instead.
var bannedLiterals = map[string]string{
	"Zone": "obtain a zone from region.Resolve or region.UTC; the zero value " +
		"names no zone and silently reads as UTC",
	"Day": "obtain a day from region.DayOf, region.ParseDay, region.NewDay or " +
		"region.DayFromDateColumn; the zero value names no date",
}

// TestZoneResolutionIsCentralised fails on a zone resolved anywhere but
// here.
func TestZoneResolutionIsCentralised(t *testing.T) {
	t.Parallel()

	root := repoRoot(t)
	files := goSourceFiles(t, root)
	if len(files) == 0 {
		t.Fatal("no Go source files were read; the scan is looking at nothing " +
			"and would pass for a tree that is entirely in breach")
	}

	// The one legitimate caller has to be found, or the scan is matching
	// nothing and this test says only that it ran.
	ownPackage := filepath.ToSlash(filepath.Join("packages", "go-shared", "region"))
	var offenders, inOwnPackage []string

	fset := token.NewFileSet()
	for _, rel := range files {
		file, err := parser.ParseFile(fset, filepath.Join(root, rel), nil, 0)
		if err != nil {
			// A file that does not parse cannot compile, so the build
			// already rejects it and this guard has nothing to add.
			continue
		}
		exempt := strings.HasPrefix(rel, ownPackage+"/")
		for _, finding := range scanZoneEscapes(fset, file, rel) {
			if exempt {
				inOwnPackage = append(inOwnPackage, finding)
				continue
			}
			offenders = append(offenders, finding)
		}
	}

	if len(inOwnPackage) == 0 {
		t.Fatalf("the scan found no zone resolution inside %s, where LoadLocation "+
			"is called. The derivation has stopped matching rather than the "+
			"resolution having moved", ownPackage)
	}
	if len(offenders) > 0 {
		sort.Strings(offenders)
		t.Fatalf("a timezone resolved outside %s is a second policy for the same "+
			"stored name, and the policies disagree:\n  %s",
			ownPackage, strings.Join(offenders, "\n  "))
	}
}

// scanZoneEscapes returns the findings in one parsed file: calls and
// references to [bannedSelectors] on the `time` package, and zero-value
// literals of the types in [bannedLiterals].
func scanZoneEscapes(fset *token.FileSet, file *ast.File, rel string) []string {
	// A file that renames or dot-imports `time` is not handled, and does
	// not need to be: nothing in the tree does either, and gofmt-driven
	// imports do not start.
	discarded := discardedResults(file)
	var out []string
	ast.Inspect(file, func(n ast.Node) bool {
		switch node := n.(type) {
		case *ast.SelectorExpr:
			pkg, ok := node.X.(*ast.Ident)
			if !ok || pkg.Name != "time" {
				return true
			}
			if why, banned := bannedSelectors[node.Sel.Name]; banned {
				out = append(out, fmt.Sprintf("%s:%d time.%s — %s",
					rel, fset.Position(node.Pos()).Line, node.Sel.Name, why))
			}
		case *ast.CompositeLit:
			name, ok := literalTypeName(node.Type)
			if !ok || len(node.Elts) > 0 || discarded[node.Pos()] {
				return true
			}
			if why, banned := bannedLiterals[name]; banned {
				out = append(out, fmt.Sprintf("%s:%d region.%s{} — %s",
					rel, fset.Position(node.Pos()).Line, name, why))
			}
		}
		return true
	})
	return out
}

// discardedResults collects the positions of composite literals that sit
// in a `return ..., err` alongside a non-nil final result.
//
// `return region.Zone{}, err` hands back a value the caller is required
// to ignore, which is how Go spells "no answer" for a value type. It is
// not a zone anybody uses. `return region.Zone{}, nil` is, and stays in
// scope.
func discardedResults(file *ast.File) map[token.Pos]bool {
	out := map[token.Pos]bool{}
	ast.Inspect(file, func(n ast.Node) bool {
		ret, ok := n.(*ast.ReturnStmt)
		if !ok || len(ret.Results) < 2 {
			return true
		}
		last := ret.Results[len(ret.Results)-1]
		if ident, isIdent := last.(*ast.Ident); isIdent && ident.Name == "nil" {
			return true
		}
		for _, result := range ret.Results[:len(ret.Results)-1] {
			if lit, isLit := result.(*ast.CompositeLit); isLit {
				out[lit.Pos()] = true
			}
		}
		return true
	})
	return out
}

// literalTypeName returns the bare type name of a composite literal,
// with or without a package qualifier.
func literalTypeName(expr ast.Expr) (string, bool) {
	switch t := expr.(type) {
	case *ast.SelectorExpr:
		pkg, ok := t.X.(*ast.Ident)
		if !ok || pkg.Name != "region" {
			return "", false
		}
		return t.Sel.Name, true
	case *ast.Ident:
		return t.Name, true
	}
	return "", false
}

// skippedDirs are trees the rule does not reach: generated code, which
// is rewritten from sqlc rather than edited, and the usual non-source
// directories.
var skippedDirs = map[string]bool{
	"node_modules": true,
	"generated":    true,
	"vendor":       true,
	"dist":         true,
	"build":        true,
	"testdata":     true,
}

// repoRoot walks up from the working directory to the go.work that names
// every module in the tree, so the scan covers all of them rather than
// the one this test happens to live in.
func repoRoot(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatalf("working directory: %v", err)
	}
	for {
		if _, statErr := os.Stat(filepath.Join(dir, "go.work")); statErr == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("go.work not found above the working directory")
		}
		dir = parent
	}
}

// goSourceFiles returns every hand-written non-test Go file in the tree,
// relative to root and slash-separated.
//
// Test files are out of scope: a test that pins DST behaviour has to
// name a zone directly, and holding it to the production rule would only
// teach people to write the guard's exemption rather than the code.
func goSourceFiles(t *testing.T, root string) []string {
	t.Helper()
	var out []string
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		name := entry.Name()
		if entry.IsDir() {
			if skippedDirs[name] || (strings.HasPrefix(name, ".") && path != root) {
				return fs.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			return nil
		}
		rel, relErr := filepath.Rel(root, path)
		if relErr != nil {
			return relErr
		}
		out = append(out, filepath.ToSlash(rel))
		return nil
	})
	if err != nil {
		t.Fatalf("walk %s: %v", root, err)
	}
	sort.Strings(out)
	return out
}
