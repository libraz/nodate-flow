package precondition

import (
	"strings"
	"testing"
)

// TestBundleWiringSetsEverySilentDependency holds the wiring that
// assembles a handler's dependency bundle to the fields that bundle's own
// package calls through.
//
// The failure it is written against leaves no trace. A bundle is a plain
// struct, so a literal that omits a field compiles, and the omission
// arrives as a nil pointer. Where the collaborator on the other end
// answers for a nil receiver — mutationlog logs a dropped change rather
// than failing a write that already committed, the audit recorder does
// nothing at all — the request succeeds, the response is correct, every
// assertion about it holds, and the rows nobody queried are missing. A
// nil querier or a nil database handle cannot fail that way: the first
// use faults, so the omission reports itself.
//
// The check is derived end to end. Which structs are bundles, which of
// their fields are load-bearing, and which collaborators go quiet rather
// than loud are all read out of the tree, so a handler package added next
// month is covered the day its first exported handler takes a bundle.
//
// [bundleSource.Literals] states the limits. The scope is the deployed
// wiring, any helper that hands a bundle back to callers it cannot see,
// and any literal that names every collaborator of its bundle that
// faults on first use — including one written inline in a test, which is
// the shape the failure took. Whoever wires all of those has nothing
// left in the bundle that can turn the run back, so the code reaching
// the silent collaborator is code that literal is asking to run.
//
// What it does not look at is which branch the request takes. Nothing
// here is control flow: a test that asserts on a rejected signature or a
// missing workspace is out only because it leaves the querier and the
// handle out too, not because the rule can see where it returns. Wire
// those into an early-return case and it is reported; hand a fully wired
// bundle to the one operation in a package that records nothing and it
// is reported as well.
func TestBundleWiringSetsEverySilentDependency(t *testing.T) {
	t.Parallel()

	src, bundles, literals := bundleScope(t)

	for _, lit := range literals {
		if !lit.Reportable() {
			continue
		}
		t.Errorf("%s builds %s and leaves %s.\n"+
			"  A nil there is not a fault: the collaborator answers for a nil receiver, so the "+
			"request succeeds and the rows it was supposed to write are simply absent.\n"+
			"  Set the field, or route this literal through the constructor that does.",
			lit.Location(), lit.Type.Name, lit.Names())
	}

	// A derived check reports nothing when its derivation stops matching,
	// and nothing is also what a clean tree looks like. The scope is
	// asserted so the two cannot be confused, at each of the three points
	// it can quietly empty out: the bundles, the literals read, and the
	// literals actually held to the requirement.
	if len(bundles) < 10 {
		t.Errorf("only %d dependency bundles were derived from the tree; the derivation has stopped matching rather than the bundles having gone away",
			len(bundles))
	}
	if len(literals) < 10 {
		t.Errorf("only %d bundle literals were read; the literal walk has stopped matching",
			len(literals))
	}
	answerable := 0
	for _, lit := range literals {
		if lit.Answerable {
			answerable++
		}
	}
	if answerable < 10 {
		t.Errorf("only %d of %d bundle literals are answerable for what they leave nil; the scope has collapsed and the rule is holding almost nothing",
			answerable, len(literals))
	}

	// The bundle that motivated this, checked by name against the source
	// so a rename fails here instead of quietly shrinking the scope.
	const signalsDeps = modulePath + "/internal/http/handlers/signals.Deps"
	if src.structs[signalsDeps] == nil {
		t.Fatalf("the signals handler package no longer declares Deps; the derivation is being checked against a type that does not exist")
	}
	required := requiredNames(bundles, signalsDeps)
	if !contains(required, "Mutations") {
		t.Errorf("signals.Deps.Mutations is not derived as required (required: %v); the inbound webhook path records a change through it and nothing tests it for nil",
			required)
	}

	// The inbound webhook path also holds a querier that faults on nil,
	// which is what lets a bundle assembled inside a test case be judged
	// at all: a case that wires it has wired everything that could stop
	// the request short of the recorder. Derive nothing enforcing here
	// and every literal written inside a test function is let past, which
	// is the shape the failure this rule exists for takes.
	enforcing := enforcingNames(bundles, signalsDeps)
	if !contains(enforcing, "Queries") {
		t.Errorf("signals.Deps.Queries is not derived as enforcing (enforcing: %v); a bundle built inside a test case can no longer be judged, and the inbound webhook path is where that matters",
			enforcing)
	}
}

