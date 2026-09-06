package taskvisibility

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"testing"
)

// The checks in this file are the positive control for the Go half of the
// statement universe. A derived scan that reports nothing looks identical
// whether the tree is clean or the walk stopped matching, and the walk
// here has several separate ways to stop: the file walk, the call
// recognition, the constant resolution, the format expansion, the splice
// recognition. Each one that fails removes statements rather than adding
// findings, so nothing else would notice.
//
// So the walk is pointed at a tree built to contain each shape it has to
// report and each near miss it must not: a projection written without
// qualifying its columns, one that spells the rule out, one whose
// predicate is assembled at run time, one that obtains the rule from the
// shared fragment instead, a projection of no task content, a statement
// handed to something that returns no rows, and one exempted by a marker.

// TestGoProjectionsAreReadAndClassified is the control for the Go walk.
func TestGoProjectionsAreReadAndClassified(t *testing.T) {
	t.Parallel()

	root := writeGoControlTree(t)
	statements, err := GoStatements(root)
	if err != nil {
		t.Fatalf("read Go projections: %v", err)
	}

	var names []string
	for _, s := range statements {
		names = append(names, s.Name)
	}
	sort.Strings(names)
	want := []string{
		"assembledPredicate", "assembledWithTheSharedFragment", "bareColumns",
		"markedProjection", "noContent", "qualifiedAndGuarded",
	}
	if len(names) != len(want) {
		t.Fatalf("the walk read %v, want %v; a statement handed to something that returns "+
			"no rows is not a projection", names, want)
	}
	for i := range names {
		if names[i] != want[i] {
			t.Errorf("statement %d is %q, want %q", i, names[i], want[i])
		}
	}

	findings, inScope, guarded := Check(statements, Sources(""))

	var scoped []string
	for _, s := range inScope {
		scoped = append(scoped, s.Name)
	}
	sort.Strings(scoped)
	wantScope := []string{
		"assembledPredicate", "assembledWithTheSharedFragment", "bareColumns",
		"markedProjection", "qualifiedAndGuarded",
	}
	if len(scoped) != len(wantScope) {
		t.Fatalf("the rule was held against %v, want %v; a projection of no content column "+
			"puts nothing about the task on the wire", scoped, wantScope)
	}
	for i := range scoped {
		if scoped[i] != wantScope[i] {
			t.Errorf("scoped statement %d is %q, want %q", i, scoped[i], wantScope[i])
		}
	}

	// One carries the rule in its own text and one reaches it through the
	// shared fragment. Both are the rule arriving, and a count of one
	// would mean one of the two ways of finding it has stopped matching.
	if guarded != 2 {
		t.Errorf("%d projections were found to carry the rule, want 2: the one that spells "+
			"it out and the one that splices it in", guarded)
	}

	type reported struct {
		name string
		kind FindingKind
	}
	var got []reported
	for _, f := range findings {
		got = append(got, reported{name: f.Statement.Name, kind: f.Kind})
	}
	sort.Slice(got, func(i, j int) bool { return got[i].name < got[j].name })

	wantFindings := []reported{
		{name: "assembledPredicate", kind: Unreadable},
		{name: "bareColumns", kind: Unguarded},
	}
	if len(got) != len(wantFindings) {
		t.Fatalf("the scan reported %v, want %v", got, wantFindings)
	}
	for i := range got {
		if got[i] != wantFindings[i] {
			t.Errorf("finding %d is %+v, want %+v", i, got[i], wantFindings[i])
		}
	}
}

// TestUnreadableFragmentsAreNamedRatherThanGuessedAt pins what happens to
// the part of a statement that is not in the source.
//
// A predicate substituted at run time reaches the source as a format verb
// and nothing else. Dropping the statement would lose a projection that
// is plainly putting content on the wire; treating the missing predicate
// as absent would report "no rule here" about text nobody read. The token
// is the third answer, and it has to survive normalisation to be one.
func TestUnreadableFragmentsAreNamedRatherThanGuessedAt(t *testing.T) {
	t.Parallel()

	root := writeGoControlTree(t)
	statements, err := GoStatements(root)
	if err != nil {
		t.Fatalf("read Go projections: %v", err)
	}
	byName := map[string]Statement{}
	for _, s := range statements {
		byName[s.Name] = s
	}

	assembled, ok := byName["assembledPredicate"]
	if !ok {
		t.Fatal("the fixture's runtime-assembled projection was not read at all")
	}
	if !assembled.Unreadable() {
		t.Errorf("the assembled projection reports nothing unreadable; its predicate is a "+
			"format verb, so what it says about the rule is %q", assembled.Normalized)
	}
	if plain := byName["qualifiedAndGuarded"]; plain.Unreadable() {
		t.Error("a projection written out in full reports part of itself unreadable")
	}
	if spliced := byName["assembledWithTheSharedFragment"]; len(spliced.Spliced) != 1 {
		t.Errorf("the projection that obtains the shared fragment records %d spliced "+
			"predicates, want 1; the splice is what carries the rule into a statement that "+
			"cannot contain it", len(spliced.Spliced))
	}
}

