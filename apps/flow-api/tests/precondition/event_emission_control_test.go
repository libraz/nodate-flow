package precondition

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// The check in this file is the control for what the event-kind rule
// counts as appending a kind and what it refuses to count.
//
// Both halves decide whether the rule is worth having. Counted too
// loosely, every kind is emitted by its own declaration and the rule
// passes on a tree that appends nothing; counted too tightly, it reports
// kinds that are appended in a spelling it does not read — a wire string
// in a VALUES list, or a name built from a request field — and a rule
// that reports correct code is one that gets switched off. So the
// fixture below puts each shape beside its opposite and pins which is
// which:
//
//   - a constant named by production Go is appended;
//   - a wire string written as a Go literal is appended, because that is
//     how a kind reaches a params struct whose field is a plain string;
//   - a wire string in an INSERT's VALUES is appended, with no Go
//     constant anywhere on the path;
//   - the same string matched in a WHERE clause is not: that statement
//     reads rows somebody else wrote;
//   - a kind built by the declaring package's run-time minter is
//     appended, for every kind under the prefix the minter builds;
//   - a kind named only by the declaration, the family table, the
//     re-export or the delivery-policy table is not appended, however
//     many of the four name it;
//   - a kind named only by a test is not, because a fixture is not a
//     feature;
//   - a kind named only by the web app is not, because the API owns the
//     table and the browser reads what it returns;
//   - a kind named only in a Go or SQL comment is not referenced at all:
//     Go is read as a syntax tree and a named statement has its comments
//     stripped before it is matched. TypeScript is read as text, so a
//     kind quoted in a comment there is a mention — which changes nothing
//     about whether it is appended, since no TypeScript is.

// TestEmissionRuleCountsAnAppendAndNothingElse drives the fixture tree
// and pins which kinds come out unemitted, name by name.
func TestEmissionRuleCountsAnAppendAndNothingElse(t *testing.T) {
	t.Parallel()

	scope := parseControlKinds(t)

	var got []string
	for _, kind := range scope.Unemitted() {
		got = append(got, kind.Name)
	}
	sort.Strings(got)

	want := []string{
		"FixtureCommentOnly",
		"FixtureDeclaredOnly",
		"FixtureSQLRead",
		"FixtureTestOnly",
		"FixtureWebComment",
		"FixtureWebOnly",
	}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("unemitted kinds = %v; want %v.\n"+
			"  Emitted in the fixture: a constant named by a handler, a wire string written as a Go literal, "+
			"a wire string in an INSERT, and two kinds built by the run-time minter.\n"+
			"  Not emitted: one named only in a WHERE clause, one only by a test, two only by the web app, "+
			"one only in Go and SQL comments, and one named by all four declaration surfaces at once.",
			got, want)
	}

	// The kind every surface names is the one that says the exclusions
	// hold. Without them it would be emitted four times over, and so would
	// every other kind in the tree.
	if sites := scope.Emitters("FixtureDeclaredOnly"); len(sites) > 0 {
		t.Errorf("FixtureDeclaredOnly is counted as appended at %s, but it is named only by the declaration, "+
			"the family table, the re-export and the delivery-policy table; counting any of those makes the "+
			"rule pass on every kind",
			describeSites(sites))
	}
	for _, surface := range emissionSurfaces {
		if scope.surfaceHits[surface.Path] == 0 {
			t.Errorf("the fixture's %s dropped no reference; the control is not exercising that exclusion", surface.Path)
		}
	}

	// The minter covers its whole namespace, which is the price of
	// reading a name the caller supplies at run time.
	for _, name := range []string{"FixtureTransitionA", "FixtureTransitionB"} {
		sites := scope.Emitters(name)
		if len(sites) != 1 || !strings.Contains(sites[0].Form, "FixtureTransition(...)") {
			t.Errorf("%s is derived with producers %s; want the single minter call that builds its namespace",
				name, describeSites(sites))
		}
	}

	// The two spellings a single Go append can take, and the SQL one.
	for name, want := range map[string]Language{
		"FixtureByConstant": LangGo,
		"FixtureByWire":     LangGo,
		"FixtureBySQL":      LangSQL,
	} {
		sites := scope.Emitters(name)
		if len(sites) != 1 {
			t.Errorf("%s is derived as having %d producers; want exactly 1 in %s.\n  found: %s",
				name, len(sites), want, describeSites(sites))
			continue
		}
		if sites[0].Lang != want {
			t.Errorf("%s is derived as produced from %s; want %s", name, sites[0].Lang, want)
		}
	}

	// A kind nothing mentions at all and a kind mentioned everywhere it
	// cannot be written are different states, and a failure has to say
	// which it found.
	if mentions := scope.NonProducing("FixtureWebOnly"); len(mentions) == 0 {
		t.Error("FixtureWebOnly is derived with no mentions, but the fixture's web app names it; " +
			"a failure would report it as unheard-of rather than as consumed and never produced")
	}
	if mentions := scope.NonProducing("FixtureCommentOnly"); len(mentions) > 0 {
		t.Errorf("FixtureCommentOnly is derived with mentions at %s, but the fixture names it only in a Go "+
			"comment and in a statement's comment; a comment is prose about a kind, not a reference to it",
			describeSites(mentions))
	}

	// The one comment the scan cannot separate from code, and the reason
	// it does not matter: a browser does not write to the events table, so
	// the loosest reading of a TypeScript file still produces nothing.
	if sites := scope.Emitters("FixtureWebComment"); len(sites) > 0 {
		t.Errorf("FixtureWebComment is counted as appended at %s; a kind quoted in a TypeScript comment is "+
			"read as a mention, and a mention in a language that never writes the row is not a producer",
			describeSites(sites))
	}
}

