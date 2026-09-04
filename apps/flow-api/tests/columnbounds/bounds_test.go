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

// TestUnresolvedBoundsAreVisible prints the bounds no column was found for,
// and the ones a column was found for by the weaker of the two rules.
//
// The unresolved ones are not failures. A search term, a bearer token, a
// free-form instruction and a password are all declared like a field and
// none is stored under the name it arrives as, so requiring an exemption on
// each would mark the majority of the bounds in this repository — and a
// marker the majority carries is one nobody reads. The gap is printed
// instead, so the reach of the overflow check is something someone can look
// at rather than assume.
//
// The call-rule placements are printed for the opposite reason. A placement
// from the owner's name can be checked by reading the name, and one from the
// statements a handler calls cannot: it is right only if the handler really
// stores that field there. Printing each with its column is what lets a
// wrong placement be seen, rather than trusted because it is derived.
//
// The run both of these have to be readable on is the one where everything
// passes, because that is the run where an unnoticed gap looks like
// coverage. A passing package's output is shown only under -v, so the target
// that runs this guard passes -v, and nothing else here writes at that
// volume.
func TestUnresolvedBoundsAreVisible(t *testing.T) {
	t.Parallel()

	tree := readTree(t)
	placed, unplaced := Placed(tree.resolutions)
	byName, byCalls := 0, 0
	var viaCalls []string
	for _, r := range placed {
		if r.Rule == ByName {
			byName++
			continue
		}
		byCalls++
		viaCalls = append(viaCalls, fmt.Sprintf("  %s maxLength=%d -> %s (%d %s) at %s",
			r.Describe(), r.Max, r.Column.Qualified(), r.Column.Capacity, r.Column.Units, r.Location()))
	}

	var report strings.Builder
	fmt.Fprintf(&report, "%d declared bounds: %d placed on a column (%d from the owner's name, "+
		"%d from the statements its handler calls), %d unresolved; %d pairs compared across surfaces\n",
		len(tree.resolutions), len(placed), byName, byCalls, len(unplaced), len(Pairs(tree.resolutions)))

	fmt.Fprintf(&report, "placed by the statements the handler calls (the derivation to check by eye):\n")
	sort.Strings(viaCalls)
	fmt.Fprintln(&report, strings.Join(viaCalls, "\n"))

	fmt.Fprintf(&report, "unresolved (no column found; the overflow check does not reach these):\n")
	lines := make([]string, 0, len(unplaced))
	for _, r := range unplaced {
		lines = append(lines, fmt.Sprintf("  %s maxLength=%d at %s", r.Describe(), r.Max, r.Location()))
	}
	sort.Strings(lines)
	fmt.Fprintln(&report, strings.Join(lines, "\n"))
	fmt.Print(report.String())
}

// TestStoredFieldsDeclareABound refuses a string field that lands in a
// column of bounded width and states no length of its own.
//
// This is the same gap the overflow half names, approached from the other
// side. There the contract draws a line past the column's width, so the
// values between the two are promised and then refused; here it draws no
// line at all, so every value past the width is promised and then refused,
// and the caller has nothing to read that would tell them where the width
// is. The error they get names nothing either way.
//
// The failure carries the column and its width, because that is the number
// whoever fixes the field has to declare, and a fix that goes looking for it
// somewhere else is how a wrong number gets written down.
//
// The counts are printed whether or not anything is refused. The second of
// them is the size of the gap this derivation does not reach — the fields
// that state no length and resolve to no column, which nothing here can
// speak for — and it is on the passing run that it matters, because that is
// the run where an unseen gap looks like coverage.
func TestStoredFieldsDeclareABound(t *testing.T) {
	t.Parallel()

	tree := readTree(t)
	placed, unplaced := Placed(tree.unstated)
	for _, r := range Absent(placed) {
		t.Errorf("%s: %s states no maxLength, and %s holds %d %s. "+
			"Every value past that width validates and is then refused by storage, and the "+
			"contract draws no line the caller could have read, because there is none to read. "+
			"Declare the bound the column holds",
			r.Location(), r.Describe(), r.Column.Qualified(), r.Column.Capacity, r.Column.Units)
	}

	fmt.Printf("%d string fields state no length: %d land in a column of bounded width, "+
		"%d reach no column\n", len(tree.unstated), len(placed), len(unplaced))
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
  slug VARCHAR(64) NOT NULL COMMENT 'Widget slug',
  kind VARCHAR(16) NOT NULL COMMENT 'Widget kind, a value set held as a string rather than an ENUM',
  state ENUM('open','done') NOT NULL DEFAULT 'open' COMMENT 'Lifecycle',
  UNIQUE KEY uniq_widgets_id (id)
) ENGINE=InnoDB;

