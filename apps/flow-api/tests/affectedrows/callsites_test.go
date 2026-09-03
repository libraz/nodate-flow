package affectedrows

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"testing"
)

// A removal statement that returns its affected-row count has already done
// the hard part: the count is the difference between a request that took a
// row away and one that named something that was never there. What kept
// going wrong is the line after it — the caller writes `_, err :=`, throws
// the count away, and answers ok, together with the audit entry and the
// timeline event that say a removal happened.
//
// The expected set is therefore not a list of endpoints. An endpoint list
// stops covering the endpoint added after it was written; this enumerates
// the calls instead, out of the statements sql/queries declares, so a new
// caller of a removal statement is in scope the moment it is written.
//
// A call that legitimately drops the count says so at the call site rather
// than in a table somewhere else, so the exemption cannot outlive the code
// it exempts. One marker covers one call: a second removal in a function
// that already carries a marker takes a second marker, which is the moment
// somebody has to state a reason.

// callSite is one call to a removal statement in hand-written Go.
type callSite struct {
	// Query is the sqlc query name, File and Line where it is called.
	Query string
	File  string
	Line  int
	// Discarded records that the count was assigned to the blank
	// identifier, so nothing downstream can read it.
	Discarded bool
	// Marked records a marker paired to this call.
	Marked bool
	// Function is the enclosing top-level function, for the message.
	Function string
	// AnswersRequests records that the call sits in an HTTP handler
	// package, where a zero count has a not-found response to map onto.
	AnswersRequests bool
	// SeesNotFound records that the enclosing function names a not-found
	// error, which is what a zero count has to turn into there.
	SeesNotFound bool
	// NamedByTheCaller records that the statement is keyed on the public
	// id the request carried, so a zero count is that caller's 404.
	NamedByTheCaller bool
}

// staleMarker is a marker that no in-scope call needed.
type staleMarker struct {
	File string
	Line int
}

// TestRemovalCallersReadTheAffectedRowCount fails on a caller that drops
// the count a removal statement went to the trouble of returning.
func TestRemovalCallersReadTheAffectedRowCount(t *testing.T) {
	t.Parallel()

	sites, stale := scanRepository(t)
	for _, site := range sites {
		if !site.Discarded || site.Marked {
			continue
		}
		t.Errorf("%s:%d: %s discards the affected-row count of %s. Zero rows here means "+
			"nothing matched, so answering ok records a removal that did not happen. Bind "+
			"the count and map zero onto the not-found error for the resource, or say here "+
			"why it cannot answer: %s",
			site.File, site.Line, site.Function, site.Query, MarkerForm)
	}
	for _, marker := range stale {
		t.Errorf("%s:%d: this affected-rows marker covers no call that drops a removal "+
			"count. It exempts nothing and reads as though something was checked; drop it",
			marker.File, marker.Line)
	}
}

// TestRemovalHandlersHaveANotFoundToReport pins the other half: a handler
// that reads the count needs somewhere to put a zero. Where the enclosing
// handler names no not-found error, the count is being read and then
// answered ok anyway, which is the same defect one step further along.
func TestRemovalHandlersHaveANotFoundToReport(t *testing.T) {
	t.Parallel()

	sites, _ := scanRepository(t)
	for _, site := range sites {
		if site.Discarded || site.Marked || site.SeesNotFound {
			continue
		}
		if !site.AnswersRequests || !site.NamedByTheCaller {
			continue
		}
		t.Errorf("%s:%d: %s reads the affected-row count of %s but names no not-found "+
			"error, so a zero count has nothing to turn into and the caller is told the "+
			"removal succeeded",
			site.File, site.Line, site.Function, site.Query)
	}
}

