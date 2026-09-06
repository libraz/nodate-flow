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
//   - and a literal written inline in a test and handed straight to one
//     call is out of scope, because which of a handler's paths that call
//     drives is not in the syntax.

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

	const objectKey = modulePath + "/internal/http/handlers/fixture.Object"
	if derived(bundles, objectKey) {
		t.Errorf("fixture.Object is derived as a bundle, but it declares its own method: "+
			"a partial literal of a type with methods is how a caller reaches one of them, "+
			"and holding those to a wiring rule reports ordinary unit tests (required: %v)",
			requiredNames(bundles, objectKey))
	}
}

// TestBundleRuleReportsOnlyLiteralsWithNoVisibleConsumer pins the scope:
// which literals of a bundle have to be complete.
func TestBundleRuleReportsOnlyLiteralsWithNoVisibleConsumer(t *testing.T) {
	t.Parallel()

	src := parseControlTree(t)
	literals := src.Literals(src.Bundles())

	reported := map[string]string{}
	inScope := map[string]bool{}
	for _, lit := range literals {
		inScope[filepath.Base(lit.File)] = true
		if lit.Incomplete() {
			reported[lit.Location()] = lit.Names()
		}
	}

	if !inScope["wire.go"] || !inScope["wire_test.go"] {
		t.Fatalf("the fixture's literals were not read (in scope: %v); the walk is matching nothing", inScope)
	}

	var flagged []string
	for at, names := range reported {
		flagged = append(flagged, at+" "+names)
	}
	sort.Strings(flagged)

	// Two files, four literals that leave Recorder nil, and only three of
	// them answerable: the wiring, the helper that hands a bundle back,
	// and the helper that names the field as nil. The fourth is written
	// inline in a test beside the call it feeds.
	if len(flagged) != 3 {
		t.Errorf("the rule reported %d literals; want 3.\n  reported:\n    %s",
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
			t.Errorf("nothing in %s was reported; a bundle assembled where its consumer is not visible has to be complete", want)
		}
	}
	for _, line := range flagged {
		if strings.Contains(line, "inline") {
			t.Errorf("an inline test literal was reported: %s.\n"+
				"  Which path the call beside it drives is a run-time fact, so reporting it would "+
				"flag every handler test that asserts on an early return", line)
		}
		if !strings.Contains(line, "Recorder") {
			t.Errorf("a literal was reported for something other than the silent collaborator: %s", line)
		}
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
	// An inline literal beside the one call it feeds: this test asserts
	// on a path that returns before the recorder is reached.
	_ = fixture.Handle(fixture.Deps{Store: &rec.Store{}, Secret: "inline"})
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
