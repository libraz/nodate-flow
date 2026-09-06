package precondition

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// The checks in this file are the control for what the stream-contract
// rule reads on each side and what it refuses to read.
//
// Both halves decide whether the rule is worth having. Read too loosely
// on the frontend side, every kind is admitted by the switch label that
// handles it and the rule passes on a union that declares nothing; read
// too tightly on the Go side, it reports kinds that are declared in a
// spelling it does not follow — a constant in a second file of the
// package, or one that carries its type from the spec above — and a rule
// that reports correct code is one that gets switched off. So the fixture
// below puts each shape beside its opposite and pins which is which:
//
//   - a Go constant whose wire string the union admits is silent;
//   - a Go constant the union does not admit is reported, even where the
//     switch below the union has a case label quoting it, because a case
//     label is not membership;
//   - a Go constant in a second file of the package is read, and one in a
//     test file is not;
//   - a constant that carries its type from the spec above it is read;
//   - a wire string quoted in a Go doc comment declares nothing, and one
//     quoted in a comment inside the union admits nothing;
//   - a union member with no Go constant is reported unless an entry
//     gives the reason, and an entry is reported when the member gains a
//     Go constant, when the union stops admitting it, or when it carries
//     no reason at all.

// TestStreamContractReadsTheUnionAndNotTheSwitch drives the fixture and
// pins which kinds come out reported, name by name.
func TestStreamContractReadsTheUnionAndNotTheSwitch(t *testing.T) {
	t.Parallel()

	scope, entries := parseControlContract(t)
	violations := scope.Violations(entries)

	var got []string
	for _, violation := range violations {
		got = append(got, violation.Wire)
	}
	sort.Strings(got)

	want := []string{
		"fixture.blank",
		"fixture.carried",
		"fixture.departed",
		"fixture.neighbour",
		"fixture.stale",
		"fixture.union_only",
	}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("reported kinds = %v; want %v.\n"+
			"  Reported: two Go kinds the union does not admit — one of them quoted by a switch label, one "+
			"carrying its type from the spec above — a union member with no Go kind and no entry, an entry "+
			"whose member gained a Go kind, an entry the union no longer admits, and an entry with no reason.\n"+
			"  Not reported: a kind declared on both sides, a member an entry explains, a wire string named "+
			"only in a Go doc comment or in a comment inside the union, and a kind declared only by a test file.",
			got, want)
	}
}

// TestStreamContractFailureStatesTheConsequence pins the words the two
// reported shapes come out in.
//
// The wording is the load-bearing part of the defect direction. What
// makes a missing union member worth a failing test is not that two lists
// differ — it is what the difference costs a session, and a message that
// says only "not in sync" leaves the reader to guess that the browser
// ignores the kind, which it does not. So the text is compared whole,
// rather than rebuilt here from the pieces that produced it.
func TestStreamContractFailureStatesTheConsequence(t *testing.T) {
	t.Parallel()

	scope, entries := parseControlContract(t)
	byWire := map[string]string{}
	for _, violation := range scope.Violations(entries) {
		byWire[violation.Wire] = violation.Message
	}

	wantNeighbour := "apps/flow-api/internal/stream/tailer.go:5 declares KindNeighbour " +
		"(\"fixture.neighbour\"), which the StreamKind union in " +
		"apps/flow-web/src/features/realtime/event-to-keys.ts:6 does not admit.\n" +
		"  The browser does not ignore the kind: keysForEvent switches on the union, has no case for it, " +
		"and returns undefined, so the SSE reader throws where it iterates the result and the reconnect " +
		"loop discards the throw. Every event of this kind costs the connection and any frame buffered " +
		"behind it, while the connection stays healthy enough that the polling fallback never engages.\n" +
		"  Add \"fixture.neighbour\" to the union and give keysForEvent the keys it invalidates. There is " +
		"no exemption list for this direction: a kind the server can send and the browser cannot accept " +
		"is a defect, not a choice."
	if got := byWire["fixture.neighbour"]; got != wantNeighbour {
		t.Errorf("the message for a Go kind the union does not admit is:\n%s\n\nwant:\n%s", got, wantNeighbour)
	}

	wantStale := "unionOnlyKinds lists \"fixture.stale\" as having no Go counterpart, reason " +
		"\"the fixture's union anticipates a family the Go side does not carry\", but " +
		"apps/flow-api/internal/stream/event.go:13 declares it as KindStale.\n" +
		"  Drop the entry: a list that keeps an entry after the kind gains a Go constant records what was " +
		"once true and checks nothing."
	if got := byWire["fixture.stale"]; got != wantStale {
		t.Errorf("the message for an entry whose member gained a Go kind is:\n%s\n\nwant:\n%s", got, wantStale)
	}
}