// parseControlKinds lays out a minimal repository holding one declaring
// package, the surfaces that consume it, and every shape of reference the
// rule has to separate, then parses it the way the real check does.
func parseControlKinds(t *testing.T) *emissionScope {
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

	write(kindDeclarationFile, `package eventbus

// Kind is the type of an entry in the events table.
type Kind string

const (
	FixtureByConstant   Kind = "fixture.by_constant"
	FixtureByWire       Kind = "fixture.by_wire"
	FixtureBySQL        Kind = "fixture.by_sql"
	FixtureSQLRead      Kind = "fixture.sql_read"
	FixtureTestOnly     Kind = "fixture.test_only"
	FixtureWebOnly      Kind = "fixture.web_only"
	FixtureCommentOnly  Kind = "fixture.comment_only"
	FixtureWebComment   Kind = "fixture.web_comment"
	FixtureDeclaredOnly Kind = "fixture.declared_only"

	FixtureTransitionA Kind = "fixture.transition.a"
	FixtureTransitionB Kind = "fixture.transition.b"
)

// FixtureTransition builds a kind from a name supplied at run time.
func FixtureTransition(name string) Kind {
	return Kind("fixture.transition." + name)
}
`)

	write("packages/go-shared/eventbus/registry.go", `package eventbus

// declaredKinds is the enumeration a consumer iterates.
var declaredKinds = []Kind{
	FixtureByConstant, FixtureByWire, FixtureBySQL, FixtureSQLRead,
	FixtureTestOnly, FixtureWebOnly, FixtureCommentOnly, FixtureWebComment,
	FixtureDeclaredOnly,
	FixtureTransitionA, FixtureTransitionB,
}
`)

	write("apps/flow-api/internal/eventbus/kinds.go", `package eventbus

import sharedbus "`+sharedEventbusPath+`"

// Kind mirrors the shared type.
type Kind = sharedbus.Kind

const (
	FixtureDeclaredOnly = sharedbus.FixtureDeclaredOnly
	FixtureByConstant   = sharedbus.FixtureByConstant
)
`)

	write("apps/flow-api/internal/notification/fanout.go", `package notification

import "`+sharedEventbusPath+`"

// classifications gives every kind a delivery policy.
var classifications = map[eventbus.Kind]string{
	eventbus.FixtureDeclaredOnly: "A thing happened",
	eventbus.FixtureWebOnly:      "",
	eventbus.FixtureTestOnly:     "",
}

// categoryFor buckets a kind by its wire string.
func categoryFor(eventType string) string {
	switch eventType {
	case "fixture.declared_only", "fixture.by_sql":
		return "lifecycle"
	default:
		return "other"
	}
}
`)

	write("apps/flow-api/internal/http/handlers/thing/handlers.go", `package thing

import "`+sharedEventbusPath+`"

// Create appends the kind by naming the constant.
func Create() eventbus.Kind { return eventbus.FixtureByConstant }

// Params is the shape sqlc generates: the column is text, so the kind
// arrives as a plain string.
type Params struct{ Type string }

// Record appends the kind by writing its wire string. A comment naming
// fixture.comment_only is prose, and appends nothing.
func Record() Params { return Params{Type: "fixture.by_wire"} }

// Transition appends whichever kind the caller names.
func Transition(name string) eventbus.Kind { return eventbus.FixtureTransition(name) }
`)

	write("apps/flow-api/internal/http/handlers/thing/handlers_test.go", `package thing

import (
	"testing"

	"`+sharedEventbusPath+`"
)

func TestFixtureTestOnly(t *testing.T) {
	if eventbus.FixtureTestOnly == "" {
		t.Fatal("empty kind")
	}
	_ = "fixture.test_only"
}
`)

	write("apps/flow-web/src/features/timeline/filter-bar.tsx", `// The timeline filter offers every kind the API can return.
export const KINDS = ['fixture.web_only', 'fixture.by_constant'];
// A comment naming 'fixture.web_comment' reads as a mention, because a
// text scan cannot tell a comment from code.
`)

	write("sql/queries/thing/thing.sql", `-- name: InsertThingEvent :exec
-- Appends the event. A comment naming 'fixture.comment_only' is prose.
INSERT INTO events (public_id, workspace_id, type, occurred_at)
VALUES (?, ?, 'fixture.by_sql', ?);

-- name: CountThingReads :one
SELECT COUNT(*) FROM events
WHERE workspace_id = ? AND type = 'fixture.sql_read';
`)

	scope, err := parseEmissionScope(root)
	if err != nil {
		t.Fatalf("parse control tree: %v", err)
	}
	if len(scope.kinds) != 11 {
		t.Fatalf("the control tree yielded %d kinds; want 11, so the fixture is being read", len(scope.kinds))
	}
	if len(scope.minters) != 1 || scope.minters[0].Prefix != "fixture.transition." {
		t.Fatalf("the control tree yielded minters %v; want one building fixture.transition.", scope.minters)
	}
	return scope
}
