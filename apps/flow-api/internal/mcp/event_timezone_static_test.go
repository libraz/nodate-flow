package mcp

import (
	"go/ast"
	"go/parser"
	"go/token"
	"slices"
	"testing"
)

// timezoneRef is one Timezone field written in a composite literal in this
// package's sources.
//
// literal is true when the value is a string constant written in place, which
// is the shape that pins a row to a zone chosen at authoring time; a zone that
// was resolved arrives as a call or a variable and is not literal.
type timezoneRef struct {
	literal bool
	pos     token.Pos
}

// timezoneRefs returns every Timezone field set in a composite literal in one
// parsed file, in the order the walk reaches them.
//
// Any composite literal counts rather than a named set of parameter structs:
// the field carries the zone a row is read on wherever it is written, so a
// second query's parameters get the same treatment without a change here.
func timezoneRefs(file *ast.File) []timezoneRef {
	var out []timezoneRef
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
			if !ok || key.Name != "Timezone" {
				continue
			}
			basic, isBasic := kv.Value.(*ast.BasicLit)
			out = append(out, timezoneRef{
				literal: isBasic && basic.Kind == token.STRING,
				pos:     kv.Value.Pos(),
			})
		}
		return true
	})
	return out
}

// TestMCPEventTimezonesAreResolved fails when a Timezone field in this package
// is set to a string constant.
//
// The zone stored on a calendar row is the one its day is read in, so it
// belongs to the workspace the row is written for and has to be resolved for
// it. A constant written in place is the same zone for every workspace: the
// event is stored correct in one offset and displaced in all the others, with
// nothing to fail on, because the row is well-formed either way.
//
// The shape is what carries the signal, so this reads the shape. A resolved
// zone reaches the field as a call or a variable; only a constant can be
// wrong for the workspace before the request is even read.
func TestMCPEventTimezonesAreResolved(t *testing.T) {
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
		for _, ref := range timezoneRefs(file) {
			found++
			if !ref.literal {
				continue
			}
			t.Errorf("%s: Timezone is a string constant. The zone must be resolved for the "+
				"workspace rather than assumed, because a day built in UTC is a day only at "+
				"offset zero — every other workspace reads the row off its grid by that offset",
				fset.Position(ref.pos))
		}
	}

	if found == 0 {
		t.Fatal("no Timezone fields found; the guard is passing because it is looking at nothing")
	}
	t.Logf("read %d Timezone fields across %d source files", found, len(files))
}

// TestTimezoneScanSeesALiteralZone is the positive control: it proves the walk
// reports what it is meant to report, rather than passing because it matches
// nothing.
func TestTimezoneScanSeesALiteralZone(t *testing.T) {
	t.Parallel()

	const src = `package p

func assumed() {
	create(params{Timezone: "UTC"})
}

func resolved(ctx context.Context) {
	create(params{Timezone: resolveUserTimezone(ctx).Name()})
}

func carried(zone string) {
	create(params{Timezone: zone})
}
`

	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "sample.go", src, 0)
	if err != nil {
		t.Fatalf("parse sample: %v", err)
	}

	refs := timezoneRefs(file)
	if len(refs) != 3 {
		t.Fatalf("scan read %d Timezone fields, want 3", len(refs))
	}

	var literals []int
	for _, ref := range refs {
		if ref.literal {
			literals = append(literals, fset.Position(ref.pos).Line)
		}
	}

	// The constant only; the resolved call and the value passed in are left
	// alone.
	want := []int{4}
	if !slices.Equal(literals, want) {
		t.Errorf("scan reported literal zones at lines %v, want %v", literals, want)
	}
}