// ruleSQL renders the visibility rule the way a hand-written query
// spells it, with the actor bound as a placeholder.
func ruleSQL(alias string) string {
	return fmt.Sprintf(`%[1]s.visibility = 'public'
    OR (%[1]s.visibility = 'project' AND EXISTS (
      SELECT 1 FROM project_members pm
      WHERE pm.project_id = %[1]s.project_id
        AND pm.user_id = ?
        AND pm.enabled = TRUE
    ))
    OR (%[1]s.visibility = 'private' AND (
      %[1]s.created_by_user_id = ?
      OR EXISTS (
        SELECT 1 FROM task_actors ta
        WHERE ta.task_id = %[1]s.id
          AND ta.kind = 'user'
          AND ta.user_id = ?
          AND ta.enabled = TRUE
      )
    ))`, alias)
}

// writeGoControlTree lays out a minimal apps/flow-api/internal tree and
// returns the root [GoStatements] would be given.
func writeGoControlTree(t *testing.T) string {
	t.Helper()
	root := t.TempDir()

	write := func(rel, body string) {
		path := filepath.Join(root, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
			t.Fatalf("mkdir %s: %v", rel, err)
		}
		if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
			t.Fatalf("write %s: %v", rel, err)
		}
	}

	const aclPackage = "github.com/libraz/nodate-flow/apps/flow-api/internal/acl"

	write("apps/flow-api/internal/acl/check.go", "package acl\n\n"+
		"// TaskVisibilityFilter returns the one spelling of the rule for a\n"+
		"// predicate assembled at run time.\n"+
		"func TaskVisibilityFilter(userID uint32, role int) (string, []any) {\n"+
		"\tconst frag = `"+ruleSQL("t")+"`\n"+
		"\treturn frag, nil\n"+
		"}\n")

	write("apps/flow-api/internal/probe/queries.go", `package probe

import (
	"fmt"
	"strings"

	"`+aclPackage+`"
)

// bareColumns projects task content without qualifying it, which is a
// difference in how the query is typed rather than in what reaches the
// reader.
func bareColumns(ctx Ctx, db DB) {
	_ = db.QueryRowContext(ctx, `+"`"+`SELECT id, title, due_on FROM tasks WHERE public_id = ?`+"`"+`, id)
}

// qualifiedAndGuarded spells the rule out in the statement's own text.
func qualifiedAndGuarded(ctx Ctx, db DB) {
	_ = db.QueryContext(ctx, `+"`"+`SELECT t.title FROM tasks t WHERE `+ruleSQL("t")+"`"+`)
}

// assembledPredicate substitutes the whole predicate at run time and
// obtains no shared fragment, so nothing in the source says what it
// restricts on.
func assembledPredicate(ctx Ctx, db DB, where string) {
	q := fmt.Sprintf(`+"`"+`SELECT t.title FROM tasks t WHERE %s`+"`"+`, where)
	_ = db.QueryContext(ctx, q)
}

// assembledWithTheSharedFragment assembles its predicate the same way and
// takes the rule from the one place it is written.
func assembledWithTheSharedFragment(ctx Ctx, db DB) {
	frag, _ := acl.TaskVisibilityFilter(0, 0)
	where := []string{"t.workspace_id = ?", frag}
	q := fmt.Sprintf(`+"`"+`SELECT t.title FROM tasks t WHERE %s`+"`"+`, strings.Join(where, " AND "))
	_ = db.QueryContext(ctx, q)
}

// noContent projects nothing that says what the task is about.
func noContent(ctx Ctx, db DB) {
	_ = db.QueryContext(ctx, `+"`"+`SELECT t.public_id, t.due_on FROM tasks t`+"`"+`)
}

// notExecuted holds the same projection and hands it to something that
// returns no rows.
func notExecuted(ctx Ctx, log Logger) {
	log.Debug(`+"`"+`SELECT t.title FROM tasks t`+"`"+`)
}

// markedProjection says why it cannot disclose a task the reader may not
// see.
//
// task-visibility: not-applicable — the fixture states a reason so the
// marker is read as one
func markedProjection(ctx Ctx, db DB) {
	_ = db.QueryContext(ctx, `+"`"+`SELECT t.title FROM tasks t`+"`"+`)
}
`)

	return root
}