CREATE TABLE widget_notes (
  id INT UNSIGNED AUTO_INCREMENT PRIMARY KEY,
  title VARCHAR(80) NOT NULL COMMENT 'Note title',
  label VARCHAR(200) NOT NULL COMMENT 'Note label, a column only this table carries'
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
// column, and a fitting bound beside it. Its slug is what the absence half
// exists for — a column of bounded width with nothing said about it — and
// its kind names the values it takes, which fixes its longest value without
// stating a length.
type CreateWidgetInput struct {
	WsID string ` + "`" + `path:"wsId" maxLength:"36"` + "`" + `
	Body struct {
		Title   string ` + "`" + `json:"title" minLength:"1" maxLength:"500"` + "`" + `
		IconURL string ` + "`" + `json:"iconUrl,omitempty" maxLength:"2048"` + "`" + `
		Body    string ` + "`" + `json:"body,omitempty" maxLength:"50000"` + "`" + `
		Slug    string ` + "`" + `json:"slug"` + "`" + `
		Kind    string ` + "`" + `json:"kind" enum:"panel,dial"` + "`" + `
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

// An operation whose name states nothing — no verb any list would hold —
// and which stores what it takes all the same.
type PresignPartInput struct {
	ID   string ` + "`" + `path:"id"` + "`" + `
	Body struct {
		Label  string ` + "`" + `json:"label" maxLength:"300"` + "`" + `
		Title  string ` + "`" + `json:"title" maxLength:"70"` + "`" + `
		Nested struct {
			Label   string ` + "`" + `json:"label" maxLength:"9999"` + "`" + `
			Caption string ` + "`" + `json:"caption" maxLength:"9999"` + "`" + `
		} ` + "`" + `json:"nested"` + "`" + `
	}
}

// The handler is a closure returned by a factory, which is how half of them
// are written here: the input is a parameter of the closure and of nothing
// else, and its statements are called from inside a transaction on a second
// receiver bound to the tx.
func PresignPart(deps Deps) func(context.Context, *PresignPartInput) (*PresignPartOutput, error) {
	return func(ctx context.Context, in *PresignPartInput) (*PresignPartOutput, error) {
		qtx := deps.Queries.WithTx(tx)
		if err := qtx.InsertWidgetNote(ctx, in.Body.Label); err != nil {
			return nil, err
		}
		if _, err := deps.Queries.InsertWidget(ctx, in.Body.Title); err != nil {
			return nil, err
		}
		return &PresignPartOutput{}, nil
	}
}

// An operation that only reads. Its field is spelled like a column and there
// is a table of that name, and nothing it calls writes one.
type LookupWidgetInput struct {
	Body struct {
		Title string ` + "`" + `json:"title" maxLength:"4000"` + "`" + `
		Slug  string ` + "`" + `json:"slug"` + "`" + `
	}
}

func LookupWidget(deps Deps) func(context.Context, *LookupWidgetInput) (*LookupWidgetOutput, error) {
	return func(ctx context.Context, in *LookupWidgetInput) (*LookupWidgetOutput, error) {
		return deps.Queries.FindWidget(ctx, in.Body.Title)
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
			"body":    stringSchema("Widget body.", Constraints{}),
			"slug":    stringSchema("Widget slug.", Constraints{Enum: []string{"left", "right"}}),
			"parts": arraySchema("Parts to create.",
				objectSchema(map[string]any{
					"title":  stringSchema("Part title.", Constraints{MinLength: intPtr(1), MaxLength: intPtr(500)}),
					"weight": intSchema("Part weight.", Constraints{Min: intPtr(0)}),
				}, []string{"title"}),
				Constraints{MaxItems: intPtr(10)}),
			"labels": arraySchema("Free-form labels.",
				stringSchema("One label.", Constraints{MaxLength: intPtr(9999)}),
				Constraints{MaxItems: intPtr(5)}),
			"visible": boolSchema("Whether the widget is listed."),
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
	if !has(handler, "CreateWidgetInput", "body", "slug") {
		t.Fatal("a field stating no length was not read at all; the absence half sees only what " +
			"the reader collects, so it would have nothing to report and would say so quietly")
	}
	if has(handler, "CreateWidgetInput", "body", "kind") {
		t.Error("a field naming the values it accepts was read as one that forgot to state a " +
			"length; the set already fixes its longest value, and asking for a bound on top " +
			"would be asking for a number that changes nothing")
	}
	if has(tools, "create_widget", "body", "slug") {
		t.Error("a tool property naming the values it accepts was read as one stating no length")
	}
	if !has(tools, "create_widget", "body", "iconUrl") {
		t.Fatal("the tool schema properties were not read; the tool half of every comparison " +
			"would be empty")
	}
	if !has(tools, "create_widget", "body", "parts.title") {
		t.Error("a property nested under an array's element schema was not read; an array is " +
			"how a tool takes a repeated body, and every field inside one would go uncompared")
	}
	if has(tools, "create_widget", "body", "labels") {
		t.Error("a bound on an array of strings was read as a field's length; the array holds " +
			"many of them and they do not land in one column, which is the same reading a " +
			"slice of strings gets on the handler side")
	}

	// A helper nobody has taught this walk about. It is written exactly the
	// way the ones it knows are, which is the point: the day somebody adds
	// the next constructor, the properties under it stop being read, and a
	// walk that stepped over what it did not recognise would go on passing
	// while it saw less. Refusing costs one line here; not refusing costs a
	// gap with nothing pointing at it.
	const unknownHelperSrc = `package probe

func registerTools(h *Handler) {
	h.register(auth.FloorProjectEditor, tool{
		name:        "create_widget",
		inputSchema: objectSchema(map[string]any{
			"title": stringSchema("Widget title.", Constraints{MaxLength: intPtr(500)}),
			"spec":  unionSchema("Either shape.", map[string]any{
				"note": stringSchema("A note.", Constraints{MaxLength: intPtr(80)}),
			}),
		}, []string{"title"}),
	})
}
`
	switch _, uerr := ParseTools("probe/tools.go", unknownHelperSrc); {
	case uerr == nil:
		t.Error("a schema constructor this walk does not know was stepped over in silence; " +
			"every property under it goes unread, and the scan covers less than it did with " +
			"nothing saying so")
	case !strings.Contains(uerr.Error(), "unionSchema"):
		t.Errorf("the refusal was %v, which does not name the constructor; the fix is one line "+
			"in the dispatch and whoever reads this has to be told which name to write", uerr)
	}

	const probeScope = "apps/probe-api/internal/http/handlers/widgets"
	calls, err := ParseHandlerCalls(probeScope, map[string]string{"probe.go": handlerSrc})
	if err != nil {
		t.Fatalf("read the control handler's calls: %v", err)
	}
	if len(calls) == 0 {
		t.Fatal("no handler was read as taking an input; the call rule places nothing, and " +
			"every field only it reaches goes unchecked on a green run")
	}
	if !calls.Methods(probeScope, "PresignPartInput")["InsertWidget"] {
		t.Fatal("a statement the handler calls was not read; the call rule rests on reaching " +
			"them, so it would place nothing")
	}
	if !calls.Methods(probeScope, "PresignPartInput")["InsertWidgetNote"] {
		t.Fatal("a statement called on a receiver bound to the transaction was not read; " +
			"every write in this repository is issued that way, so the rule would see only " +
			"the reads")
	}

	decls := append(append([]Declaration(nil), handler...), tools...)
	evidence := Evidence{
		Schema: schema,
		WriteSets: map[string]map[string]bool{
			"widgets":  {"widgets": true, "widget_notes": true, "widget_comments": true},
			"gadgets":  {"gadgets": true},
			"comments": {"comments": true},
		},
		Calls: calls,
		StatementWrites: map[string]map[string]bool{
			"InsertWidget":     {"widgets": true},
			"InsertWidgetNote": {"widget_notes": true},
			"FindWidget":       {},
		},
	}
	resolutions := ResolveAll(decls, evidence)
	placed, unplaced := Placed(resolutions)
	if len(placed) == 0 {
		t.Fatal("nothing resolved to a column, so the overflow half compared nothing")
	}
	byRule := map[Rule]int{}
	for _, r := range placed {
		byRule[r.Rule]++
	}
	for _, rule := range []Rule{ByName, ByCalls} {
		if byRule[rule] == 0 {
			t.Fatalf("the %s rule placed nothing here, so it proves nothing about the tree: "+
				"a rule that has stopped matching reads exactly like one with nothing to say", rule)
		}
	}

	// The overflow half: one bound over its column, and nothing else.
	overflowing := map[string]bool{}
	for _, r := range Overflows(resolutions) {
		overflowing[r.Describe()+" -> "+r.Column.Qualified()] = true
	}
	for _, want := range []string{
		"REST CreateWidgetInput body.title -> widgets.title",
		"MCP create_widget body.title -> widgets.title",
		"REST PresignPartInput body.label -> widget_notes.label",
		"REST PresignPartInput body.nested.label -> widget_notes.label",
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
		{"a member no table the handler writes carries lands nowhere the statements name",
			"PresignPartInput", "nested.caption"},
		{"a query parameter selects rows rather than supplying a value stored in one",
			"PatchWidgetInput", "q"},
		{"a read operation's term is not stored under the name it arrives as",
			"SearchWidgetsInput", "q"},
		{"another resource's column is its own", "CreateGadgetInput", "title"},
		{"a tool whose name does not spell a write on a resource states no column",
			"search_widgets", "query"},
		{"a comment on a widget is not the comment a tool of that name writes",
			"CreateCommentInput", "body"},
		{"a field whose name is a column of two tables the handler writes could land in " +
			"either, which is not an answer", "PresignPartInput", "title"},
		{"an operation that only reads stores nothing, whatever its fields are called",
			"LookupWidgetInput", "title"},
	} {
		for _, r := range Overflows(resolutions) {
			if r.Owner == neighbour.owner && r.Name == neighbour.name {
				t.Errorf("flagged %s %s: %s", r.Owner, r.Name, neighbour.why)
			}
		}
	}
	// The nested members have no column of their own to be placed on; being
	// unresolved is what keeps them out, so say that rather than only that
	// they were not flagged. Nothing states which table the widget's nested
	// title lives in — its input reaches no statement, and the resource its
	// name spells says nothing about a member of another object — while the
	// nested caption reaches statements that write two tables, neither of
	// which carries a column of that name.
	if !unplacedHas(unplaced, "CreateWidgetInput", "nested.title") {
		t.Error("a member of a nested object resolved to a column with no evidence of where " +
			"it lands; the resource the input names says nothing about which table that " +
			"member lives in, and its handler calls no statement that would")
	}
	if !unplacedHas(unplaced, "PresignPartInput", "nested.caption") {
		t.Error("a member none of the tables its handler writes carries resolved to a column; " +
			"the statements are the whole evidence for a nested field, and they name no " +
			"column it could land in")
	}
	if !unplacedHas(unplaced, "SearchWidgetsInput", "q") {
		t.Error("a read operation's term resolved to a column")
	}
	if !unplacedHas(unplaced, "PresignPartInput", "title") {
		t.Error("a field carried by two of the tables its handler writes resolved to one of " +
			"them; two candidates is no answer, and picking between them would place a bound " +
			"against a width nobody chose")
	}
	if !unplacedHas(unplaced, "LookupWidgetInput", "title") {
		t.Error("a field of an operation that only reads resolved to a column; the statements " +
			"it calls write nothing, so there is no column it lands in")
	}
	// The call rule is what places these, and they are the shapes the name
	// rule cannot see: nothing in PresignPartInput spells a resource, and a
	// member of a nested object is outside what a resource name can answer
	// for even where one is spelled.
	for _, r := range placed {
		if r.Owner != "PresignPartInput" || r.Rule == ByCalls {
			continue
		}
		if r.Name == "label" || r.Name == "nested.label" {
			t.Errorf("PresignPartInput.%s was placed by the %s rule; its name states no "+
				"resource, so a placement from it means the name rule is matching something "+
				"it cannot know", r.Name, r.Rule)
		}
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

	// The absence half: a column of bounded width with nothing said about
	// it, on each surface, and nothing else.
	absent := map[string]bool{}
	for _, r := range Absent(resolutions) {
		absent[r.Describe()+" -> "+r.Column.Qualified()] = true
	}
	for _, want := range []string{
		"REST CreateWidgetInput body.slug -> widgets.slug",
		"MCP create_widget body.body -> widgets.body",
	} {
		if !absent[want] {
			t.Errorf("did not flag %q: a field landing in a column of bounded width while "+
				"stating no length is the state the third half exists to catch", want)
		}
		delete(absent, want)
	}
	for extra := range absent {
		t.Errorf("flagged %q, which either states a length or is bounded by the values it names", extra)
	}
	if !unplacedHas(unplaced, "LookupWidgetInput", "slug") {
		t.Error("a field stating no length was placed on a column by an operation that writes " +
			"nothing; being unresolved is what keeps it out, and a field the derivation cannot " +
			"place is reported rather than refused")
	}
	for _, p := range Pairs(resolutions) {
		if !p.A.Bounded || !p.B.Bounded {
			t.Errorf("%s and %s were compared for agreement while one of them states no length; "+
				"a missing bound reads as zero, which disagrees with every number there is",
				p.A.Describe(), p.B.Describe())
		}
	}
}

// tree is the derivation run over the repository, read once per test.
type tree struct {
	// resolutions are the fields that state a bound: the set the overflow
	// and agreement halves read.
	resolutions []Resolution
	// unstated are the string fields that state none: the set the absence
	// half reads.
	unstated []Resolution
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
		t.Fatal("no statement under sql/queries was read as writing a table; the name " +
			"rule's second half has nothing to confirm against, so nothing can resolve")
	}

	statements, err := StatementWrites(root)
	if err != nil {
		t.Fatalf("read the statements under sql/queries: %v", err)
	}
	writing := 0
	for _, tables := range statements {
		writing += len(tables)
	}
	if writing == 0 {
		t.Fatal("no named statement under sql/queries was read as writing a table; either " +
			"the annotation naming a statement changed shape or the file split stopped " +
			"matching, and the call rule can place nothing")
	}

	calls := CallIndex{}
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

		made, cerr := HandlerCallIndex(root, rel)
		if cerr != nil {
			t.Fatalf("read the handler calls under %s: %v", rel, cerr)
		}
		if len(made) == 0 {
			t.Fatalf("no handler under %s was read as taking an input; the handlers are "+
				"written some other way now, and the call rule places nothing", rel)
		}
		for ref, methods := range made {
			if _, seen := calls[ref]; !seen {
				calls[ref] = map[string]bool{}
			}
			for name := range methods {
				calls[ref][name] = true
			}
		}
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

	all := ResolveAll(decls, Evidence{
		Schema:          schema,
		WriteSets:       writeSets,
		Calls:           calls,
		StatementWrites: statements,
	})
	resolutions, unstated := Stated(all)
	if len(resolutions) == 0 {
		t.Fatal("no input field was read as stating a bound; the tags or the tool schemas are " +
			"written some other way now, and the overflow and agreement halves compare nothing")
	}
	if len(unstated) == 0 {
		t.Fatal("no input field was read as stating no bound; every string field in both trees " +
			"declaring a length would be news, so the reader stopped collecting the silent ones " +
			"rather than the silence having gone away")
	}
	if placed, _ := Placed(resolutions); len(placed) == 0 {
		t.Fatal("no declared bound resolved to a column, so the overflow half compared " +
			"nothing; the resolution rule has stopped matching this repository's naming")
	}
	byRule := map[Rule]int{}
	for _, r := range resolutions {
		if r.Placed {
			byRule[r.Rule]++
		}
	}
	for _, rule := range []Rule{ByName, ByCalls} {
		if byRule[rule] == 0 {
			t.Fatalf("the %s rule placed nothing; it has stopped matching this repository, "+
				"and every field only it reaches is now unchecked while the run stays green", rule)
		}
	}
	if len(Pairs(resolutions)) == 0 {
		t.Fatal("no field is declared on two surfaces, so nothing was compared for " +
			"agreement; the pairing has stopped matching rather than the create/update and " +
			"tool/REST pairs having gone away")
	}

	return tree{resolutions: resolutions, unstated: unstated}
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
