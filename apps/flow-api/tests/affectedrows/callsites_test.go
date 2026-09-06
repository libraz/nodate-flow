package affectedrows

import (
	"fmt"
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
//
// Which call a marker covers is read the way a person reads it, off the
// syntax rather than off a line distance. A marker is the comment attached
// to a statement, and it reaches that statement plus the ones written
// directly beneath it with no blank line between — the paragraph gofmt
// already draws — up to and including the first removal in it. A doc
// comment reaches the function it introduces. Nothing carries across a
// paragraph break, a block or a function, so a reason written about one
// removal cannot exempt an unrelated one further down the file, and a
// marker that reaches nothing is reported at its own line rather than at
// whichever marker happened to be left over at the end.
//
// Both forms of write are read. A call to the generated method carrying a
// named statement is the form this repository asks for, and a write built
// as a Go string literal is the exception it still contains — see
// literals.go for why matching only the first would cover less and less
// as more SQL is written inline.

// callSite is one call to a removal statement in hand-written Go.
type callSite struct {
	// Query is the sqlc query name, File and Line where it is called.
	Query string
	File  string
	Line  int
	// pos is the same position Line renders, kept so a marker's reach can
	// be compared against the syntax it was written in.
	pos token.Pos
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

// staleMarker is a marker that exempted nothing, reported at its own
// position so a reader is sent to the marker they have to delete.
type staleMarker struct {
	File string
	Line int
	// Reason says which way the marker failed to exempt anything, so the
	// failure names the mistake and not only the line.
	Reason string
}

// The ways a marker can exempt nothing. All three read to a later reader
// as though the count was considered, which is the thing the marker
// exists to make impossible.
const (
	// markerReachesNoRemoval is a marker whose paragraph performs no
	// removal at all: the code it was written about has moved or gone.
	markerReachesNoRemoval = "it reaches no call that drops a removal count"
	// markerCoversABoundCount is a marker over a call that binds its
	// count. Exempting a call that needs no exemption is the same defect
	// as omitting one that does; the count is already being read, so the
	// marker only obscures that.
	markerCoversABoundCount = "the removal under it binds its affected-row count, so there is nothing to exempt"
	// markerRepeatsAnExemption is a second marker over a call an earlier
	// marker already covers. One marker covers one call in both
	// directions, so the surplus reason stands for no decision.
	markerRepeatsAnExemption = "the removal under it is already exempted by the marker above this one"
)

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
		t.Errorf("%s:%d: this affected-rows marker exempts nothing: %s. It reads as though "+
			"the count was considered; delete it, or move it onto the call it is about",
			marker.File, marker.Line, marker.Reason)
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

// inlineDiscards issues the removal as a Go string literal, which is the
// form a scan matching only named statements cannot see at all.
func inlineDiscards(tx *sql.Tx) {
	_, _ = tx.ExecContext(ctx, ` + "`" + `UPDATE labels SET enabled = FALSE WHERE public_id = ? AND enabled = TRUE` + "`" + `, id)
}

// inlineBinds reads the count of the same write, so nothing is dropped.
func inlineBinds(tx *sql.Tx) {
	rows, err := tx.ExecContext(ctx, ` + "`" + `UPDATE labels SET enabled = FALSE WHERE public_id = ? AND enabled = TRUE` + "`" + `, id)
	_ = rows
	_ = err
}

// inlineViaConstant binds the SQL a few lines above the call, which is
// how the wrapped ones are written.
func inlineViaConstant(tx *sql.Tx) {
	const upd = ` + "`" + `UPDATE lenses
		SET deleted_at = NOW()
		WHERE public_id = ? AND deleted_at IS NULL` + "`" + `
	_, _ = tx.ExecContext(ctx, upd, id)
}

// inlineClaim writes a guarded claim rather than a removal: the row comes
// back out of the archived state, so a zero count here says it was
// already archived, not that it was never there.
func inlineClaim(tx *sql.Tx) {
	_, _ = tx.ExecContext(ctx, ` + "`" + `UPDATE tasks SET archived_at = NOW() WHERE id = ? AND archived_at IS NULL` + "`" + `, id)
}

// inlineNotAnExec hands the same removal to something that returns no
// count, so there is none to drop.
func inlineNotAnExec(log *Logger) {
	log.Debug(` + "`" + `UPDATE labels SET enabled = FALSE WHERE public_id = ? AND enabled = TRUE` + "`" + `)
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
	// The unmarked removal in bare, the second one in twice, the one under
	// the marker that states no reason, and the two written as Go string
	// literals — one spelled in the call, one bound to a constant above it.
	if want := []int{11, 21, 28, 41, 57}; !equalInts(discarded, want) {
		t.Errorf("scan reported unmarked discards at lines %v, want %v", discarded, want)
	}
	var stalest []int
	for _, marker := range stale {
		stalest = append(stalest, marker.Line)
	}
	if want := []int{33}; !equalInts(stalest, want) {
		t.Errorf("scan reported stale markers at lines %v, want %v", stalest, want)
	}

	// The discards above are the failures; the whole set of sites is what
	// says the literal half is matching at all. A form that stops matching
	// removes sites rather than adding findings, so nothing else would
	// notice — the write that binds its count and the two that are not
	// removals are as much a part of the evidence as the reported ones.
	var found []int
	for _, site := range sites {
		found = append(found, site.Line)
	}
	if want := []int{5, 11, 20, 21, 28, 41, 46, 57}; !equalInts(found, want) {
		t.Errorf("scan found removal call sites at lines %v, want %v; a guarded claim "+
			"(line 64) and a literal handed to something that returns no count (line 70) "+
			"are not sites, and both literal forms must be", found, want)
	}
}

// TestMarkerCoversOnlyTheCallItIsWrittenAbove pins the pairing itself.
// The exemption is only worth something if it names the write it is about,
// so a marker has to reach the removal beneath it and nothing else: not a
// removal further down the function, and not a second one on the strength
// of one reason. Each of these is a marker that reads to a later reader as
// though the count was considered.
func TestMarkerCoversOnlyTheCallItIsWrittenAbove(t *testing.T) {
	t.Parallel()

	const src = `package p

// distant states its reason over a write that reads its count, while an
// unrelated removal further down the function drops one.
func distant(q *Queries) {
	// affected-rows: not-applicable — written about the write below it.
	rows, err := q.DisableLabel(ctx, params)
	_ = rows
	_ = err

	if err := elsewhere(); err != nil {
		return
	}

	_, _ = q.DeleteLens(ctx, params)
}

// paragraph keeps a marker with the statements written under it: the
// argument the removal takes belongs to the same paragraph, and the blank
// line after the removal ends it.
func paragraph(q *Queries) {
	// affected-rows: not-applicable — the removal below is idempotent here.
	id := lookup()
	_, _ = q.DisableLabel(ctx, id)

	_, _ = q.DeleteLens(ctx, params)
}

// twiceOver states two reasons over one removal.
func twiceOver(q *Queries) {
	// affected-rows: not-applicable — one reason covers this removal.
	// affected-rows: not-applicable — and this one covers nothing.
	_, _ = q.DisableLabel(ctx, params)
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
	// The removal distant drops four statements below its marker, and the
	// one paragraph drops past the blank line that ended its marker's
	// paragraph. Neither marker reaches them; the removals directly under
	// the markers (line 24) and under the first of the pair (line 33) are
	// the ones that are covered.
	if want := []int{15, 26}; !equalInts(discarded, want) {
		t.Errorf("scan reported unmarked discards at lines %v, want %v; a marker that "+
			"reaches past the write it stands over exempts a removal nobody wrote a "+
			"reason for", discarded, want)
	}

	var reported []string
	for _, marker := range stale {
		reported = append(reported, fmt.Sprintf("%d: %s", marker.Line, marker.Reason))
	}
	want := []string{
		"6: " + markerCoversABoundCount,
		"32: " + markerRepeatsAnExemption,
	}
	if !slices.Equal(reported, want) {
		t.Errorf("scan reported stale markers %q, want %q; each is named at its own line "+
			"so the reader is sent to the marker to delete", reported, want)
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
	fileConstants := packageConstants(file)

	var sites []callSite

	for _, decl := range file.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok || fn.Body == nil {
			continue
		}
		resolver := sqlResolver{
			fset:      fset,
			file:      name,
			imports:   imports,
			inScope:   inScope,
			constants: mergeConstants(fileConstants, stringConstants(fn.Body)),
		}
		discarded := discardedCalls(fn.Body, resolver)
		ast.Inspect(fn.Body, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}
			statement, ok := resolver.statementFor(call)
			if !ok {
				return true
			}
			sites = append(sites, callSite{
				Query:            statement.Name,
				File:             name,
				Line:             fset.Position(call.Pos()).Line,
				pos:              call.Pos(),
				Discarded:        discarded[call.Pos()],
				Function:         fn.Name.Name,
				AnswersRequests:  answersRequests,
				SeesNotFound:     namesNotFound(fn),
				NamedByTheCaller: statement.NamedByTheCaller(),
			})
			return true
		})
	}
	sort.Slice(sites, func(i, j int) bool { return sites[i].pos < sites[j].pos })

	return sites, pairMarkers(fset, file, name, sites)
}

// pairMarkers gives each marker the call it was written about and reports
// the ones that were written about nothing. It marks the sites in place.
//
// A marker takes the first removal its paragraph reaches that no earlier
// marker has taken. Taking rather than merely matching is what keeps one
// marker to one call: a paragraph holding two removals needs two reasons,
// and a second marker over a single removal has none left to cover.
func pairMarkers(fset *token.FileSet, file *ast.File, name string, sites []callSite) []staleMarker {
	comments := ast.NewCommentMap(fset, file, file.Comments)
	lists := statementLists(file)
	taken := make([]bool, len(sites))

	var stale []staleMarker
	for _, marker := range markerComments(comments, file) {
		from, to := markerScope(fset, marker.owner, lists, sites)
		reached, free := false, -1
		for i := range sites {
			if sites[i].pos < from || sites[i].pos >= to {
				continue
			}
			reached = true
			if !taken[i] {
				free = i
				break
			}
		}
		report := func(reason string) {
			stale = append(stale, staleMarker{
				File:   name,
				Line:   fset.Position(marker.pos).Line,
				Reason: reason,
			})
		}
		switch {
		case free < 0 && reached:
			report(markerRepeatsAnExemption)
		case free < 0:
			report(markerReachesNoRemoval)
		case !sites[free].Discarded:
			taken[free] = true
			report(markerCoversABoundCount)
		default:
			taken[free] = true
			sites[free].Marked = true
		}
	}
	return stale
}

// markerComment is one marker together with the node it is written about.
type markerComment struct {
	pos   token.Pos
	owner ast.Node
}

// markerComments returns the file's markers in source order, each paired
// with the node the comment map attaches it to. A marker the map attaches
// to nothing keeps a nil owner: standing outside the syntax is what it is
// being reported for, not a reason to stop looking at it.
func markerComments(comments ast.CommentMap, file *ast.File) []markerComment {
	owners := map[*ast.CommentGroup]ast.Node{}
	for node, groups := range comments {
		for _, group := range groups {
			owners[group] = node
		}
	}
	var out []markerComment
	for _, group := range file.Comments {
		for _, c := range group.List {
			if !MarkerPattern.MatchString(c.Text) {
				continue
			}
			out = append(out, markerComment{pos: c.Pos(), owner: owners[group]})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].pos < out[j].pos })
	return out
}

// markerScope returns the source range one marker was written about.
//
// A doc comment introduces a declaration, so a marker in one reaches that
// function's body. A marker inside a body is attached to a statement, and
// reaches that statement plus the ones written directly beneath it in the
// same statement list with no blank line between, stopping at the first
// one that performs a removal. Stopping there is what keeps a marker from
// reading past the call it sits on to a later one: the write it was
// written above is the write it gets.
func markerScope(fset *token.FileSet, owner ast.Node, lists map[token.Pos]stmtRun, sites []callSite) (token.Pos, token.Pos) {
	if owner == nil {
		return token.NoPos, token.NoPos
	}
	if fn, ok := owner.(*ast.FuncDecl); ok {
		if fn.Body == nil {
			return fn.Pos(), fn.End()
		}
		return fn.Body.Pos(), fn.Body.End()
	}
	run, ok := lists[owner.Pos()]
	if !ok {
		return owner.Pos(), owner.End()
	}
	end := run.list[run.index].End()
	for i := run.index; i < len(run.list); i++ {
		end = run.list[i].End()
		if performsARemoval(run.list[i], sites) || i+1 == len(run.list) {
			break
		}
		if fset.Position(end).Line+1 != fset.Position(run.list[i+1].Pos()).Line {
			break
		}
	}
	return run.list[run.index].Pos(), end
}

// performsARemoval reports whether a statement holds an in-scope call.
func performsARemoval(stmt ast.Stmt, sites []callSite) bool {
	for i := range sites {
		if sites[i].pos >= stmt.Pos() && sites[i].pos < stmt.End() {
			return true
		}
	}
	return false
}

// stmtRun is a statement's place among its siblings.
type stmtRun struct {
	list  []ast.Stmt
	index int
}

// statementLists indexes every statement by the list it is written in, so
// the statements beneath a marker can be read off the syntax rather than
// guessed at from a line distance.
func statementLists(file *ast.File) map[token.Pos]stmtRun {
	out := map[token.Pos]stmtRun{}
	record := func(list []ast.Stmt) {
		for i, stmt := range list {
			out[stmt.Pos()] = stmtRun{list: list, index: i}
		}
	}
	ast.Inspect(file, func(n ast.Node) bool {
		switch node := n.(type) {
		case *ast.BlockStmt:
			record(node.List)
		case *ast.CaseClause:
			record(node.Body)
		case *ast.CommClause:
			record(node.Body)
		}
		return true
	})
	return out
}

// discardedCalls returns the position of every in-scope call whose first
// result is assigned to the blank identifier, which is where the count
// stops being readable.
func discardedCalls(body *ast.BlockStmt, resolver sqlResolver) map[token.Pos]bool {
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
		if _, ok := resolver.statementFor(call); !ok {
			return true
		}
		if ident, ok := assign.Lhs[0].(*ast.Ident); ok && ident.Name == "_" {
			out[call.Pos()] = true
		}
		return true
	})
	return out
}

// sqlResolver answers, for one function, which removal a call performs.
// It carries the two vocabularies that question is decided against: the
// named statements sql/queries declares, and the string constants the
// surrounding source binds.
type sqlResolver struct {
	fset      *token.FileSet
	file      string
	imports   map[string]bool
	inScope   map[string]Statement
	constants map[string]string
}

// statementFor reports the removal a call performs, in either form: a
// call to the generated method carrying a named statement, or an exec
// call handed SQL built as a Go string literal.
func (r sqlResolver) statementFor(call *ast.CallExpr) (Statement, bool) {
	if statement, ok := r.namedStatement(call); ok {
		return statement, true
	}
	return r.inlineStatement(call)
}

// namedStatement reports the removal statement a call invokes.
//
// The receiver is deliberately not inspected: *Queries, a WithTx copy and
// a wrapper around either all reach the same statement, and a check keyed
// on "deps.Queries" would be satisfied by renaming the field. What is
// excluded is a call through an imported package name, because a handler
// constructor can share a name with the statement it runs.
func (r sqlResolver) namedStatement(call *ast.CallExpr) (Statement, bool) {
	sel, ok := call.Fun.(*ast.SelectorExpr)
	if !ok {
		return Statement{}, false
	}
	statement, ok := r.inScope[sel.Sel.Name]
	if !ok {
		return Statement{}, false
	}
	if ident, ok := sel.X.(*ast.Ident); ok && r.imports[ident.Name] {
		return Statement{}, false
	}
	return statement, true
}

// inlineStatement reports the removal an exec call performs with SQL
// written as a Go string literal, whether the literal sits in the call or
// is bound to a constant just above it.
//
// Only an exec call is read. A count exists because the driver returns
// sql.Result, so a literal handed to anything else — a logger, a query,
// a helper that builds a message — carries no count to drop.
func (r sqlResolver) inlineStatement(call *ast.CallExpr) (Statement, bool) {
	sel, ok := call.Fun.(*ast.SelectorExpr)
	if !ok || !strings.HasPrefix(sel.Sel.Name, "Exec") {
		return Statement{}, false
	}
	for _, arg := range call.Args {
		text, ok := r.sqlArgument(arg)
		if !ok {
			continue
		}
		if statement, ok := InlineStatement(text, r.file, r.fset.Position(arg.Pos()).Line); ok {
			return statement, true
		}
	}
	return Statement{}, false
}

// sqlArgument returns the SQL text an argument carries.
func (r sqlResolver) sqlArgument(arg ast.Expr) (string, bool) {
	switch node := arg.(type) {
	case *ast.BasicLit:
		if node.Kind != token.STRING {
			return "", false
		}
		return GoStringLiteral(node.Value)
	case *ast.Ident:
		text, ok := r.constants[node.Name]
		return text, ok
	default:
		return "", false
	}
}

// stringConstants maps every identifier the node binds to a plain string
// literal onto that literal's text, so `const upd = "UPDATE ..."` a few
// lines above the exec call resolves.
//
// A name bound twice in the same scan is dropped rather than guessed at:
// crediting one binding's SQL to the other's call site would report a
// write at a position that does not perform it.
func stringConstants(nodes ...ast.Node) map[string]string {
	out := map[string]string{}
	ambiguous := map[string]bool{}
	bind := func(name string, value ast.Expr) {
		if name == "" || name == "_" {
			return
		}
		lit, ok := value.(*ast.BasicLit)
		if !ok || lit.Kind != token.STRING {
			return
		}
		text, ok := GoStringLiteral(lit.Value)
		if !ok {
			return
		}
		if _, seen := out[name]; seen {
			ambiguous[name] = true
			return
		}
		out[name] = text
	}

	for _, node := range nodes {
		ast.Inspect(node, func(n ast.Node) bool {
			switch decl := n.(type) {
			case *ast.ValueSpec:
				for i, ident := range decl.Names {
					if i < len(decl.Values) {
						bind(ident.Name, decl.Values[i])
					}
				}
			case *ast.AssignStmt:
				if len(decl.Lhs) != len(decl.Rhs) {
					return true
				}
				for i, lhs := range decl.Lhs {
					if ident, ok := lhs.(*ast.Ident); ok {
						bind(ident.Name, decl.Rhs[i])
					}
				}
			}
			return true
		})
	}
	for name := range ambiguous {
		delete(out, name)
	}
	return out
}

// packageConstants returns only the file's top-level string bindings. A
// scan of the whole file would collect every function's locals into one
// namespace, where two functions each declaring `const upd` would credit
// one function's SQL to the other's call site.
func packageConstants(file *ast.File) map[string]string {
	var decls []ast.Node
	for _, decl := range file.Decls {
		if gen, ok := decl.(*ast.GenDecl); ok {
			decls = append(decls, gen)
		}
	}
	return stringConstants(decls...)
}

// mergeConstants layers a function's own bindings over the file's, which
// is the order Go resolves them in.
func mergeConstants(outer, inner map[string]string) map[string]string {
	out := make(map[string]string, len(outer)+len(inner))
	for name, text := range outer {
		out[name] = text
	}
	for name, text := range inner {
		out[name] = text
	}
	return out
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
