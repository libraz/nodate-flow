package precondition

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// The checks in this file are the control for what the dependency-bundle
// rule counts and what it lets past.
//
// The rule is worth having only if the second half is right. Nearly every
// field of a real bundle is legitimately optional — a secret, a flag, an
// object-store client a router forwards without using, a dispatcher the
// handlers test for nil — and a rule that reported those would be
// switched off, after which it protects nothing. So the fixture below
// puts each rejected shape next to the one accepted shape and pins which
// is which:
//
//   - a collaborator that answers for a nil receiver, called through and
//     never tested, is required;
//   - a collaborator that faults on a nil receiver is not, because the
//     omission reports itself the moment the code reaches it;
//   - a field the package tests for nil is not, whichever branch the test
//     guards;
//   - a field the package only reads and hands on is not;
//   - an unexported field is not, because no outside literal can set it;
//   - a struct carrying its own methods is not a bundle at all, since a
//     half-filled literal of one is how a caller reaches a single method;
//   - a literal written inline in a test that leaves out a collaborator
//     which faults on first use is out of scope, because it stops at the
//     first line reaching that collaborator and the author said so by
//     not wiring it;
//   - and the same literal with every such collaborator wired is in
//     scope, because nothing left in the bundle can turn the run back
//     before the silent one.

// TestBundleRuleCountsTheSilentDependencyAndNothingElse drives the
// fixture tree and pins the derived requirement, field by field.
func TestBundleRuleCountsTheSilentDependencyAndNothingElse(t *testing.T) {
	t.Parallel()

	src := parseControlTree(t)
	bundles := src.Bundles()

	const depsKey = modulePath + "/internal/http/handlers/fixture.Deps"
	got := requiredNames(bundles, depsKey)
	sort.Strings(got)
	if strings.Join(got, ",") != "Recorder" {
		t.Errorf("fixture.Deps required = %v; want exactly [Recorder].\n"+
			"  Store faults on a nil receiver, Optional is nil-tested by the package, Secret is a "+
			"value, Forwarded is only handed on, and hidden cannot be set from outside the package.",
			got)
	}
	if !derived(bundles, depsKey) {
		t.Fatal("fixture.Deps was not derived as a bundle; the fixture is holding nothing")
	}

	// The other half of the same reading: which collaborator's absence
	// says a literal was never meant to run the whole path. Without it
	// every inline test literal would be answerable, and the rule would
	// report the tests that assert on an early return.
	enforcing := enforcingNames(bundles, depsKey)
	sort.Strings(enforcing)
	if strings.Join(enforcing, ",") != "Store" {
		t.Errorf("fixture.Deps enforcing = %v; want exactly [Store].\n"+
			"  Recorder answers for a nil receiver so it stops nothing, Optional is nil-tested, "+
			"Secret cannot hold nil, and Forwarded is only handed on.",
			enforcing)
	}

	const objectKey = modulePath + "/internal/http/handlers/fixture.Object"
	if derived(bundles, objectKey) {
		t.Errorf("fixture.Object is derived as a bundle, but it declares its own method: "+
			"a partial literal of a type with methods is how a caller reaches one of them, "+
			"and holding those to a wiring rule reports ordinary unit tests (required: %v)",
			requiredNames(bundles, objectKey))
	}
}

// TestBundleRuleReportsOnlyLiteralsThatAskForTheWholePath pins the
// scope: which literals of a bundle have to be complete.
//
// The fixture writes the two inline test literals side by side. They
// leave the same silent collaborator out and differ only in whether the
// collaborator that faults is wired, which is the whole of the reading —
// so a change that collapses the two shows up here as one of them
// swapping sides rather than as a quiet drift in what the rule covers.
func TestBundleRuleReportsOnlyLiteralsThatAskForTheWholePath(t *testing.T) {
	t.Parallel()

	src := parseControlTree(t)
	literals := src.Literals(src.Bundles())

	reported := map[string]string{}
	seen := map[string]bool{}
	letPast := map[string]bool{}
	for _, lit := range literals {
		seen[filepath.Base(lit.File)] = true
		switch {
		case lit.Reportable():
			reported[lit.Location()] = lit.Names()
		case lit.Incomplete():
			// Read, incomplete, and deliberately not held to it. Kept
			// apart from the complete literals so the control below can
			// tell "let past" from "never looked at".
			letPast[lit.Location()] = true
		}
	}

	if !seen["wire.go"] || !seen["wire_test.go"] {
		t.Fatalf("the fixture's literals were not read (files seen: %v); the walk is matching nothing", seen)
	}

	var flagged []string
	for at, names := range reported {
		flagged = append(flagged, at+" "+names)
	}
	sort.Strings(flagged)

	// Two files, five literals that leave Recorder nil, and four of them
	// answerable: the deployed wiring, the helper that hands a bundle
	// back, the helper that names the field as nil, and the inline test
	// literal that wires the faulting collaborator and so is asking for
	// the path the recorder sits on. The fifth leaves that collaborator
	// out and stops before it.
	if len(flagged) != 4 {
		t.Errorf("the rule reported %d literals; want 4.\n  reported:\n    %s",
			len(flagged), strings.Join(flagged, "\n    "))
	}
	for _, want := range []string{"wire.go", "wire_test.go"} {
		found := false
		for _, line := range flagged {
			if strings.Contains(line, want) {
				found = true
			}
		}
		if !found {
			t.Errorf("nothing in %s was reported; a bundle assembled where nothing can turn the run back has to be complete", want)
		}
	}
	for _, line := range flagged {
		if strings.Contains(line, "unwired") {
			t.Errorf("an inline test literal that leaves the faulting collaborator out was reported: %s.\n"+
				"  It cannot reach the recorder without going red first, so reporting it would flag "+
				"every handler test that asserts on an early return", line)
		}
		if !strings.Contains(line, "Recorder") {
			t.Errorf("a literal was reported for something other than the silent collaborator: %s", line)
		}
	}

	// The inline literal that stops early has to be read and let past,
	// not missing. A walk that stopped matching it would leave this test
	// green for the wrong reason.
	if len(letPast) != 1 {
		t.Errorf("%d incomplete literals were read and let past; want exactly the one that leaves the faulting collaborator out.\n"+
			"  let past: %v", len(letPast), letPast)
	}
}