// TestCallSiteScanSeesADiscardedCount is the positive control. It proves
// the scan reports what it is meant to report, and pins the three rules
// that make the marker worth anything: one marker covers one call, a
// marker with no reason is not a marker, and a marker covering nothing is
// reported rather than ignored.
func TestCallSiteScanSeesADiscardedCount(t *testing.T) {
	t.Parallel()

	const src = `package p

// bound reads the count.
func bound(q *Queries) {
	rows, err := q.DisableLabel(ctx, params)
	_ = rows
	_ = err
}

func bare(q *Queries) {
	if _, err := q.DisableLabel(ctx, params); err != nil {
		return
	}
}

// twice removes two rows under one marker.
//
// affected-rows: not-applicable — only the first of the two is accounted for.
func twice(q *Queries) {
	_, _ = q.DisableLabel(ctx, params)
	_, _ = q.DeleteLens(ctx, params)
}

// reasonless carries a marker with nothing after it.
//
// affected-rows: not-applicable —
func reasonless(q *Queries) {
	_, _ = q.DeleteLens(ctx, params)
}

// spare covers a call that does not exist.
//
// affected-rows: not-applicable — this function removes nothing.
func spare(q *Queries) {
	_, _ = q.UpdateLabel(ctx, params)
}
`

	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "sample.go", src, parser.ParseComments)
	if err != nil {
		t.Fatalf("parse sample: %v", err)
	}
	inScope := map[string]Statement{
		"DisableLabel": {Name: "DisableLabel"},
		"DeleteLens":   {Name: "DeleteLens"},
	}
	sites, stale := scanFile(fset, file, "sample.go", inScope)

	var discarded []int
	for _, site := range sites {
		if site.Discarded && !site.Marked {
			discarded = append(discarded, site.Line)
		}
	}
	// The unmarked removal in bare, the second one in twice, and the one
	// under the marker that states no reason.
	if want := []int{11, 21, 28}; !equalInts(discarded, want) {
		t.Errorf("scan reported unmarked discards at lines %v, want %v", discarded, want)
	}
	var stalest []int
	for _, marker := range stale {
		stalest = append(stalest, marker.Line)
	}
	if want := []int{33}; !equalInts(stalest, want) {
		t.Errorf("scan reported stale markers at lines %v, want %v", stalest, want)
	}
}

func equalInts(got, want []int) bool {
	return slices.Equal(got, want)
}

// scanRepository parses every hand-written Go file in the workspace and
// returns the calls to removal statements, plus the markers that covered
// nothing.
//
// Generated queriers are skipped: they declare the methods rather than
// call them. Test files are skipped too — the defect is a request that is
// answered ok, and a test that ignores a count answers nobody.
func scanRepository(t *testing.T) ([]callSite, []staleMarker) {
	t.Helper()

	root, err := RepoRoot()
	if err != nil {
		t.Fatalf("locate repository root: %v", err)
	}
	statements, err := Statements(root)
	if err != nil {
		t.Fatalf("read sql/queries: %v", err)
	}
	inScope := map[string]Statement{}
	for _, s := range Removals(statements) {
		if s.CountIsReachable() {
			inScope[s.Name] = s
		}
	}
	if len(inScope) == 0 {
		t.Fatal("no removal statement returns its affected-row count; the SQL derivation " +
			"has stopped matching rather than the removals having gone away")
	}

	fset := token.NewFileSet()
	var sites []callSite
	var stale []staleMarker
	files := goSourceFiles(t, root)
	if len(files) == 0 {
		t.Fatal("no Go source files were read; the scan is looking at nothing")
	}
	for _, name := range files {
		file, parseErr := parser.ParseFile(fset, filepath.Join(root, name), nil, parser.ParseComments)
		if parseErr != nil {
			t.Fatalf("parse %s: %v", name, parseErr)
		}
		fileSites, fileStale := scanFile(fset, file, name, inScope)
		sites = append(sites, fileSites...)
		stale = append(stale, fileStale...)
	}
	if len(sites) == 0 {
		t.Fatal("no call to a removal statement was found anywhere; the call-site scan " +
			"has stopped matching")
	}
	return sites, stale
}