// TestStreamContractReadsBothDeclarations asserts the fixture is being
// read the way the rule assumes, so a failure above is read as a
// judgement rather than as a parse that found nothing.
func TestStreamContractReadsBothDeclarations(t *testing.T) {
	t.Parallel()

	scope, _ := parseControlContract(t)

	var kinds []string
	for _, kind := range scope.kinds {
		kinds = append(kinds, kind.Name)
	}
	wantKinds := []string{"KindBoth", "KindCarried", "KindNeighbour", "KindStale"}
	if strings.Join(kinds, ",") != strings.Join(wantKinds, ",") {
		t.Errorf("Go kinds = %v; want %v.\n"+
			"  The fixture also holds an untyped constant in a block of its own, a wire string in a doc "+
			"comment, and a constant declared in a test file; none of the three is a kind the server can send.",
			kinds, wantKinds)
	}

	var members []string
	for _, member := range scope.members {
		members = append(members, member.Wire)
	}
	wantMembers := []string{
		"fixture.both", "fixture.stale", "fixture.union_only", "fixture.explained", "fixture.blank",
	}
	if strings.Join(members, ",") != strings.Join(wantMembers, ",") {
		t.Errorf("union members = %v; want %v.\n"+
			"  The fixture quotes three more kinds in the file: one above the declaration, one in a comment "+
			"inside it, and one as a switch label below it. None of the three is a member.",
			members, wantMembers)
	}

	if scope.residue != "" {
		t.Errorf("the union read left residue %q; the fixture declares a union of string literals and nothing else",
			scope.residue)
	}
	if !scope.kindTypeDeclared {
		t.Error("the fixture's Kind type was not derived; the constants are read as being of a type that does not exist")
	}
	if scope.goFiles != 2 {
		t.Errorf("%d Go files were read from the fixture package; want 2, the two non-test files", scope.goFiles)
	}
}

// parseControlContract lays out a minimal repository holding one Go
// package that declares the wire format and one frontend file that
// declares the union and consumes it, then parses it the way the real
// check does.
func parseControlContract(t *testing.T) (*streamContractScope, []UnionOnlyKind) {
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

	write(streamKindPackageDir+"/event.go", `package stream

// Kind is the closed set of families the fixture stream emits.
type Kind string

const (
	// KindBoth is declared on both sides. Naming "fixture.union_only" in
	// a doc comment declares nothing.
	KindBoth Kind = "fixture.both"

	// KindStale is admitted by the union and also listed as having no Go
	// counterpart.
	KindStale Kind = "fixture.stale"

	// KindCarried leaves the type off and carries it from the spec above.
	KindCarried = "fixture.carried"
)

// heartbeatKey is not a kind: its block names no type to carry.
const heartbeatKey = "fixture.commented"
`)

	write(streamKindPackageDir+"/tailer.go", `package stream

const (
	// KindNeighbour is declared in a second file of the same package.
	KindNeighbour Kind = "fixture.neighbour"
)
`)

	write(streamKindPackageDir+"/sse_test.go", `package stream

// KindTestOnly is a fixture's own kind. Nothing the server sends.
const KindTestOnly Kind = "fixture.test_only"
`)

	write(streamUnionFile, `/**
 * The union the browser parses the wire format against. A kind quoted
 * in this comment - 'fixture.neighbour' - admits nothing.
 */

export type StreamKind =
  | 'fixture.both'
  | 'fixture.stale'
  // A comment inside the declaration naming 'fixture.commented' is prose.
  | 'fixture.union_only'
  | 'fixture.explained'
  | 'fixture.blank';

export interface StreamEvent {
  kind: StreamKind;
}

export function keysForEvent(evt: StreamEvent): readonly string[] {
  switch (evt.kind) {
    case 'fixture.both':
      return ['both'];
    case 'fixture.neighbour':
      return ['neighbour'];
    case 'fixture.carried':
      return ['carried'];
    default:
      return [];
  }
}
`)

	scope, err := parseStreamContract(root)
	if err != nil {
		t.Fatalf("parse control tree: %v", err)
	}

	entries := []UnionOnlyKind{
		{
			Wire:   "fixture.explained",
			Reason: "the fixture's union anticipates a family the Go side does not carry",
		},
		{
			Wire:   "fixture.stale",
			Reason: "the fixture's union anticipates a family the Go side does not carry",
		},
		{
			Wire:   "fixture.departed",
			Reason: "the member this entry was written for is no longer in the union",
		},
		{
			Wire:   "fixture.blank",
			Reason: "   ",
		},
	}
	return scope, entries
}
