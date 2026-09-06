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
// [bundleSource.Literals] states the limits, and the one worth knowing
// here is the scope: a literal written inline in a test and handed
// straight to one call is not held to this. Which of a handler's paths a
// caller drives is a run-time fact — a test that asserts on a rejected
// signature or a missing workspace returns before the field is reached —
// and no reading of the syntax separates that from a caller who drives
// the whole path. So the requirement lands where the consumer is not
// visible beside the literal: the deployed wiring, the shared test
// harness, and any helper that hands a bundle back to callers it cannot
// see.
func TestBundleWiringSetsEverySilentDependency(t *testing.T) {
	t.Parallel()

	src, bundles, literals := bundleScope(t)

	for _, lit := range literals {
		if !lit.Incomplete() {
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
	// asserted so the two cannot be confused.
	if len(bundles) < 10 {
		t.Errorf("only %d dependency bundles were derived from the tree; the derivation has stopped matching rather than the bundles having gone away",
			len(bundles))
	}
	if len(literals) < 10 {
		t.Errorf("only %d bundle literals were in scope; the literal walk has stopped matching",
			len(literals))
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