// scanFile returns the in-scope calls in one parsed file and the markers
// that covered none of them.
func scanFile(fset *token.FileSet, file *ast.File, name string, inScope map[string]Statement) ([]callSite, []staleMarker) {
	imports := importNames(file)
	answersRequests := strings.Contains(name, "/internal/http/handlers/")

	var sites []callSite
	var stale []staleMarker

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
				if c.Pos() >= start && c.End() <= fn.End() && MarkerPattern.MatchString(c.Text) {
					markers = append(markers, c.Pos())
				}
			}
		}
		sort.Slice(markers, func(i, j int) bool { return markers[i] < markers[j] })

		discarded := discardedCalls(fn.Body, imports, inScope)
		var found []callSite
		ast.Inspect(fn.Body, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}
			statement, ok := queryName(call, imports, inScope)
			if !ok {
				return true
			}
			found = append(found, callSite{
				Query:            statement.Name,
				File:             name,
				Line:             fset.Position(call.Pos()).Line,
				Discarded:        discarded[call.Pos()],
				Function:         fn.Name.Name,
				AnswersRequests:  answersRequests,
				SeesNotFound:     namesNotFound(fn),
				NamedByTheCaller: statement.NamedByTheCaller(),
			})
			return true
		})
		sort.Slice(found, func(i, j int) bool { return found[i].Line < found[j].Line })

		// Markers pair with the discarded calls only, in source order: a
		// call that reads its count needs no exemption, and a marker left
		// over is one that stopped covering anything.
		next := 0
		for i := range found {
			if !found[i].Discarded {
				continue
			}
			if next < len(markers) && fset.Position(markers[next]).Line < found[i].Line {
				found[i].Marked = true
				next++
			}
		}
		for ; next < len(markers); next++ {
			stale = append(stale, staleMarker{File: name, Line: fset.Position(markers[next]).Line})
		}
		sites = append(sites, found...)
	}
	return sites, stale
}

// discardedCalls returns the position of every in-scope call whose first
// result is assigned to the blank identifier, which is where the count
// stops being readable.
func discardedCalls(body *ast.BlockStmt, imports map[string]bool, inScope map[string]Statement) map[token.Pos]bool {
	out := map[token.Pos]bool{}
	ast.Inspect(body, func(n ast.Node) bool {
		assign, ok := n.(*ast.AssignStmt)
		if !ok || len(assign.Rhs) != 1 || len(assign.Lhs) < 2 {
			return true
		}
		call, ok := assign.Rhs[0].(*ast.CallExpr)
		if !ok {
			return true
		}
		if _, ok := queryName(call, imports, inScope); !ok {
			return true
		}
		if ident, ok := assign.Lhs[0].(*ast.Ident); ok && ident.Name == "_" {
			out[call.Pos()] = true
		}
		return true
	})
	return out
}

// queryName reports the removal statement a call invokes.
//
// The receiver is deliberately not inspected: *Queries, a WithTx copy and
// a wrapper around either all reach the same statement, and a check keyed
// on "deps.Queries" would be satisfied by renaming the field. What is
// excluded is a call through an imported package name, because a handler
// constructor can share a name with the statement it runs.
func queryName(call *ast.CallExpr, imports map[string]bool, inScope map[string]Statement) (Statement, bool) {
	sel, ok := call.Fun.(*ast.SelectorExpr)
	if !ok {
		return Statement{}, false
	}
	statement, ok := inScope[sel.Sel.Name]
	if !ok {
		return Statement{}, false
	}
	if ident, ok := sel.X.(*ast.Ident); ok && imports[ident.Name] {
		return Statement{}, false
	}
	return statement, true
}

// namesNotFound reports whether a function references a not-found error,
// which is what a zero count has to become in a handler. Both spellings in
// use are accepted: a spec reached through the errors package, and a
// package-level error value the handlers declare for themselves.
func namesNotFound(fn *ast.FuncDecl) bool {
	found := false
	ast.Inspect(fn, func(n ast.Node) bool {
		if ident, ok := n.(*ast.Ident); ok && strings.HasSuffix(ident.Name, "NotFound") {
			found = true
		}
		return !found
	})
	return found
}

// importNames returns the identifiers a file's imports bind.
func importNames(file *ast.File) map[string]bool {
	out := map[string]bool{}
	for _, spec := range file.Imports {
		if spec.Name != nil {
			out[spec.Name.Name] = true
			continue
		}
		path := strings.Trim(spec.Path.Value, `"`)
		out[path[strings.LastIndex(path, "/")+1:]] = true
	}
	return out
}

// skippedDirs are the trees that hold no hand-written caller: vendored
// packages, build output, and the queriers sqlc writes.
var skippedDirs = map[string]bool{
	".git":         true,
	"node_modules": true,
	"backup":       true,
	"dist":         true,
	"bin":          true,
	"generated":    true,
}

// goSourceFiles returns every hand-written Go file in the workspace,
// repository-relative and slash-separated.
func goSourceFiles(t *testing.T, root string) []string {
	t.Helper()
	var out []string
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		name := entry.Name()
		if entry.IsDir() {
			if skippedDirs[name] || strings.HasPrefix(name, ".") && path != root {
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
