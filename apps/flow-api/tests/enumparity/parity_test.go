package enumparity

import (
	"path/filepath"
	"strings"
	"testing"
)

// TestInputEnumConstraintsAgree refuses a wire field whose accepted values
// one operation states and another leaves open.
//
// The two operations write the same column. When only one of them says
// what it takes, the other is guarded by nothing above the storage layer:
// the caller gets a write failure instead of a named field, the OpenAPI
// document tells clients the field is any string, and whatever arrives
// reaches every rule downstream as a value none of them classifies.
//
// Nothing is listed here. The inputs come out of the handler trees and the
// pairing out of their type names, so an operation added tomorrow is
// checked without anyone registering it.
func TestInputEnumConstraintsAgree(t *testing.T) {
	t.Parallel()

	for _, d := range Divergences(treeFields(t)) {
		with := make([]string, 0, len(d.With))
		for _, f := range d.With {
			with = append(with, f.Owner+" at "+f.Location())
		}
		for _, f := range d.Without {
			t.Errorf("%s: %s leaves %s.%s open, while %s states its values as %s. "+
				"Both write the same %s field, so the open one is refused by nothing above "+
				"storage: the caller gets a write failure instead of a named field, and a "+
				"value nothing recognises reaches every rule that reads it. Give this field "+
				"an enum tag — the same set, or the narrower one this operation actually "+
				"takes; there is no exemption, because an unconstrained field is not a "+
				"narrower set",
				f.Location(), f.Owner, f.Section, f.Name,
				strings.Join(with, " and "), strings.Join(d.Sets(), " / "), d.Resource)
		}
	}
}

