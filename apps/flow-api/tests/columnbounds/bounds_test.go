package columnbounds

import (
	"fmt"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// TestDeclaredBoundsFitTheirColumns refuses an input that accepts a string
// longer than the column it is stored in.
//
// The bound is what the API tells a caller, through the OpenAPI document
// and through the tool schema, that it will take. When the column is
// narrower, everything between the two widths validates and then fails to
// store: the caller gets a server error naming nothing, for a request the
// contract said was well formed.
//
// Nothing is listed here. The bounds come out of the handler trees and the
// tool schemas, the widths out of the generated schema, and the mapping
// between them out of the resource each surface names and the tables its
// own statements write.
func TestDeclaredBoundsFitTheirColumns(t *testing.T) {
	t.Parallel()

	tree := readTree(t)
	for _, r := range Overflows(tree.resolutions) {
		t.Errorf("%s: %s accepts %d characters, and %s holds %d %s. "+
			"Everything between the two validates and is then refused by storage, so the "+
			"caller gets a server error for a request this API told them was acceptable. "+
			"Either widen the column or lower the bound to what it holds",
			r.Location(), r.Describe(), r.Max, r.Column.Qualified(), r.Column.Capacity, r.Column.Units)
	}
}

// TestDeclaredBoundsAgreeAcrossSurfaces refuses two declarations of one
// field that state different lengths.
//
// This half asks nothing of the schema and needs no column. One column has
// one width, so of two surfaces stating different bounds for the field that
// writes it, at most one can be right: either the create surface accepts
// what the update surface will not store, or the tool accepts what the REST
// operation rejects and the same value takes two different answers
// depending on how it arrived.
func TestDeclaredBoundsAgreeAcrossSurfaces(t *testing.T) {
	t.Parallel()

	tree := readTree(t)
	for _, p := range Disagreements(tree.resolutions) {
		t.Errorf("%s: %s accepts %d characters, while %s at %s accepts %d. "+
			"Both write the %s resource's %s, and one column has one width, so at most one "+
			"of these is it. Whichever is right, the other lets a caller through a door the "+
			"same value is refused at on the other surface",
			p.A.Location(), p.A.Describe(), p.A.Max,
			p.B.Describe(), p.B.Location(), p.B.Max,
			p.A.Resource, p.A.Name)
	}
}

// TestUnresolvedBoundsAreVisible prints the bounds no column was found for.
//
// They are not failures. A search term, a bearer token, a free-form
// instruction and a password are all declared like a field and none is
// stored under the name it arrives as, so requiring an exemption on each
// would mark the majority of the bounds in this repository — and a marker
// the majority carries is one nobody reads. The gap is printed instead, so
// the reach of the overflow check is something someone can look at rather
// than assume.
//
// The run this has to be readable on is the one where everything passes,
// because that is the run where an unnoticed gap looks like coverage. A
// passing package's output is shown only under -v, so the target that runs
// this guard passes -v, and nothing else here writes at that volume.
func TestUnresolvedBoundsAreVisible(t *testing.T) {
	t.Parallel()

	tree := readTree(t)
	placed, unplaced := Placed(tree.resolutions)

	var report strings.Builder
	fmt.Fprintf(&report, "%d declared bounds: %d placed on a column, %d unresolved; "+
		"%d pairs compared across surfaces\n",
		len(tree.resolutions), len(placed), len(unplaced), len(Pairs(tree.resolutions)))
	fmt.Fprintf(&report, "unresolved (no column found; the overflow check does not reach these):\n")
	lines := make([]string, 0, len(unplaced))
	for _, r := range unplaced {
		lines = append(lines, fmt.Sprintf("  %s maxLength=%d at %s", r.Describe(), r.Max, r.Location()))
	}
	sort.Strings(lines)
	fmt.Fprintln(&report, strings.Join(lines, "\n"))
	fmt.Print(report.String())
}

// TestDerivationStillMatches is the positive control, and it runs on every
// invocation because it reads sources stated here rather than the tree.
//
// A guard that has stopped being able to see anything reads exactly like a
// guard with nothing to report. So the shapes this exists to catch are
// driven through the same functions the tree goes through: a bound wider
// than its column, a bound that fits, and two surfaces that disagree —
// alongside the neighbours that look like each and are not.
func TestDerivationStillMatches(t *testing.T) {
	t.Parallel()

	const schemaSrc = `
CREATE TABLE widgets (
  id INT UNSIGNED AUTO_INCREMENT PRIMARY KEY COMMENT 'Internal PK, never exposed',
  title VARCHAR(255) NOT NULL COMMENT 'Widget title (255, note the -- and (parens) a comment may carry)',
  icon_url VARCHAR(2048) NULL COMMENT 'Icon image URL',
  body TEXT NULL COMMENT 'Free text',
  state ENUM('open','done') NOT NULL DEFAULT 'open' COMMENT 'Lifecycle',
  UNIQUE KEY uniq_widgets_id (id)
) ENGINE=InnoDB;

CREATE TABLE widget_notes (
  id INT UNSIGNED AUTO_INCREMENT PRIMARY KEY,
  title VARCHAR(80) NOT NULL COMMENT 'Note title'
) ENGINE=InnoDB;

CREATE TABLE gadgets (
  id INT UNSIGNED AUTO_INCREMENT PRIMARY KEY,
  title VARCHAR(500) NOT NULL COMMENT 'Gadget title'
) ENGINE=InnoDB;

-- Two tables a surface could call a comment, holding different widths.
CREATE TABLE widget_comments (
  id INT UNSIGNED AUTO_INCREMENT PRIMARY KEY,
  body VARCHAR(2000) NOT NULL COMMENT 'Comment on a widget'
) ENGINE=InnoDB;

CREATE TABLE comments (
  id INT UNSIGNED AUTO_INCREMENT PRIMARY KEY,
  body VARCHAR(20000) NOT NULL COMMENT 'Comment on something else entirely'
) ENGINE=InnoDB;
`

	const handlerSrc = `package probe

// The pair the overflow half exists for: a create bound wider than the
// column, and a fitting bound beside it.
type CreateWidgetInput struct {
	WsID string ` + "`" + `path:"wsId" maxLength:"36"` + "`" + `
	Body struct {
		Title   string ` + "`" + `json:"title" minLength:"1" maxLength:"500"` + "`" + `
		IconURL string ` + "`" + `json:"iconUrl,omitempty" maxLength:"2048"` + "`" + `
		Body    string ` + "`" + `json:"body,omitempty" maxLength:"50000"` + "`" + `
		Tags    []string ` + "`" + `json:"tags,omitempty" maxLength:"40"` + "`" + `
		Nested  struct {
			Title string ` + "`" + `json:"title" maxLength:"9999"` + "`" + `
		} ` + "`" + `json:"nested"` + "`" + `
	}
}

// PatchWidgetBody is named rather than inline, which is how half the inputs
// in this repository spell a body.
type PatchWidgetBody struct {
	Title   *string ` + "`" + `json:"title,omitempty" minLength:"1" maxLength:"200"` + "`" + `
	IconURL *string ` + "`" + `json:"iconUrl,omitempty" maxLength:"2048"` + "`" + `
}

type PatchWidgetInput struct {
	ID   string ` + "`" + `path:"id"` + "`" + `
	Q    string ` + "`" + `query:"q" maxLength:"4000"` + "`" + `
	Body PatchWidgetBody
}

// A read operation. Its term is not stored under the name it arrives as.
type SearchWidgetsInput struct {
	Q string ` + "`" + `query:"q" minLength:"1" maxLength:"4000"` + "`" + `
}

// A resource of its own, whose title is its own column.
type CreateGadgetInput struct {
	Body struct {
		Title string ` + "`" + `json:"title" maxLength:"500"` + "`" + `
	}
}

// A comment on a widget. The tool called add_comment writes a different
// table, so the two are not the same field however alike the nouns read.
type CreateCommentInput struct {
	Body struct {
		Body string ` + "`" + `json:"body" minLength:"1" maxLength:"2000"` + "`" + `
	}
}
`

	const toolsSrc = `package probe

func registerTools(h *Handler) {
	h.register(auth.FloorProjectEditor, tool{
		name:        "create_widget",
		inputSchema: objectSchema(map[string]any{
			"title":   stringSchema("Widget title.", Constraints{MinLength: intPtr(1), MaxLength: intPtr(500)}),
			"iconUrl": stringSchema("Icon URL.", Constraints{MaxLength: intPtr(2048)}),
			"parts": map[string]any{
				"type":  "array",
				"items": objectSchema(map[string]any{
					"title": stringSchema("Part title.", Constraints{MinLength: intPtr(1), MaxLength: intPtr(500)}),
				}, []string{"title"}),
			},
		}, []string{"title"}),
	})
	h.register(auth.FloorProjectEditor, tool{
		name:        "search_widgets",
		inputSchema: objectSchema(map[string]any{
			"query": stringSchema("Search term.", Constraints{MaxLength: intPtr(4000)}),
		}, nil),
	})
	h.register(auth.FloorProjectEditor, tool{
		name:        "add_comment",
		inputSchema: objectSchema(map[string]any{
			"body": stringSchema("Comment body.", Constraints{MinLength: intPtr(1), MaxLength: intPtr(20000)}),
		}, []string{"body"}),
	})
}
`

	schema := ParseSchema(schemaSrc)
	if got := schema.Count(); got == 0 {
		t.Fatal("the control schema produced no string columns; the schema reader stopped " +
			"matching, and every overflow comparison it feeds is against nothing")
	}
	if c, ok := schema.Column("widgets", "title"); !ok || c.Capacity != 255 || c.Units != "characters" {
		t.Fatalf("widgets.title read as %+v (found=%v), want 255 characters; a column comment "+
			"carrying parentheses and a comment marker must not be read as structure", c, ok)
	}
	if c, ok := schema.Column("widgets", "body"); !ok || c.Units != "bytes" {
		t.Fatalf("widgets.body read as %+v (found=%v), want a byte capacity", c, ok)
	}
	if _, ok := schema.Column("widgets", "state"); ok {
		t.Error("an ENUM was read as a bounded string; what it accepts is a value set, and " +
			"comparing a length against it would report something nothing could fix")
	}

	handler, err := ParseHandlerPackage("apps/probe-api/internal/http/handlers/widgets", map[string]string{"probe.go": handlerSrc})
	if err != nil {
		t.Fatalf("parse the control handler source: %v", err)
	}
	tools, err := ParseTools("probe/tools.go", toolsSrc)
	if err != nil {
		t.Fatalf("parse the control tool source: %v", err)
	}
	if !has(handler, "PatchWidgetInput", "body", "title") {
		t.Fatal("the fields of a body declared as a separate type were not read; half the " +
			"inputs in this repository are written that way, and every one of them would be " +
			"compared on one side only")
	}
	if has(handler, "CreateWidgetInput", "body", "tags") {
		t.Error("a bound on a string slice was read as a field's length; it does not land in " +
			"one column, so placing it would compare a width against something else")
	}
	if !has(tools, "create_widget", "body", "iconUrl") {
		t.Fatal("the tool schema properties were not read; the tool half of every comparison " +
			"would be empty")
	}
	if !has(tools, "create_widget", "body", "parts.title") {
		t.Error("a property nested under an array's items was not read")
	}

	decls := append(append([]Declaration(nil), handler...), tools...)
	writeSets := map[string]map[string]bool{
		"widgets":  {"widgets": true, "widget_notes": true, "widget_comments": true},
		"gadgets":  {"gadgets": true},
		"comments": {"comments": true},
	}
	resolutions := ResolveAll(decls, schema, writeSets)
	placed, unplaced := Placed(resolutions)
	if len(placed) == 0 {
		t.Fatal("nothing resolved to a column, so the overflow half compared nothing")
	}

	// The overflow half: one bound over its column, and nothing else.
	overflowing := map[string]bool{}
	for _, r := range Overflows(resolutions) {
		overflowing[r.Describe()+" -> "+r.Column.Qualified()] = true
	}
	for _, want := range []string{
		"REST CreateWidgetInput body.title -> widgets.title",
		"MCP create_widget body.title -> widgets.title",
	} {
		if !overflowing[want] {
			t.Errorf("did not flag %q: a bound wider than its column is the state this exists "+
				"to catch, and it was not caught", want)
		}
		delete(overflowing, want)
	}
	for extra := range overflowing {
		t.Errorf("flagged %q, which fits its column", extra)
	}

	// The neighbours. Each is a shape the tree actually contains, and each
	// would be answered by adding an exemption if it were flagged.
	for _, neighbour := range []struct {
		why   string
		owner string
		name  string
	}{
		{"a bound equal to the column's width is the width", "CreateWidgetInput", "iconUrl"},
		{"a bound under a text column's byte capacity fits it", "CreateWidgetInput", "body"},
		{"a member of a nested object is not a column of the resource the input is named after",
			"CreateWidgetInput", "nested.title"},
		{"a query parameter selects rows rather than supplying a value stored in one",
			"PatchWidgetInput", "q"},
		{"a read operation's term is not stored under the name it arrives as",
			"SearchWidgetsInput", "q"},
		{"another resource's column is its own", "CreateGadgetInput", "title"},
		{"a tool whose name does not spell a write on a resource states no column",
			"search_widgets", "query"},
		{"a comment on a widget is not the comment a tool of that name writes",
			"CreateCommentInput", "body"},
	} {
		for _, r := range Overflows(resolutions) {
			if r.Owner == neighbour.owner && r.Name == neighbour.name {
				t.Errorf("flagged %s %s: %s", r.Owner, r.Name, neighbour.why)
			}
		}
	}
	// The nested title has no column of its own to be placed on; being
	// unresolved is what keeps it out, so say that rather than only that it
	// was not flagged.
	if !unplacedHas(unplaced, "CreateWidgetInput", "nested.title") {
		t.Error("a member of a nested object resolved to a column; the resource the input " +
			"names says nothing about which table that member lives in")
	}
	if !unplacedHas(unplaced, "SearchWidgetsInput", "q") {
		t.Error("a read operation's term resolved to a column")
	}

	// The disagreement half, which needs no column of its own.
	var disagreed []string
	for _, p := range Disagreements(resolutions) {
		disagreed = append(disagreed, fmt.Sprintf("%s(%d) vs %s(%d)", p.A.Owner, p.A.Max, p.B.Owner, p.B.Max))
	}
	sort.Strings(disagreed)
	want := []string{
		"CreateWidgetInput(500) vs PatchWidgetInput(200)",
		"PatchWidgetInput(200) vs create_widget(500)",
	}
	if strings.Join(disagreed, " | ") != strings.Join(want, " | ") {
		t.Fatalf("disagreements were %v, want %v; the patch bound on one field differs from "+
			"both the create bound and the tool's, and the widget's comment is a different "+
			"column from the one the tool of that name writes", disagreed, want)
	}
	if len(Pairs(resolutions)) < 2 {
		t.Fatal("fewer than two fields were compared across surfaces, so the agreeing ones " +
			"prove nothing about the pairing still matching")
	}
}

// tree is the derivation run over the repository, read once per test.
type tree struct {
	resolutions []Resolution
}

// readTree runs the derivation over the committed sources and proves every
// input set it rests on is non-empty.
//
// This check reads files by path. A handler tree that moved, a tool
// convention that shifted, a schema dump that stopped being generated —
// each of them empties one of these sets, and an empty set passes every
// comparison it is asked to make.
func readTree(t *testing.T) tree {
	t.Helper()
	root, err := RepoRoot()
	if err != nil {
		t.Fatalf("locate repository root: %v", err)
	}

	dump, err := ReadSchema(root)
	if err != nil {
		t.Fatalf("read sql/schema.sql: %v", err)
	}
	schema := ParseSchema(dump)
	if schema.Count() == 0 {
		t.Fatal("sql/schema.sql declared no string-holding column; the dump or the reader " +
			"changed shape, rather than the schema having stopped storing text")
	}

	writeSets, err := WriteSets(root)
	if err != nil {
		t.Fatalf("read sql/queries: %v", err)
	}
	written := 0
	for _, tables := range writeSets {
		written += len(tables)
	}
	if written == 0 {
		t.Fatal("no statement under sql/queries was read as writing a table; the resolution " +
			"rule's second half has nothing to confirm against, so nothing can resolve")
	}

	var decls []Declaration
	for _, rel := range HandlerRoots {
		found, ferr := HandlerDeclarations(root, rel)
		if ferr != nil {
			t.Fatalf("read %s: %v", rel, ferr)
		}
		if len(found) == 0 {
			t.Fatalf("no declared bound was read under %s; the handler tree moved or the "+
				"derivation stopped matching, rather than the bounds having gone away", rel)
		}
		for _, d := range found {
			if !strings.HasPrefix(d.Path, filepath.ToSlash(rel)) {
				t.Fatalf("%s was read from %s, outside %s", d.Describe(), d.Path, rel)
			}
		}
		decls = append(decls, found...)
	}

	tools, err := ToolDeclarations(root)
	if err != nil {
		t.Fatalf("read %s: %v", ToolsPath, err)
	}
	if len(tools) == 0 {
		t.Fatalf("no bound was read out of %s; the tool schemas are declared some other way "+
			"now, and the MCP side of every comparison is empty", ToolsPath)
	}
	decls = append(decls, tools...)

	resolutions := ResolveAll(decls, schema, writeSets)
	if placed, _ := Placed(resolutions); len(placed) == 0 {
		t.Fatal("no declared bound resolved to a column, so the overflow half compared " +
			"nothing; the resolution rule has stopped matching this repository's naming")
	}
	if len(Pairs(resolutions)) == 0 {
		t.Fatal("no field is declared on two surfaces, so nothing was compared for " +
			"agreement; the pairing has stopped matching rather than the create/update and " +
			"tool/REST pairs having gone away")
	}

	return tree{resolutions: resolutions}
}

// has reports whether the derivation produced one named field of one owner.
func has(decls []Declaration, owner, section, name string) bool {
	for _, d := range decls {
		if d.Owner == owner && d.Section == section && d.Name == name {
			return true
		}
	}
	return false
}

// unplacedHas reports whether one named field reached no column.
func unplacedHas(all []Resolution, owner, name string) bool {
	for _, r := range all {
		if r.Owner == owner && r.Name == name {
			return true
		}
	}
	return false
}