// parseControlTree lays out a minimal apps/flow-api tree holding one
// bundle, one object, and every literal shape the rule has to separate,
// then parses it the way the real check does.
func parseControlTree(t *testing.T) *bundleSource {
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

	write("apps/flow-api/internal/rec/rec.go", `package rec

// Recorder answers for a nil receiver: losing the row it writes must not
// fail a request whose change already committed.
type Recorder struct{ db int }

func (r *Recorder) Record(action string) {
	if r == nil {
		return
	}
	_ = action
}

// Store faults on a nil receiver, so an unwired one reports itself the
// first time anything reaches it.
type Store struct{ db int }

func (s *Store) Query(name string) int { return s.db + len(name) }
`)

	write("apps/flow-api/internal/http/handlers/fixture/types.go", `package fixture

import "`+modulePath+`/internal/rec"

// Deps is the bundle the handler runs with.
type Deps struct {
	Recorder  *rec.Recorder
	Store     *rec.Store
	Optional  *rec.Recorder
	Forwarded *rec.Recorder
	Secret    string
	hidden    *rec.Recorder
}

// Object carries a method of its own, so it is an object rather than a
// bundle somebody hands in.
type Object struct {
	Recorder *rec.Recorder
}

func (o Object) Run() { o.Recorder.Record("object.run") }
`)

	write("apps/flow-api/internal/http/handlers/fixture/handler.go", `package fixture

import "`+modulePath+`/internal/rec"

// Handle reaches through the bundle the way a handler does.
func Handle(d Deps) int {
	d.Recorder.Record("fixture.handle")
	d.hidden.Record("fixture.hidden")
	n := d.Store.Query(d.Secret)
	if d.Optional != nil {
		d.Optional.Record("fixture.optional")
	}
	return n + forward(d.Forwarded)
}

// Use takes the object, which is what would make it an input if it had
// no methods of its own.
func Use(o Object) { o.Run() }

func forward(r *rec.Recorder) int { return 0 }
`)

	write("apps/flow-api/internal/wiring/wire.go", `package wiring

import (
	"`+modulePath+`/internal/http/handlers/fixture"
	"`+modulePath+`/internal/rec"
)

// Wire is the deployed wiring. It does not get to pick which path runs,
// so it answers for every field the handler package reaches.
func Wire(store *rec.Store) int {
	return fixture.Handle(fixture.Deps{Store: store, Secret: "s"})
}
`)

	write("apps/flow-api/internal/wiring/wire_test.go", `package wiring

import (
	"testing"

	"`+modulePath+`/internal/http/handlers/fixture"
	"`+modulePath+`/internal/rec"
)

// helperDeps hands a bundle back to callers it cannot see, so it answers
// for the fields any of them might drive.
func helperDeps() fixture.Deps {
	return fixture.Deps{Store: &rec.Store{}, Secret: "s"}
}

// nilDeps states the same configuration out loud.
func nilDeps() *fixture.Deps {
	return &fixture.Deps{Recorder: nil, Store: &rec.Store{}, Secret: "s"}
}

func TestHandlerRejectsEmptySecret(t *testing.T) {
	// An inline literal with no store: the first line that reaches one
	// faults, so this case asserts on what happens before that and says
	// so by leaving the store out.
	_ = fixture.Handle(fixture.Deps{Secret: "unwired"})
	// An inline literal with the store wired: nothing left in the bundle
	// can turn the run back, so it is asking for the path the recorder
	// sits on and answers for the recorder.
	_ = fixture.Handle(fixture.Deps{Store: &rec.Store{}, Secret: "wired"})
	_ = helperDeps()
	_ = nilDeps()
}
`)

	src, err := parseBundleSource(root)
	if err != nil {
		t.Fatalf("parse control tree: %v", err)
	}
	if len(src.structs) == 0 {
		t.Fatal("the control tree yielded no struct; the fixture is not being read")
	}
	return src
}