// TestDerivationStillMatches is the positive control, and it runs on every
// invocation because it reads a source stated here rather than the tree.
//
// It holds the derivation against the shapes it has to tell apart: the
// asymmetry it exists to catch, and four neighbours that look like one and
// are not. A check that flagged those would be answered by adding
// exemptions, which is the outcome this is written to avoid.
func TestDerivationStillMatches(t *testing.T) {
	t.Parallel()

	const src = `package probe

// The pair the check exists for: one resource, two write operations, and
// only one of them says what the field takes.
type CreateWidgetInput struct {
	WsID string ` + "`" + `path:"wsId"` + "`" + `
	Body struct {
		Mode   string ` + "`" + `json:"mode" enum:"draft,live"` + "`" + `
		Status string ` + "`" + `json:"status" enum:"new,open,done"` + "`" + `
		Reason string ` + "`" + `json:"reason" enum:"manual,automatic"` + "`" + `
		Note   string ` + "`" + `json:"note" maxLength:"2000"` + "`" + `
		Rank   int64  ` + "`" + `json:"rank"` + "`" + `
		Filter struct {
			Mode string ` + "`" + `json:"mode"` + "`" + `
		} ` + "`" + `json:"filter"` + "`" + `
	}
}

// PatchWidgetBody is named rather than inline, which is how half the
// inputs in this repository spell a body.
type PatchWidgetBody struct {
	Mode   *string ` + "`" + `json:"mode"` + "`" + `
	Status *string ` + "`" + `json:"status" enum:"open,done"` + "`" + `
	Note   *string ` + "`" + `json:"note" maxLength:"2000"` + "`" + `
	Filter *struct {
		Mode *string ` + "`" + `json:"mode"` + "`" + `
	} ` + "`" + `json:"filter"` + "`" + `
}

type PatchWidgetInput struct {
	ID   string ` + "`" + `path:"id"` + "`" + `
	Mode string ` + "`" + `query:"mode"` + "`" + `
	Body PatchWidgetBody
}

// TransitionWidgetInput is not a write verb this pairs on. Its reason is a
// free-text note that happens to share a name with a categorical one.
type TransitionWidgetInput struct {
	ID   string ` + "`" + `path:"id"` + "`" + `
	Body struct {
		Reason string ` + "`" + `json:"reason" maxLength:"2000"` + "`" + `
	}
}

// A different resource in the same package, whose mode is its own.
type CreateGadgetInput struct {
	Body struct {
		Mode string ` + "`" + `json:"mode"` + "`" + `
	}
}
`

	fields, err := ParsePackage("probe", map[string]string{"probe.go": src})
	if err != nil {
		t.Fatalf("parse the control source: %v", err)
	}

	// The named body type has to be resolved, or the patch side of the
	// pair is a marker field with nothing behind it and the check passes
	// by seeing one operation instead of two.
	if !hasField(fields, "PatchWidgetInput", "body", "mode") {
		t.Fatal("the fields of a body declared as a separate type were not read; " +
			"half the inputs in this repository are written that way, and the check " +
			"would compare one side of every one of them")
	}
	if hasField(fields, "CreateWidgetInput", "body", "rank") {
		t.Error("a non-string field was read; an enum constraint describes strings, " +
			"so pairing one would report a divergence nothing could fix")
	}

	divergences := Divergences(fields)
	if len(divergences) != 1 {
		for _, d := range divergences {
			t.Logf("flagged %s.%s on %s", d.Section, d.Name, d.Resource)
		}
		t.Fatalf("flagged %d fields, want exactly the one asymmetry", len(divergences))
	}
	got := divergences[0]
	if got.Resource != "Widget" || got.Section != "body" || got.Name != "mode" {
		t.Fatalf("flagged %s %s.%s, want Widget body.mode", got.Resource, got.Section, got.Name)
	}
	if len(got.With) != 1 || got.With[0].Owner != "CreateWidgetInput" {
		t.Errorf("the constrained side is %+v, want CreateWidgetInput", got.With)
	}
	if len(got.Without) != 1 || got.Without[0].Owner != "PatchWidgetInput" {
		t.Errorf("the open side is %+v, want PatchWidgetInput", got.Without)
	}

	// The neighbours are the half that decides whether this check is worth
	// running. Each of them is a shape the tree actually contains.
	for _, neighbour := range []struct {
		why      string
		resource string
		section  string
		name     string
	}{
		{"two operations may accept different subsets of a column, and both of these state theirs",
			"Widget", "body", "status"},
		{"a free-text field on an operation this does not pair is not the categorical field it shares a name with",
			"Widget", "body", "reason"},
		{"a member of a nested object is not the top-level field spelled the same",
			"Widget", "body", "filter.mode"},
		{"a query parameter is not the body field spelled the same",
			"Widget", "query", "mode"},
		{"another resource's field is its own",
			"Gadget", "body", "mode"},
	} {
		for _, d := range divergences {
			if d.Resource == neighbour.resource && d.Section == neighbour.section && d.Name == neighbour.name {
				t.Errorf("flagged %s %s.%s: %s", d.Resource, d.Section, d.Name, neighbour.why)
			}
		}
	}
}

// hasField reports whether the derivation produced one named field of one
// input.
func hasField(fields []Field, owner, section, name string) bool {
	for _, f := range fields {
		if f.Owner == owner && f.Section == section && f.Name == name {
			return true
		}
	}
	return false
}

// treeFields reads the handler trees and proves each root contributed
// something.
//
// This check reads files by path. A root that moved, a suffix that stopped
// matching, a body type that left the package — each of them empties the
// scanned set, and an empty set passes every comparison it is asked to
// make.
func treeFields(t *testing.T) []Field {
	t.Helper()
	root, err := RepoRoot()
	if err != nil {
		t.Fatalf("locate repository root: %v", err)
	}

	var all []Field
	for _, rel := range Roots {
		fields, ferr := Fields(root, []string{rel})
		if ferr != nil {
			t.Fatalf("read %s: %v", rel, ferr)
		}
		if len(fields) == 0 {
			t.Fatalf("no input field was read under %s; the handler tree moved or the "+
				"derivation stopped matching, rather than the inputs having gone away", rel)
		}
		for _, f := range fields {
			if !strings.HasPrefix(f.Path, filepath.ToSlash(rel)) {
				t.Fatalf("field %s.%s was read from %s, outside %s", f.Owner, f.Name, f.Path, rel)
			}
		}
		all = append(all, fields...)
	}

	if len(Comparisons(all)) == 0 {
		t.Fatal("no wire field is described by two of the operations that write its " +
			"resource, so nothing was compared; the pairing has stopped matching rather " +
			"than the create/update pairs having gone away")
	}
	return all
}
