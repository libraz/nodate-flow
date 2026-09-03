// Package vacuousassert derives, from the test sources themselves, the
// two shapes that let an isolation or leak-hygiene test in this
// repository pass while checking nothing.
//
// Both shapes were found the same way: a real defect sat behind a test
// that was already green. The theme-round-trip regression on the TS side
// ranged over zero mock calls and never entered its loop body; the Go
// counterpart is the three cross-tenant tests in tests/e2e that asserted
// a refusal did not carry a resource's title without ever proving the
// title was discoverable by anyone, and the lens-token-hygiene test that
// checked an audit-log column against a slice nothing had shown was
// non-empty. In every case the assertion or the loop body never ran
// against real data, so the check could not have failed no matter what
// the code under test did.
//
//	unproven negation   `NotContains(t, string(body), needle)` where the
//	                     test never shows `needle` is discoverable at all
//	                     — no `Contains` on the same text, no `Equal`
//	                     pinning it, and no non-emptiness proof anywhere
//	                     in the function. A refusal that never carries
//	                     anything and a refusal that never carries the
//	                     one secret worth hiding look identical to this
//	                     assertion; only the first is a security property.
//	unproven loop        a `for rows.Next() { ... }` scan that appends
//	                     into a slice, ranged over later in the same
//	                     function with an assertion per element, with no
//	                     `require.NotEmpty` / `require.Len` on that slice
//	                     in between. A loop over zero rows runs its
//	                     assertions zero times and reports the test as
//	                     passing.
//
// Nothing here is a list of files. Both shapes are read out of the
// parsed AST of every test file under the configured roots, so a new
// test written tomorrow in the same shape is caught without anyone
// registering it — and a check written as a source-text search would
// have the hole this project already found once: a marker mentioned in
// a comment reading as though the call beneath it were covered.
package vacuousassert

import (
	"errors"
	"fmt"
	"go/ast"
	"go/format"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

// MarkerForm is the machine-readable exemption, written as a comment
// anywhere in the enclosing function.
//
// The reason is mandatory and has to read as prose: a marker that states
// no reason is what the free-text exemptions elsewhere in this
// repository decayed into, where the value was written once and never
// read again.
const MarkerForm = "vacuous-assert: not-applicable — <why this needs no positive counterpart>"

// MarkerPattern matches MarkerForm. Requiring the reason to start and
// end with a letter is what stops a mention of the marker — in a doc
// comment describing this package, for instance — from acting as one.
var MarkerPattern = regexp.MustCompile("vacuous-assert:[ \t]*not-applicable[ \t]*—[ \t]*[A-Za-z][^\n]*[A-Za-z]")

// TestRoots are the directories this package scans, repository-relative.
// apps/auth-api/tests does not exist at the time of writing; a missing
// root is skipped rather than failing the scan, so the day it appears it
// is picked up without anyone updating this list.
var TestRoots = []string{
	filepath.Join("apps", "flow-api", "tests"),
	filepath.Join("apps", "auth-api", "tests"),
}

// UnprovenNegation is a NotContains call against a serialized response
// body whose needle the enclosing function never shows to be
// discoverable, or the surrounding response to be non-empty.
type UnprovenNegation struct {
	File     string
	Line     int
	Function string
	Needle   string
	Marked   bool
}

// Location renders the finding's position for a failure message.
func (f UnprovenNegation) Location() string { return fmt.Sprintf("%s:%d", f.File, f.Line) }

// UnprovenLoop is a range loop over a slice built by scanning database
// rows, asserting on each element, with no proof taken first that the
// slice held anything to assert on.
type UnprovenLoop struct {
	File     string
	Line     int
	Function string
	Variable string
	Marked   bool
}

// Location renders the finding's position for a failure message.
func (f UnprovenLoop) Location() string { return fmt.Sprintf("%s:%d", f.File, f.Line) }

// StaleMarker is a marker that no finding in its function needed.
type StaleMarker struct {
	File string
	Line int
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
			return "", errors.New("vacuousassert: go.work not found above the working directory")
		}
		dir = parent
	}
}

// TestFiles returns every *_test.go file under the configured roots,
// repository-relative and slash-separated, in a stable order.
func TestFiles(root string) ([]string, error) {
	var out []string
	for _, testRoot := range TestRoots {
		dir := filepath.Join(root, testRoot)
		if _, statErr := os.Stat(dir); statErr != nil {
			continue
		}
		err := filepath.WalkDir(dir, func(path string, entry fs.DirEntry, walkErr error) error {
			if walkErr != nil {
				return walkErr
			}
			if entry.IsDir() || !strings.HasSuffix(entry.Name(), "_test.go") {
				return nil
			}
			rel, relErr := filepath.Rel(root, path)
			if relErr != nil {
				rel = path
			}
			out = append(out, filepath.ToSlash(rel))
			return nil
		})
		if err != nil {
			return nil, err
		}
	}
	sort.Strings(out)
	return out, nil
}

