package mcp

import (
	"go/ast"
	"go/parser"
	"go/token"
	"strings"
	"testing"
)

// legacyKindSuffix marks the eventbus kinds that are retained so historical
// rows continue to round-trip. The suffix is the signal rather than a list
// of names: a kind retired later carries it too, and is covered here without
// anybody remembering to come back.
const legacyKindSuffix = "Legacy"

// eventKindRef is one EventType field written in this package's sources.
//
// ident is the identifier naming the kind — the selector's final name for
// eventbus.TaskCommentAdded, the bare name for a local constant — and is
// empty when the value is not identifier-shaped, in which case only the
// count applies to it.
type eventKindRef struct {
	ident string
	pos   token.Pos
}

// eventKindRefs returns every EventType field set in a composite literal in
// one parsed file, in the order the walk reaches them.
//
// Any composite literal counts, not just the mutation record: the field
// names the kind an event is appended under wherever it is written, so a
// second record type gets the same treatment without a change here.
func eventKindRefs(file *ast.File) []eventKindRef {
	var out []eventKindRef
	ast.Inspect(file, func(n ast.Node) bool {
		lit, ok := n.(*ast.CompositeLit)
		if !ok {
			return true
		}
		for _, elt := range lit.Elts {
			kv, ok := elt.(*ast.KeyValueExpr)
			if !ok {
				continue
			}
			key, ok := kv.Key.(*ast.Ident)
			if !ok || key.Name != "EventType" {
				continue
			}
			out = append(out, eventKindRef{ident: eventKindIdent(kv.Value), pos: kv.Value.Pos()})
		}
		return true
	})
	return out
}

// eventKindIdent returns the identifier a kind expression names, or "" when
// the value is assembled some other way.
func eventKindIdent(expr ast.Expr) string {
	switch v := expr.(type) {
	case *ast.SelectorExpr:
		return v.Sel.Name
	case *ast.Ident:
		return v.Name
	default:
		return ""
	}
}

// TestMCPEventKindsAreNotLegacy fails when a record in this package names a
// legacy event kind.
//
// A legacy kind exists so events already in the table keep their meaning; it
// is not a name a new event can be filed under. The consumers downstream key
// on the current kind, so an event appended under the retired one is routed
// past all of them at once — the notification classifier answers silent, the
// timeline filter does not list it, and the stream tap resolves it to no
// kind. Each of those is a quiet nothing rather than an error, so the write
// succeeds and the change reaches nobody.
//
// The kindscan guard cannot see this: it resolves string literals written
// where a kind belongs, and a legacy kind referenced by its constant is
// correctly typed. The identifier is what carries the signal, so this reads
// the identifiers.
func TestMCPEventKindsAreNotLegacy(t *testing.T) {
	t.Parallel()

	files := mcpPackageSourceFiles(t)
	if len(files) == 0 {
		t.Fatal("no source files were read from the package; the check is looking at nothing")
	}

	fset := token.NewFileSet()
	found := 0
	for _, name := range files {
		file, err := parser.ParseFile(fset, name, nil, 0)
		if err != nil {
			t.Fatalf("parse %s: %v", name, err)
		}
		for _, ref := range eventKindRefs(file) {
			found++
			if !strings.HasSuffix(ref.ident, legacyKindSuffix) {
				continue
			}
			t.Errorf("%s: EventType is %s, a kind kept only so historical rows round-trip. "+
				"An event appended under it reaches no notification, no timeline and no stream. "+
				"Name the current kind for this change — the one the REST handler for the same "+
				"change emits", fset.Position(ref.pos), ref.ident)
		}
	}

	if found == 0 {
		t.Fatal("no EventType values found; the guard is passing because it is looking at nothing")
	}
	t.Logf("read %d EventType values across %d source files", found, len(files))
}

// TestLegacyEventKindScanSeesALegacyKind is the positive control: it proves
// the walk reports what it is meant to report, rather than passing because
// it matches nothing.
func TestLegacyEventKindScanSeesALegacyKind(t *testing.T) {
	t.Parallel()

	const src = `package p

func current() {
	record(mutation{EventType: eventbus.TaskCommentAdded})
}

func retired() {
	record(mutation{EventType: eventbus.CommentAddedLegacy})
}

func local() {
	record(mutation{EventType: SomethingRemovedLegacy})
}

func assembled(kind eventbus.Kind) {
	record(mutation{EventType: kind})
}
`

	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "sample.go", src, 0)
	if err != nil {
		t.Fatalf("parse sample: %v", err)
	}

	refs := eventKindRefs(file)
	if len(refs) != 4 {
		t.Fatalf("scan read %d EventType values, want 4", len(refs))
	}

	var legacy []int
	for _, ref := range refs {
		if strings.HasSuffix(ref.ident, legacyKindSuffix) {
			legacy = append(legacy, fset.Position(ref.pos).Line)
		}
	}

	// The selector on the retired kind and the bare local constant; the
	// current kind and the value passed in are left alone.
	want := []int{8, 12}
	if len(legacy) != len(want) {
		t.Fatalf("scan reported %d legacy kinds at lines %v, want %d at %v", len(legacy), legacy, len(want), want)
	}
	for i := range legacy {
		if legacy[i] != want[i] {
			t.Errorf("legacy kind %d is at line %d, want %d", i, legacy[i], want[i])
		}
	}
}
