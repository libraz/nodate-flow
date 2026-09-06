package taskvisibility

import (
	"sort"
	"strings"
	"testing"
)

// TestEveryTaskContentProjectionCarriesTheVisibilityRule reads the schema
// and the query tree and refuses a statement that puts a task's own
// content on the wire without the Layer 4 visibility rule against it.
//
// The rule is not the hard part. What kept happening is that the set of
// statements returning a task title grew past the set applying the rule,
// and each copy of the predicate was typed out again — so a fix to one
// copy left the rest as they were, and a private task's title reached
// readers who may not see it more than once.
//
// Nothing is listed here. The relations exposing task content come out of
// sql/schema.sql, transitively through the views, and the statements
// projecting them out of sql/queries — so a view added tomorrow over
// tasks.title, and a statement added tomorrow over that view, are both
// checked without anyone remembering they exist.
func TestEveryTaskContentProjectionCarriesTheVisibilityRule(t *testing.T) {
	t.Parallel()

	sources, statements := load(t)
	findings, inScope, guarded := Check(statements, sources)

	// A derivation that quietly stops matching passes for the wrong
	// reason, and this one reads files by path. Both ends have to have
	// found something for the result to mean anything: relations that
	// expose task content, and statements that project it.
	requireSourcesFound(t, sources)
	if len(inScope) == 0 {
		t.Fatal("no statement under sql/queries projects a task content column; " +
			"the select-list parser has stopped matching rather than the queries having changed")
	}
	if guarded == 0 {
		t.Fatal("no statement under sql/queries carries the canonical visibility unit; " +
			"the normaliser has stopped matching rather than the rule having been removed")
	}

	for _, f := range findings {
		switch f.Kind {
		case Unguarded:
			t.Errorf("%s: %s projects %s and applies no visibility rule, so a task the "+
				"reader may not see is returned with its content. AND the canonical unit "+
				"into the WHERE clause:\n\n%s\n\nor say above the statement why this "+
				"projection cannot disclose one: %s",
				f.Statement.Location(), f.Statement.Name, Describe(f.Exposure),
				indent(Canonical(f.Exposure.Alias, f.Exposure.Source.Anchors)), MarkerForm)
		case Divergent:
			t.Errorf("%s: %s projects %s behind a visibility predicate that is not the "+
				"canonical unit. Every other statement spells the rule the same way, and a "+
				"second spelling is how one copy gets fixed while the others stay wrong. "+
				"Write it exactly:\n\n%s\n\n(no marker excuses this: something here is "+
				"already applying the rule)",
				f.Statement.Location(), f.Statement.Name, Describe(f.Exposure),
				indent(Canonical(f.Exposure.Alias, f.Exposure.Source.Anchors)))
		case NoAnchor:
			t.Errorf("%s: %s projects %s from a relation that exposes no %s, so the rule "+
				"cannot be written against it at all. Either carry the rule's own inputs "+
				"through the relation the way v_inbox and v_task_list do, project the "+
				"content from one that already does, or say above the statement what "+
				"resolves the task's visibility before this runs: %s",
				f.Statement.Location(), f.Statement.Name, Describe(f.Exposure),
				strings.Join(requiredAnchors(), " / "), MarkerForm)
		case Unreadable:
			t.Errorf("%s: %s projects %s from a statement assembled at run time, and the part "+
				"that would carry the visibility rule is not in the source, so nothing here can "+
				"tell whether a task the reader may not see is returned with its content. "+
				"Move the projection to a statement whose predicate is written down, or say at "+
				"the call site what resolves the task's visibility before this runs: %s",
				f.Statement.Location(), f.Statement.Name, Describe(f.Exposure), MarkerForm)
		case Opaque:
			t.Errorf("%s: %s projects %s, and this could not follow it to the relation it comes "+
				"from — so whether a task the reader may not see is returned with its content "+
				"cannot be decided from the statement. It is reported rather than passed over "+
				"because a projection nobody could follow used to be dropped, and a dropped one "+
				"looks exactly like a statement that projects nothing. Spell the column out as "+
				"`alias.column` against the relation it comes from, or say above the statement "+
				"what resolves the task's visibility before this runs: %s",
				f.Statement.Location(), f.Statement.Name, f.Detail, MarkerForm)
		case StaleMarker:
			// Reported by its own test, which says why.
		}
	}
}

// TestVisibilityMarkersStillApply drops a marker that has outlived what
// it exempted.
//
// A marker is a claim that this statement cannot disclose a task the
// reader may not see. Once the statement stops projecting task content,
// or starts carrying the predicate itself, the claim is no longer about
// anything — and a reader who finds it there concludes the statement was
// considered and cleared.
func TestVisibilityMarkersStillApply(t *testing.T) {
	t.Parallel()

	sources, statements := load(t)
	findings, _, _ := Check(statements, sources)

	marked := 0
	for _, s := range statements {
		if s.Marked() {
			marked++
		}
	}
	if marked == 0 {
		t.Fatal("no statement under sql/queries carries a visibility marker; " +
			"the marker pattern has stopped matching rather than every exemption having been removed")
	}

	for _, f := range findings {
		if f.Kind != StaleMarker {
			continue
		}
		t.Errorf("%s: %s carries a task-visibility marker but nothing was relying on it — "+
			"it either projects no task content or applies the rule anyway. Drop the marker",
			f.Statement.Location(), f.Statement.Name)
	}
}

