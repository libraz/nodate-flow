package precondition

import (
	"strings"
	"testing"
)

// unionOnlyKinds are the members of the frontend union that no Go stream
// kind declares.
//
// Every one of them names an event family that exists in the eventbus and
// has kinds appended against it, and that streamKindForFamily maps to ""
// — deliberately not published. So the member is the frontend already
// holding the shape it would need if that mapping changed, and there is
// nothing for a Go constant to be. A reason here says which family, so
// the entry can be checked against the mapping rather than believed.
//
// The list is not documentation, because it is held to both declarations
// at once. A member that gains a Go constant fails
// [TestEveryStreamKindTheServerSendsIsOneTheBrowserAccepts] as stale, and
// so does one the union stops admitting; either has to be deleted before
// the suite is green again.
var unionOnlyKinds = []UnionOnlyKind{
	{
		Wire:   "favorite.changed",
		Reason: "the favorite family has kinds for adding and removing one, and streamKindForFamily maps the family to \"\"",
	},
	{
		Wire:   "import.changed",
		Reason: "the import family has kinds for a job's whole lifecycle, appended by the imports handler and the MCP import tool, and streamKindForFamily maps the family to \"\"",
	},
	{
		Wire:   "intake.changed",
		Reason: "the intake family has kinds for every disposition an inbox item can take, and streamKindForFamily maps the family to \"\"",
	},
	{
		Wire:   "label.changed",
		Reason: "the label family has kinds for creating, updating and disabling one, and streamKindForFamily maps the family to \"\"",
	},
	{
		Wire:   "reaction.changed",
		Reason: "the reaction family has kinds for adding and removing one, and streamKindForFamily maps the family to \"\"",
	},
}

// TestEveryStreamKindTheServerSendsIsOneTheBrowserAccepts holds the Go
// wire format to the union the browser parses it against.
//
// The two are declared independently and kept in agreement by a comment
// asking a human to do it. Nothing fails when they drift: a Go kind
// missing from the union is a type error in neither language, the server
// publishes it happily, and the cost lands in a browser as a thrown
// iteration inside the SSE reader that the reconnect loop catches and
// discards. Every existing check reads one side alone — the family table
// is total over the families, the frontend switch is total over the union
// — and both stay green while the sets they are total over disagree.
//
// [streamContractScope.Violations] states what counts on each side, why
// only one direction is a defect, and what the rule does not look at. The
// two largest gaps: a wire string present on both sides passes however
// wrongly keysForEvent maps it, including onto a key prefix that matches
// no query; and only this one frontend is read, so a kind admitted here
// is treated as admitted everywhere.
func TestEveryStreamKindTheServerSendsIsOneTheBrowserAccepts(t *testing.T) {
	t.Parallel()

	scope := streamContractSetup(t)
	for _, violation := range scope.Violations(unionOnlyKinds) {
		t.Error(violation.Message)
	}
}

// TestKindsDeclaredOnBothSidesAreNotReported is the arm that says the
// rule is looking at something.
//
// A matcher loose enough to read the file at large instead of the union
// — the switch labels below it quote every kind a second time — reports
// nothing whatever the two sides do, and nothing is also what agreement
// looks like from the outside. A matcher tight enough to miss the union's
// own spelling reports every kind there is. Both are pinned here by kinds
// that are genuinely declared on both sides, one per shape the wire
// format has: a family kind, and the synthetic marker the SSE handler
// sends on connect.
func TestKindsDeclaredOnBothSidesAreNotReported(t *testing.T) {
	t.Parallel()

	scope := streamContractSetup(t)

	reported := map[string]string{}
	for _, violation := range scope.Violations(unionOnlyKinds) {
		reported[violation.Wire] = violation.Message
	}

	for name, wire := range map[string]string{
		"KindCalendarChanged": "calendar.changed",
		"KindItemChanged":     "item.changed",
		"KindResync":          "resync",
	} {
		kind, declared := scope.Declared(wire)
		if !declared {
			t.Errorf("the stream package is derived as not declaring %s (%q); the constant read has stopped "+
				"matching, and every kind it stops seeing is a kind this rule no longer asks about",
				name, wire)
			continue
		}
		if kind.Name != name {
			t.Errorf("%q is derived as declared by %s at %s; want %s", wire, kind.Name, kind.Location(), name)
		}
		if !scope.Admits(wire) {
			t.Errorf("the %s union is derived as not admitting %q, which it declares on its own line; the "+
				"member read is anchored to the wrong span",
				streamUnionTypeName, wire)
		}
		if message, ok := reported[wire]; ok {
			t.Errorf("%q is declared on both sides and was reported anyway:\n  %s", wire, message)
		}
	}
}

// streamContractSetup reads both declarations and asserts each is being
// read at all.
//
// A derived check reports nothing when its derivation stops matching, and
// nothing is also what two lists in agreement look like. Every floor here
// separates the two, at each point the read can quietly empty out: the Go
// constants, the union members, and the span the members are taken from.
func streamContractSetup(t *testing.T) *streamContractScope {
	t.Helper()

	root, err := RepoRoot()
	if err != nil {
		t.Fatalf("locate repository root: %v", err)
	}
	scope, err := parseStreamContract(root)
	if err != nil {
		t.Fatalf("read the stream wire format: %v", err)
	}

	if !scope.kindTypeDeclared {
		t.Fatalf("%s no longer declares the %s type; the constants being looked for are of a type that does "+
			"not exist, so none of them is read",
			streamKindPackageDir, streamKindTypeName)
	}
	if scope.goFiles < 4 {
		t.Fatalf("only %d files were read from %s; the package walk is reading a fraction of it",
			scope.goFiles, streamKindPackageDir)
	}
	if len(scope.kinds) < 10 {
		t.Fatalf("only %d stream kinds were read from %s; the constant declarations have stopped matching",
			len(scope.kinds), streamKindPackageDir)
	}
	if len(scope.members) < 12 {
		t.Fatalf("only %d members were read from the %s union in %s; the declaration has stopped matching and "+
			"the kinds it no longer admits would be reported",
			len(scope.members), streamUnionTypeName, streamUnionFile)
	}
	if strings.TrimSpace(scope.residue) != "" {
		t.Fatalf("the %s union holds %q, which is not a string literal; a member written some other way is "+
			"invisible to the read and the Go kind it admits would be reported",
			streamUnionTypeName, scope.residue)
	}

	// A member read twice is the signature of a span that ran past the
	// type declaration into the switch below it, where every kind is
	// quoted again as a case label. That span admits every kind in the
	// file, which is the reading under which the rule passes on anything.
	seen := map[string]int{}
	for _, member := range scope.members {
		seen[member.Wire]++
		if seen[member.Wire] == 2 {
			t.Fatalf("the %s union is read as admitting %q twice, at %s; the read has escaped the type "+
				"declaration and is counting the switch labels below it",
				streamUnionTypeName, member.Wire, member.Location())
		}
	}

	return scope
}