// TestOptionalWiringIsNotHeldToTheRule is the other half of the same
// question, and the half that decides whether the check is worth having.
//
// Most of what a dependency bundle carries is optional by design. The
// test harness builds the router without object storage and without an
// email transport, and the router forwards both onward without using
// either; the webhook bundle carries a judge dispatcher the handlers test
// for nil before waking. A rule that called those omissions failures
// would be switched off within a week, and would then protect nothing.
func TestOptionalWiringIsNotHeldToTheRule(t *testing.T) {
	t.Parallel()

	src, bundles, literals := bundleScope(t)

	// The router bundle is in scope — its package calls through it — so
	// what it is not held to is a reading rather than an absence.
	const routerDeps = modulePath + "/internal/http/router.Deps"
	if src.structs[routerDeps] == nil {
		t.Fatalf("the router package no longer declares Deps; this control is being checked against a type that does not exist")
	}
	if !derived(bundles, routerDeps) {
		t.Fatalf("router.Deps is not derived as a bundle at all; the control below would hold nothing")
	}

	// Fields the router only forwards without using, and a field the
	// webhook handlers test for nil before waking. None is something a
	// literal owes.
	for _, field := range []string{"Storage", "EmailSender", "JudgeEnqueuer"} {
		for _, bundle := range bundles {
			if contains(requiredNames(bundles, bundle.Key), field) {
				t.Errorf("%s.%s is derived as required, but it is optional configuration: "+
					"holding a literal to it would report the wiring that deliberately leaves it out",
					bundle.Name, field)
			}
		}
	}

	// The harness that boots the full route set for the integration
	// suite, and the helper that builds the webhook bundle every case in
	// that suite runs through. Both are in scope by construction — the
	// first is not a _test.go file, the second hands a bundle back — so
	// their silence is a reading rather than an absence.
	for _, want := range []string{
		"apps/flow-api/tests/helpers/server.go",
		"apps/flow-api/tests/e2e/webhook_inbound_dedupe_test.go",
	} {
		seen := false
		for _, lit := range literals {
			if lit.File != want {
				continue
			}
			seen = true
			if !lit.Answerable {
				t.Errorf("%s builds %s and is not answerable; this file is the control for a rule that fires on legitimate wiring, and a literal it no longer holds proves nothing",
					lit.Location(), lit.Type.Name)
				continue
			}
			if lit.Incomplete() {
				t.Errorf("%s builds %s and is reported for %s; this literal wires what its package reaches and the rule is wrong to flag it",
					lit.Location(), lit.Type.Name, lit.Names())
			}
		}
		if !seen {
			t.Errorf("no bundle literal in %s was in scope; this file is the control for a rule that fires on legitimate wiring, and it is holding nothing",
				want)
		}
	}
}

// TestPartialTestLiteralsAreReadAndLetPast is the control for the other
// way this rule could go wrong: firing on a test that builds the bundle
// it needs and no more.
//
// Each file below drives a path that returns before the silent
// collaborator — a Slack URL-verification handshake, a request with no
// workspace on the context, an invite accept measured against a stub
// driver — and each leaves out a collaborator that would fault if the
// request went further. That omission is the author saying where the
// request stops, and it is the whole of what keeps these out.
//
// So the assertion is three-part, because two of the three can hold
// while the rule protects nothing. The literal has to still be read as a
// bundle literal, it has to still leave a required field nil, and only
// then does it have to be let past. A file whose literal has vanished, or
// whose type is no longer derived as a bundle, fails here rather than
// passing quietly.
func TestPartialTestLiteralsAreReadAndLetPast(t *testing.T) {
	t.Parallel()

	_, _, literals := bundleScope(t)

	for _, file := range []string{
		"apps/flow-api/internal/http/handlers/signals/webhooks_slack_test.go",
		"apps/flow-api/internal/http/handlers/export/handler_test.go",
		"apps/flow-api/internal/http/handlers/calendars/invite_accept_roundtrips_test.go",
	} {
		read, partial := 0, 0
		for _, lit := range literals {
			if lit.File != file {
				continue
			}
			read++
			if !lit.Incomplete() {
				continue
			}
			partial++
			if lit.Answerable {
				t.Errorf("%s builds %s and is reported for %s.\n"+
					"  This case asserts on a path that returns before the field is reached, and it "+
					"leaves out a collaborator that would fault if the request went further. Reporting "+
					"it means the rule now fires on correct code.",
					lit.Location(), lit.Type.Name, lit.Names())
			}
		}
		if read == 0 {
			t.Errorf("no bundle literal in %s was read at all; this file is the control for a rule that fires on a test wiring only what it drives, and it is holding nothing",
				file)
			continue
		}
		if partial == 0 {
			t.Errorf("every bundle literal in %s now sets each required field; this control passes without exercising the reading it exists for",
				file)
		}
	}
}

// bundleScope parses the tree once and returns the derivation together
// with the literals it covers, failing loudly on an empty read rather
// than letting a check range over nothing.
func bundleScope(t *testing.T) (*bundleSource, []BundleType, []BundleLiteral) {
	t.Helper()

	root, err := RepoRoot()
	if err != nil {
		t.Fatalf("locate repository root: %v", err)
	}
	src, err := parseBundleSource(root)
	if err != nil {
		t.Fatalf("parse apps/flow-api: %v", err)
	}
	if len(src.structs) == 0 {
		t.Fatal("no struct was read from apps/flow-api; the check would be looking at nothing")
	}
	if len(src.tolerant) == 0 {
		t.Fatal("no nil-tolerant collaborator was read from apps/flow-api; every field would fall out of scope and the check would pass vacuously")
	}
	bundles := src.Bundles()
	return src, bundles, src.Literals(bundles)
}

// requiredNames returns the required field names of one bundle.
func requiredNames(bundles []BundleType, key string) []string {
	for _, bundle := range bundles {
		if bundle.Key != key {
			continue
		}
		out := make([]string, 0, len(bundle.Required))
		for _, field := range bundle.Required {
			out = append(out, field.Name)
		}
		return out
	}
	return nil
}

// enforcingNames returns the names of the collaborators one bundle
// carries that fault the first time the package reaches them.
func enforcingNames(bundles []BundleType, key string) []string {
	for _, bundle := range bundles {
		if bundle.Key != key {
			continue
		}
		out := make([]string, 0, len(bundle.Enforcing))
		for _, field := range bundle.Enforcing {
			out = append(out, field.Name)
		}
		return out
	}
	return nil
}

// derived reports whether the bundle set holds the type.
func derived(bundles []BundleType, key string) bool {
	for _, bundle := range bundles {
		if bundle.Key == key {
			return true
		}
	}
	return false
}

// contains reports whether the list holds the name.
func contains(list []string, name string) bool {
	for _, item := range list {
		if strings.EqualFold(item, name) {
			return true
		}
	}
	return false
}