// TestSplicedFilterMatchesTheGeneratedOne holds the hand-written form of
// the rule to the same spelling as the generated one.
//
// The list endpoints that take optional filters cannot use a sqlc
// statement: sqlc compiles one query per file and the WHERE clause is
// assembled at runtime, so those paths splice acl.TaskVisibilityFilter in
// instead. That is two places the rule is written, which is the situation
// this whole gate exists about. They cannot share a string — one is Go,
// one is SQL sqlc has to parse — but they can be held to being the same
// string once both are normalised.
func TestSplicedFilterMatchesTheGeneratedOne(t *testing.T) {
	t.Parallel()

	root, err := RepoRoot()
	if err != nil {
		t.Fatalf("locate repository root: %v", err)
	}
	fragment, err := ReadVisibilityFilterFragment(root)
	if err != nil {
		t.Fatal(err)
	}
	schema, err := ReadSchema(root)
	if err != nil {
		t.Fatalf("read schema: %v", err)
	}

	// The fragment is spliced against v_task_list under the alias `v` at
	// every call site, so that is the relation whose exposed names it has
	// to be generated with.
	source, ok := Sources(schema)["v_task_list"]
	if !ok || !source.Carries {
		t.Fatal("v_task_list no longer exposes the visibility rule's own inputs, " +
			"so the spliced fragment has nothing to be compared against")
	}

	got := NormalizeFragment(fragment)
	want := Canonical("v", source.Anchors)
	if !strings.Contains(got, want) {
		t.Errorf("acl.TaskVisibilityFilter splices a predicate that is not the canonical unit.\n"+
			"want: %s\ngot:  %s\n\nThe dynamic list paths and the sqlc list queries answer the "+
			"same question for the same caller; when the two spellings drift, one of them is "+
			"already wrong and nothing says which", want, got)
	}
}

// TestSchemaViewsStayInWhatTheSourceWalkFollows refuses a view defined
// through a SELECT of its own.
//
// The half of this package that decides which relations expose task
// content reads a view's select list against the relations its FROM
// clause names. A view reading a derived table or a CTE has content
// arriving from a statement inside it, which that walk does not follow —
// and the way it would not follow it is by finding no content and
// dropping the view out of the source set, taking every statement over
// the view out of scope with it.
//
// No view is written that way today. This fails when the first one is,
// rather than when somebody notices that a statement over it was never
// checked.
func TestSchemaViewsStayInWhatTheSourceWalkFollows(t *testing.T) {
	t.Parallel()

	root, err := RepoRoot()
	if err != nil {
		t.Fatalf("locate repository root: %v", err)
	}
	schema, err := ReadSchema(root)
	if err != nil {
		t.Fatalf("read schema: %v", err)
	}
	views := parseViews(schema)
	if len(views) == 0 {
		t.Fatal("the view parser found no view in sql/schema.sql; it has stopped matching " +
			"rather than every view having been dropped")
	}
	for _, v := range views {
		if derived := derivedTables(v.body); len(derived) > 0 {
			t.Errorf("%s reads a SELECT of its own (bound to %q). The relation walk follows a "+
				"view's columns to the relations its FROM clause names and stops there, so the "+
				"content this one exposes would be attributed to nothing and every statement "+
				"over the view would leave the check's scope. Write the view over the relations "+
				"directly, or teach Sources to follow a derived table the way Exposures does",
				v.name, derived[0].alias)
		}
		if withPrefixPat.MatchString(v.body) {
			t.Errorf("%s is defined through a CTE, which the relation walk does not follow; "+
				"the same applies as for a derived table", v.name)
		}
	}
}

// requiredAnchors names the inputs a relation has to expose before the
// rule can be written against it.
func requiredAnchors() []string {
	out := make([]string, 0, len(anchorSources))
	for _, name := range anchorSources {
		out = append(out, "tasks."+name)
	}
	return out
}

func indent(s string) string { return "    " + s }

// requireSourcesFound fails unless the schema walk found both `tasks` and
// at least one view that inherits its content, which is the half of the
// derivation that has no other witness.
func requireSourcesFound(t *testing.T, sources map[string]*Source) {
	t.Helper()
	if len(sources) < 2 {
		names := make([]string, 0, len(sources))
		for n := range sources {
			names = append(names, n)
		}
		sort.Strings(names)
		t.Fatalf("expected sql/schema.sql to define views exposing task content on top of "+
			"the tasks table; the view parser found only %v", names)
	}
	carriers := 0
	for _, s := range sources {
		if s.Carries {
			carriers++
		}
	}
	if carriers < 2 {
		t.Fatalf("expected more than one relation to expose the visibility rule's own inputs; "+
			"found %d, so the anchor derivation has stopped matching", carriers)
	}
}

func load(t *testing.T) (map[string]*Source, []Statement) {
	t.Helper()
	root, err := RepoRoot()
	if err != nil {
		t.Fatalf("locate repository root: %v", err)
	}
	schema, err := ReadSchema(root)
	if err != nil {
		t.Fatalf("read schema: %v", err)
	}
	files, err := ReadQueries(root)
	if err != nil {
		t.Fatalf("read queries: %v", err)
	}
	if len(files) == 0 {
		t.Fatal("no .sql files found under sql/queries")
	}
	statements := Statements(files)
	if len(statements) == 0 {
		t.Fatal("no named statements found under sql/queries")
	}
	// The Go half is not optional. A projection built in Go reaches the
	// same reader as one declared in sql/queries, and a walk that read
	// zero of them would leave the check passing over half its universe
	// without saying so.
	goStatements, err := GoStatements(root)
	if err != nil {
		t.Fatalf("read Go projections: %v", err)
	}
	if len(goStatements) == 0 {
		t.Fatal("no projection was read out of apps/flow-api/internal. Either the Go walk " +
			"has stopped matching, or every runtime-assembled query in this tree has moved " +
			"to sql/queries — the second would be a change worth confirming here rather " +
			"than one to pass over")
	}
	return Sources(schema), append(statements, goStatements...)
}