// Scan parses every test file under the configured roots and returns the
// unproven negations, the unproven loops, and the markers that covered
// neither.
func Scan(root string) ([]UnprovenNegation, []UnprovenLoop, []StaleMarker, error) {
	files, err := TestFiles(root)
	if err != nil {
		return nil, nil, nil, err
	}
	fset := token.NewFileSet()
	var negations []UnprovenNegation
	var loops []UnprovenLoop
	var stale []StaleMarker
	for _, rel := range files {
		src, readErr := os.ReadFile(filepath.Join(root, rel)) //#nosec G304,G122 -- repository path walked at test time
		if readErr != nil {
			return nil, nil, nil, readErr
		}
		file, parseErr := parser.ParseFile(fset, rel, src, parser.ParseComments)
		if parseErr != nil {
			return nil, nil, nil, fmt.Errorf("parse %s: %w", rel, parseErr)
		}
		n, l, s := scanFile(fset, file, rel)
		negations = append(negations, n...)
		loops = append(loops, l...)
		stale = append(stale, s...)
	}
	return negations, loops, stale, nil
}

// candidateKind distinguishes the two shapes for marker pairing, which
// treats every candidate in a function as one shared, position-ordered
// sequence.
type candidateKind int

const (
	kindNegation candidateKind = iota
	kindLoop
)

type candidate struct {
	kind     candidateKind
	pos      token.Pos
	needle   string
	variable string
}

// assertCall is one call this package reasons about, kept in the order
// the AST walk finds it — which, since ast.Inspect walks depth-first, is
// source order within one function body.
type assertCall struct {
	call   *ast.CallExpr
	method string
}

// scanFile scans one parsed file, function by function.
func scanFile(fset *token.FileSet, file *ast.File, relpath string) ([]UnprovenNegation, []UnprovenLoop, []StaleMarker) {
	var negations []UnprovenNegation
	var loops []UnprovenLoop
	var stale []StaleMarker

	for _, decl := range file.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok || fn.Body == nil {
			continue
		}

		candidates := scanFunc(fn)
		sort.Slice(candidates, func(i, j int) bool { return candidates[i].pos < candidates[j].pos })

		start := fn.Pos()
		if fn.Doc != nil {
			start = fn.Doc.Pos()
		}
		var markers []token.Pos
		for _, group := range file.Comments {
			for _, c := range group.List {
				if c.Pos() >= start && c.End() <= fn.End() && MarkerPattern.MatchString(c.Text) {
					markers = append(markers, c.Pos())
				}
			}
		}
		sort.Slice(markers, func(i, j int) bool { return markers[i] < markers[j] })

		// Markers pair with candidates in source order: a candidate
		// preceded by an unused marker is exempted, and a marker left
		// over covered nothing, matching the pairing rule the
		// affected-rows check uses for its own markers.
		next := 0
		for _, c := range candidates {
			line := fset.Position(c.pos).Line
			marked := false
			if next < len(markers) && fset.Position(markers[next]).Line < line {
				marked = true
				next++
			}
			functionName := fn.Name.Name
			switch c.kind {
			case kindNegation:
				negations = append(negations, UnprovenNegation{
					File: relpath, Line: line, Function: functionName,
					Needle: c.needle, Marked: marked,
				})
			case kindLoop:
				loops = append(loops, UnprovenLoop{
					File: relpath, Line: line, Function: functionName,
					Variable: c.variable, Marked: marked,
				})
			}
		}
		for ; next < len(markers); next++ {
			stale = append(stale, StaleMarker{File: relpath, Line: fset.Position(markers[next]).Line})
		}
	}
	return negations, loops, stale
}

// scanFunc returns the candidates in one function that still need a
// marker: a NotContains whose needle is never proven discoverable, and a
// scan-then-range loop never proven non-empty.
func scanFunc(fn *ast.FuncDecl) []candidate {
	var out []candidate

	var calls []assertCall
	ast.Inspect(fn.Body, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		if m, ok := testifyCall(call, "NotContains", "Contains", "Equal", "NotEmpty", "Len"); ok {
			calls = append(calls, assertCall{call, m})
		}
		return true
	})

	hasNonEmptinessProof := false
	for _, c := range calls {
		if strings.HasSuffix(c.method, ".NotEmpty") || strings.HasSuffix(c.method, ".Len") {
			hasNonEmptinessProof = true
			break
		}
	}

	for _, c := range calls {
		if !strings.HasSuffix(c.method, ".NotContains") {
			continue
		}
		args := c.call.Args
		if len(args) < 3 || !isStringConversion(args[1]) {
			continue
		}
		needle := renderExpr(args[2])
		if needleProvenDiscoverable(calls, c.call, needle) || hasNonEmptinessProof {
			continue
		}
		out = append(out, candidate{kind: kindNegation, pos: c.call.Pos(), needle: needle})
	}

	out = append(out, scanLoops(fn, calls)...)
	return out
}

