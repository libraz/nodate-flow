package mcp

import (
	"go/ast"
	"go/parser"
	"go/token"
	"regexp"
	"sort"
	"testing"
)

// The MCP transport answers the same questions the REST handlers answer,
// about the same rows. Every time it asked the database itself, with its
// own query string, it ended up with its own rule — and the two drifted
// without anything noticing, because a hand-written SELECT compiles
// exactly as well as the right one.
//
// The queries in sql/queries are the shared source: sqlc generates them
// for both transports, so a change to one reaches the other. The
// constraint here is that this package reaches the database through the
// generated queriers, and a call that goes around them says why at the
// call site.
//
// The exemption lives with the call rather than in a list, so it cannot
// outlive the code it exempts, and one marker exempts one call: adding a
// second query to a function that already carries a marker takes a
// second marker, which is the moment somebody has to state a reason.

// directSQLMethods are the database/sql methods that take a query string.
// A call to one of them is hand-written SQL by definition — generated
// code reaches the driver under its own query names, never these.
var directSQLMethods = map[string]bool{
	"Query":           true,
	"QueryContext":    true,
	"QueryRow":        true,
	"QueryRowContext": true,
	"Exec":            true,
	"ExecContext":     true,
	"Prepare":         true,
	"PrepareContext":  true,
}

// directSQLMarker exempts one call:
//
//	no-generated-query: <why no generated query covers this>
//
// The reason is mandatory and has to read as prose. Requiring the text
// after the colon to start and end with a letter is what stops a mention
// of the marker from acting as one, which is the same rule
// scripts/check-reachability.mjs uses for its own exemption.
var directSQLMarker = regexp.MustCompile(`no-generated-query:[ \t]*[A-Za-z][^\n]*[A-Za-z]`)

// isDirectSQLCall reports whether a node is a call to one of the
// database/sql query methods, on any receiver.
//
// The receiver is deliberately not inspected: *sql.DB, *sql.Tx and a
// wrapper around either all reach the driver with a query string this
// package wrote, and a check keyed on "deps.DB" would be satisfied by
// renaming the field.
func isDirectSQLCall(n ast.Node) bool {
	call, ok := n.(*ast.CallExpr)
	if !ok {
		return false
	}
	sel, ok := call.Fun.(*ast.SelectorExpr)
	if !ok {
		return false
	}
	return directSQLMethods[sel.Sel.Name]
}

// unmarkedDirectSQL returns the position of every direct-SQL call in one
// parsed file that carries no marker of its own, in source order.
//
// A marker counts for a call when it sits above it, anywhere from the
// enclosing function's doc comment down to the call itself. Markers are
// paired to calls in source order, one each, so a function with two
// queries needs two.
func unmarkedDirectSQL(file *ast.File) []token.Pos {
	covered := map[token.Pos]bool{}
	var unmarked []token.Pos

	for _, decl := range file.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok || fn.Body == nil {
			continue
		}
		start := fn.Pos()
		if fn.Doc != nil {
			start = fn.Doc.Pos()
		}

		var markers []token.Pos
		for _, group := range file.Comments {
			for _, c := range group.List {
				if c.Pos() >= start && c.End() <= fn.End() && directSQLMarker.MatchString(c.Text) {
					markers = append(markers, c.Pos())
				}
			}
		}
		sort.Slice(markers, func(i, j int) bool { return markers[i] < markers[j] })

		var calls []token.Pos
		ast.Inspect(fn.Body, func(n ast.Node) bool {
			if isDirectSQLCall(n) {
				calls = append(calls, n.Pos())
				covered[n.Pos()] = true
			}
			return true
		})
		sort.Slice(calls, func(i, j int) bool { return calls[i] < calls[j] })

		next := 0
		for _, call := range calls {
			if next < len(markers) && markers[next] < call {
				next++
				continue
			}
			unmarked = append(unmarked, call)
		}
	}

	// A query outside any function body — a package-level initializer —
	// has nowhere to carry a marker, so it is always reported.
	ast.Inspect(file, func(n ast.Node) bool {
		if isDirectSQLCall(n) && !covered[n.Pos()] {
			unmarked = append(unmarked, n.Pos())
		}
		return true
	})

	sort.Slice(unmarked, func(i, j int) bool { return unmarked[i] < unmarked[j] })
	return unmarked
}

// TestMCPReachesTheDatabaseThroughGeneratedQueries fails on a query this
// package writes itself and does not account for.
func TestMCPReachesTheDatabaseThroughGeneratedQueries(t *testing.T) {
	t.Parallel()

	fset := token.NewFileSet()
	files := mcpPackageSourceFiles(t)
	if len(files) == 0 {
		t.Fatal("no source files were read from the package; the check is looking at nothing")
	}
	for _, name := range files {
		file, err := parser.ParseFile(fset, name, nil, parser.ParseComments)
		if err != nil {
			t.Fatalf("parse %s: %v", name, err)
		}
		for _, pos := range unmarkedDirectSQL(file) {
			t.Errorf("%s: this reaches the database with a query string of its own. "+
				"sql/queries is what keeps MCP and REST reading the same rows under the same rules, "+
				"so route it through deps.Queries or deps.CalendarQueries. If no generated query "+
				"covers it, say so above the call: the marker is no-generated-query followed by the reason",
				fset.Position(pos))
		}
	}
}

// TestDirectSQLScanSeesAnUnmarkedCall is the positive control: it proves
// the scan above reports what it is meant to report, rather than passing
// because it finds nothing.
//
// It also pins the two rules that make the marker worth anything — one
// marker covers one call, and a marker with no reason is not a marker.
func TestDirectSQLScanSeesAnUnmarkedCall(t *testing.T) {
	t.Parallel()

	const src = `package p

// marked reads one row.
//
// no-generated-query: this one is accounted for.
func marked(db *sql.DB) {
	db.QueryRowContext(ctx, "SELECT 1")
}

func bare(db *sql.DB) {
	db.QueryRowContext(ctx, "SELECT 2")
}

// twice runs two queries under one marker.
//
// no-generated-query: only the first of the two is accounted for.
func twice(db *sql.DB) {
	db.QueryRowContext(ctx, "SELECT 3")
	db.ExecContext(ctx, "DELETE FROM t")
}

// reasonless carries a marker with nothing after it.
//
// no-generated-query:
func reasonless(db *sql.DB) {
	db.ExecContext(ctx, "DELETE FROM u")
}

// generated goes through a querier and needs no marker.
func generated(q *Queries) {
	q.FindCalendarMember(ctx, params)
}
`

	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "sample.go", src, parser.ParseComments)
	if err != nil {
		t.Fatalf("parse sample: %v", err)
	}

	var got []int
	for _, pos := range unmarkedDirectSQL(file) {
		got = append(got, fset.Position(pos).Line)
	}

	// The unmarked query in bare, the second query in twice, and the one
	// under the marker that says nothing.
	want := []int{11, 19, 26}
	if len(got) != len(want) {
		t.Fatalf("scan reported %d unmarked calls at lines %v, want %d at %v", len(got), got, len(want), want)
	}
	for i := range got {
		if got[i] != want[i] {
			t.Errorf("unmarked call %d is at line %d, want %d", i, got[i], want[i])
		}
	}
}