// needleProvenDiscoverable reports whether some other call in the same
// function shows the needle text appearing where it is expected to
// appear: as the target of a Contains, or as either side of an Equal.
func needleProvenDiscoverable(calls []assertCall, self *ast.CallExpr, needle string) bool {
	for _, c := range calls {
		if c.call == self {
			continue
		}
		args := c.call.Args
		switch {
		case strings.HasSuffix(c.method, ".Contains") && len(args) >= 3:
			if renderExpr(args[2]) == needle {
				return true
			}
		case strings.HasSuffix(c.method, ".Equal") && len(args) >= 3:
			if renderExpr(args[1]) == needle || renderExpr(args[2]) == needle {
				return true
			}
		}
	}
	return false
}

// scanLoops finds a `for rows.Next() { ... append(slice, ...) }` scan
// followed, later in the same function, by a `range slice` loop that
// makes an assertion per element, and flags it when no NotEmpty/Len call
// on that slice appears before the range loop.
func scanLoops(fn *ast.FuncDecl, calls []assertCall) []candidate {
	var out []candidate

	scanned := map[string]token.Pos{}
	ast.Inspect(fn.Body, func(n ast.Node) bool {
		forStmt, ok := n.(*ast.ForStmt)
		if !ok || forStmt.Cond == nil {
			return true
		}
		condCall, ok := forStmt.Cond.(*ast.CallExpr)
		if !ok || len(condCall.Args) != 0 {
			return true
		}
		sel, ok := condCall.Fun.(*ast.SelectorExpr)
		if !ok || sel.Sel.Name != "Next" {
			return true
		}
		ast.Inspect(forStmt.Body, func(n2 ast.Node) bool {
			assign, ok := n2.(*ast.AssignStmt)
			if !ok || len(assign.Lhs) != 1 || len(assign.Rhs) != 1 {
				return true
			}
			lhs, ok := assign.Lhs[0].(*ast.Ident)
			if !ok {
				return true
			}
			rhsCall, ok := assign.Rhs[0].(*ast.CallExpr)
			if !ok {
				return true
			}
			if id, ok := rhsCall.Fun.(*ast.Ident); ok && id.Name == "append" {
				scanned[lhs.Name] = forStmt.End()
			}
			return true
		})
		return true
	})

	for varName, scanEnd := range scanned {
		var rangeStmt *ast.RangeStmt
		ast.Inspect(fn.Body, func(n ast.Node) bool {
			rng, ok := n.(*ast.RangeStmt)
			if !ok || rng.Pos() < scanEnd || rangeStmt != nil {
				return true
			}
			id, ok := rng.X.(*ast.Ident)
			if !ok || id.Name != varName {
				return true
			}
			assertsPerElement := false
			ast.Inspect(rng.Body, func(n2 ast.Node) bool {
				if call, ok := n2.(*ast.CallExpr); ok {
					if _, ok := testifyCall(call, "NotContains", "Contains", "Equal", "True", "False", "NoError", "Empty", "NotEmpty"); ok {
						assertsPerElement = true
					}
				}
				return true
			})
			if assertsPerElement {
				rangeStmt = rng
			}
			return true
		})
		if rangeStmt == nil {
			continue
		}
		proven := false
		for _, c := range calls {
			if c.call.Pos() >= rangeStmt.Pos() {
				continue
			}
			if strings.HasSuffix(c.method, ".NotEmpty") || strings.HasSuffix(c.method, ".Len") {
				if len(c.call.Args) >= 2 && renderExpr(c.call.Args[1]) == varName {
					proven = true
					break
				}
			}
		}
		if !proven {
			out = append(out, candidate{kind: kindLoop, pos: rangeStmt.Pos(), variable: varName})
		}
	}
	return out
}

// testifyCall reports whether call is assert.<name> or require.<name>
// for one of names, and returns "assert.Name" / "require.Name".
func testifyCall(call *ast.CallExpr, names ...string) (string, bool) {
	sel, ok := call.Fun.(*ast.SelectorExpr)
	if !ok {
		return "", false
	}
	pkg, ok := sel.X.(*ast.Ident)
	if !ok || (pkg.Name != "assert" && pkg.Name != "require") {
		return "", false
	}
	for _, name := range names {
		if sel.Sel.Name == name {
			return pkg.Name + "." + name, true
		}
	}
	return "", false
}

// isStringConversion reports whether e is a call to the builtin string
// conversion, string(x) — the shape a []byte response body takes before
// a Contains/NotContains check runs against it.
func isStringConversion(e ast.Expr) bool {
	call, ok := e.(*ast.CallExpr)
	if !ok || len(call.Args) != 1 {
		return false
	}
	id, ok := call.Fun.(*ast.Ident)
	return ok && id.Name == "string"
}

// renderExpr renders an expression as source text, so two occurrences of
// the same identifier, selector, or literal compare equal regardless of
// surrounding whitespace.
func renderExpr(e ast.Expr) string {
	var buf strings.Builder
	// format.Node never fails for an ast.Expr with a nil *token.FileSet
	// position base; errors here would only come from a broken writer.
	_ = format.Node(&buf, token.NewFileSet(), e)
	return buf.String()
}
