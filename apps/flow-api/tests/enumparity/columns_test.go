package enumparity

import (
	"fmt"
	"sort"
	"strings"
	"testing"

	"github.com/libraz/nodate-flow/apps/flow-api/tests/columnbounds"
)

// TestEnumFieldsCarryTheirColumnsValues refuses a wire field whose stated
// values its ENUM column cannot answer for.
//
// A field left open tells every client — through the OpenAPI document and
// through the generated SDK — that it takes any string, while the column
// takes a handful. Everything else validates and is then refused by
// storage, so the caller gets a server error for a request the contract
// said was well formed, naming nothing they can act on. A field stating a
// value the column does not accept fails the same way from the other side:
// the contract promises what storage refuses.
//
// A field stating a strict subset is neither. An operation may take less
// than its column holds, and this tree carries narrowings that are the
// point of the operation rather than an oversight, so they are printed by
// the coverage test rather than refused here.
//
// Nothing is listed here. The fields come out of the handler trees, the
// members out of the generated schema, and the mapping between them out of
// the resource each operation names and the tables its own statements
// write — the same resolution the length check runs on.
func TestEnumFieldsCarryTheirColumnsValues(t *testing.T) {
	t.Parallel()

	for _, c := range Violations(Compare(readEnumTree(t).placements)) {
		if c.Verdict == Open {
			t.Errorf("%s: %s leaves %s.%s open, and it writes %s, which accepts only %s. "+
				"Every client is told the field is any string, so anything else validates and "+
				"is refused by storage: the caller gets a server error for a request this API "+
				"called well formed. Give the field an enum tag carrying those values, or the "+
				"narrower set this operation actually takes",
				c.Location(), c.Owner, c.Section, c.Name,
				c.Column.Qualified(), strings.Join(c.Column.Members, ","))
			continue
		}
		t.Errorf("%s: %s states %s.%s as %s, while %s accepts only %s, so %s is promised by "+
			"this contract and refused by storage. Either the column is missing a value or the "+
			"tag is stating one nothing can store",
			c.Location(), c.Owner, c.Section, c.Name,
			strings.Join(c.Declared(), ","), c.Column.Qualified(),
			strings.Join(c.Column.Members, ","), strings.Join(c.Extra, ","))
	}
}

// TestEnumColumnCoverageIsVisible prints what this check reaches and what
// it does not refuse: how much of the schema's ENUM surface it places a
// field on, which placements came from the weaker of the two derivations,
// which fields state a strict subset of their column, and which stated
// value sets it could not confirm against any column.
//
// The narrowings are printed because they are the shape a genuine oversight
// hides in. A field that omits a value because the operation must not take
// it and one that omits a value somebody forgot to add read identically
// from here, and the difference is a question about the operation that no
// derivation answers. Refusing both would put an exemption on more
// declarations than the rule caught; printing them is what lets the
// difference be looked at.
//
// The unplaced ones are not failures either. A field naming a value set is not
// always stored under the name it arrives as — a filter, a sort key and a
// mode selecting a code path are all spelled like a column and none of them
// is one — so requiring an exemption on each would mark most of them, and a
// marker most of them carry is one nobody reads. The gap is printed
// instead, so the reach of this check is something someone can look at
// rather than assume.
//
// The run this has to be readable on is the one where everything passes,
// because that is the run where an unnoticed gap looks like coverage. A
// passing package's output is shown only under -v, so the target that runs
// this guard passes -v.
func TestEnumColumnCoverageIsVisible(t *testing.T) {
	t.Parallel()

	tree := readEnumTree(t)

	placedAt := map[string]bool{}
	byName, byCalls := 0, 0
	var viaCalls, columns []string
	for _, p := range tree.placements {
		placedAt[p.Location()] = true
		columns = append(columns, p.Column.Qualified())
		if p.Rule == columnbounds.ByName {
			byName++
			continue
		}
		byCalls++
		viaCalls = append(viaCalls, fmt.Sprintf("  %s %s.%s -> %s at %s",
			p.Owner, p.Section, p.Name, p.Column.Qualified(), p.Location()))
	}
	distinct := map[string]bool{}
	for _, c := range columns {
		distinct[c] = true
	}

	comparisons := Compare(tree.placements)
	var narrowed []string
	for _, c := range WithVerdict(comparisons, Narrows) {
		narrowed = append(narrowed, fmt.Sprintf("  %s %s.%s omits %s of %s at %s",
			c.Owner, c.Section, c.Name, strings.Join(c.Missing, ","),
			c.Column.Qualified(), c.Location()))
	}

	var unconfirmed []string
	for _, f := range tree.fields {
		if f.Enum == "" || placedAt[f.Location()] {
			continue
		}
		unconfirmed = append(unconfirmed, fmt.Sprintf("  %s %s.%s = %s at %s",
			f.Owner, f.Section, f.Name, f.Enum, f.Location()))
	}

	var report strings.Builder
	fmt.Fprintf(&report, "%d ENUM columns in the schema; %d input fields placed on %d of them "+
		"(%d from the owner's name, %d from the statements its handler calls); "+
		"%d state their column's values exactly\n",
		tree.enumColumns, len(tree.placements), len(distinct), byName, byCalls,
		len(WithVerdict(comparisons, Matches)))

	fmt.Fprintln(&report, "placed by the statements the handler calls (the derivation to check by eye):")
	sort.Strings(viaCalls)
	fmt.Fprintln(&report, strings.Join(viaCalls, "\n"))

	fmt.Fprintln(&report, "stating a strict subset of their column (not refused; each is either the "+
		"point of the operation or a value nobody added):")
	sort.Strings(narrowed)
	fmt.Fprintln(&report, strings.Join(narrowed, "\n"))

	fmt.Fprintln(&report, "stated value sets no ENUM column was found for (this check does not reach these):")
	sort.Strings(unconfirmed)
	fmt.Fprintln(&report, strings.Join(unconfirmed, "\n"))
	fmt.Print(report.String())
}

// TestColumnDerivationStillMatches is the positive control for the column
// half, and it runs on every invocation because it reads sources stated
// here rather than the tree.
//
// A guard that has stopped being able to see anything reads exactly like a
// guard with nothing to report. So the three states this exists to tell
// apart are driven through the same functions the tree goes through — a
// field stating its column's values, one stating none, and one stating a
// different set in each direction — alongside the neighbours that look like
// each and are not.
func TestColumnDerivationStillMatches(t *testing.T) {
	t.Parallel()

	const schemaSrc = `
CREATE TABLE widgets (
  id INT UNSIGNED AUTO_INCREMENT PRIMARY KEY COMMENT 'Internal PK, never exposed',
  title VARCHAR(255) NOT NULL COMMENT 'Widget title (255, note the -- and (parens) a comment may carry)',
  state ENUM('open','done') NOT NULL DEFAULT 'open' COMMENT 'Lifecycle, and a comment carrying a comma, a (paren) and a -- marker',
  mode ENUM('draft','live') NOT NULL COMMENT 'Editing mode',
  kind ENUM('simple','compound') NOT NULL COMMENT 'Widget kind',
  states ENUM('x','y') NOT NULL COMMENT 'A column spelled like a repeated field'
) ENGINE=InnoDB;

CREATE TABLE widget_notes (
  id INT UNSIGNED AUTO_INCREMENT PRIMARY KEY,
  phase ENUM('early','late') NOT NULL COMMENT 'Note phase'
) ENGINE=InnoDB;

CREATE TABLE gadgets (
  id INT UNSIGNED AUTO_INCREMENT PRIMARY KEY,
  mode ENUM('fast','slow') NOT NULL COMMENT 'Another resource state of its own'
) ENGINE=InnoDB;
`

	const handlerSrc = `package probe

// The three states this exists to tell apart, plus the neighbours.
type CreateWidgetInput struct {
	WsID string ` + "`" + `path:"wsId"` + "`" + `
	Q    string ` + "`" + `query:"state"` + "`" + `
	Body struct {
		State  string ` + "`" + `json:"state" enum:"open,done"` + "`" + `
		Mode   string ` + "`" + `json:"mode"` + "`" + `
		Kind   string ` + "`" + `json:"kind" enum:"simple"` + "`" + `
		Title  string ` + "`" + `json:"title" maxLength:"255"` + "`" + `
		States []string ` + "`" + `json:"states"` + "`" + `
		Nested struct {
			State string ` + "`" + `json:"state"` + "`" + `
		} ` + "`" + `json:"nested"` + "`" + `
	}
}

// PatchWidgetBody is named rather than inline, which is how half the inputs
// in this repository spell a body.
type PatchWidgetBody struct {
	State *string ` + "`" + `json:"state" enum:"open,done,archived"` + "`" + `
}

type PatchWidgetInput struct {
	ID   string ` + "`" + `path:"id"` + "`" + `
	Body PatchWidgetBody
}

// Another resource, whose mode is its own column and is stated in full.
type CreateGadgetInput struct {
	Body struct {
		Mode string ` + "`" + `json:"mode" enum:"fast,slow"` + "`" + `
	}
}

// An operation whose name states no verb, and which stores what it takes
// all the same. Only the call rule reaches it.
type PresignPartInput struct {
	Body struct {
		Phase string ` + "`" + `json:"phase"` + "`" + `
	}
}

func PresignPart(deps Deps) func(context.Context, *PresignPartInput) (*PresignPartOutput, error) {
	return func(ctx context.Context, in *PresignPartInput) (*PresignPartOutput, error) {
		qtx := deps.Queries.WithTx(tx)
		return nil, qtx.InsertWidgetNote(ctx, in.Body.Phase)
	}
}

// An operation that only reads. Its field is spelled like a column and
// nothing it calls writes one.
type LookupWidgetInput struct {
	Body struct {
		State string ` + "`" + `json:"state"` + "`" + `
	}
}

func LookupWidget(deps Deps) func(context.Context, *LookupWidgetInput) (*LookupWidgetOutput, error) {
	return func(ctx context.Context, in *LookupWidgetInput) (*LookupWidgetOutput, error) {
		return deps.Queries.FindWidget(ctx, in.Body.State)
	}
}
`

	schema := columnbounds.ParseSchema(schemaSrc)
	if schema.EnumCount() == 0 {
		t.Fatal("the control schema produced no ENUM column; the members are read out of " +
			"string literals, and every comparison this check makes is against them")
	}
	state, ok := schema.EnumColumn("widgets", "state")
	if !ok || strings.Join(state.Members, ",") != "open,done" {
		t.Fatalf("widgets.state read as %+v (found=%v), want members open,done in that order; "+
			"a column comment carrying a comma, a parenthesis and a comment marker must not be "+
			"read as structure", state, ok)
	}
	if _, isBounded := schema.Column("widgets", "state"); isBounded {
		t.Error("an ENUM was read as a length-bounded column; the length check would compare a " +
			"width against a value set and report something no bound could fix")
	}
	if c, boundedOK := schema.Column("widgets", "title"); !boundedOK || c.Capacity != 255 {
		t.Fatalf("widgets.title read as %+v (found=%v), want 255; separating the ENUMs must not "+
			"take the length-bounded columns with them", c, boundedOK)
	}

	const probeScope = "apps/probe-api/internal/http/handlers/widgets"
	fields, err := ParsePackage(probeScope, map[string]string{"probe.go": handlerSrc})
	if err != nil {
		t.Fatalf("parse the control handler source: %v", err)
	}
	if !hasField(fields, "PatchWidgetInput", "body", "state") {
		t.Fatal("the fields of a body declared as a separate type were not read; half the " +
			"inputs in this repository are written that way, and none of them would be placed")
	}
	calls, err := columnbounds.ParseHandlerCalls(probeScope, map[string]string{"probe.go": handlerSrc})
	if err != nil {
		t.Fatalf("read the control handler's calls: %v", err)
	}
	if !calls.Methods(probeScope, "PresignPartInput")["InsertWidgetNote"] {
		t.Fatal("a statement called on a receiver bound to the transaction was not read; every " +
			"write in this repository is issued that way, so the call rule would place nothing")
	}

	placements := PlaceOnEnums(fields, columnbounds.Evidence{
		Schema: schema,
		// One package writing several tables is the ordinary shape here, and
		// it is what makes the resource in an operation's name load-bearing:
		// two of these tables carry a column called mode.
		WriteSets: map[string]map[string]bool{
			"widgets": {"widgets": true, "widget_notes": true, "gadgets": true},
		},
		Calls: calls,
		StatementWrites: map[string]map[string]bool{
			"InsertWidgetNote": {"widget_notes": true},
			"FindWidget":       {},
		},
	})
	if len(placements) == 0 {
		t.Fatal("nothing was placed on an ENUM column, so the comparison ran against nothing")
	}
	byRule := map[columnbounds.Rule]int{}
	for _, p := range placements {
		byRule[p.Rule]++
	}
	for _, rule := range []columnbounds.Rule{columnbounds.ByName, columnbounds.ByCalls} {
		if byRule[rule] == 0 {
			t.Fatalf("the %s rule placed nothing here, so it proves nothing about the tree: a "+
				"rule that has stopped matching reads exactly like one with nothing to say", rule)
		}
	}

	// The four verdicts, told apart. Each is keyed by owner and field, and
	// carries what a failure would have to say about it.
	got := map[string]string{}
	for _, c := range Compare(placements) {
		got[c.Owner+"."+c.Name] = fmt.Sprintf("%s missing=%v extra=%v", c.Verdict, c.Missing, c.Extra)
	}
	for _, want := range []struct{ field, state string }{
		{"CreateWidgetInput.state", "matches missing=[] extra=[]"},
		{"CreateWidgetInput.mode", "open missing=[draft live] extra=[]"},
		{"CreateWidgetInput.kind", "narrows missing=[compound] extra=[]"},
		{"PatchWidgetInput.state", "overstates missing=[] extra=[archived]"},
		{"CreateGadgetInput.mode", "matches missing=[] extra=[]"},
		{"PresignPartInput.phase", "open missing=[early late] extra=[]"},
	} {
		if got[want.field] != want.state {
			t.Errorf("%s compared as %q, want %q", want.field, got[want.field], want.state)
		}
		delete(got, want.field)
	}
	for field, state := range got {
		t.Errorf("compared %s as %s, and nothing here places it on a column", field, state)
	}

	// What is refused, as against what is only reported. A subset that
	// became a failure would be answered by an exemption, and a value the
	// column cannot store that stopped being one would leave the contract
	// promising it.
	refused := map[string]bool{}
	for _, c := range Violations(Compare(placements)) {
		refused[c.Owner+"."+c.Name] = true
	}
	for _, want := range []string{"CreateWidgetInput.mode", "PresignPartInput.phase", "PatchWidgetInput.state"} {
		if !refused[want] {
			t.Errorf("did not refuse %s: a field stating nothing, or stating a value its column "+
				"cannot store, is the state this exists to catch", want)
		}
		delete(refused, want)
	}
	for field := range refused {
		t.Errorf("refused %s, and an operation may take less than its column holds", field)
	}

	// The neighbours. Each is a shape the tree actually contains, and each
	// would be answered by adding an exemption if it were flagged.
	placed := map[string]bool{}
	for _, p := range placements {
		placed[p.Owner+"."+p.Section+"."+p.Name] = true
	}
	for _, neighbour := range []struct{ why, field string }{
		{"a field stating exactly what its column accepts is the state this wants",
			"CreateWidgetInput.body.state"},
		{"a length-bounded column states no value set, so a field on one is not missing an enum",
			"CreateWidgetInput.body.title"},
		{"a repeated field carries its values into rows of another table rather than into a column",
			"CreateWidgetInput.body.states"},
		{"a member of a nested object is not a column of the resource the input is named after",
			"CreateWidgetInput.body.nested.state"},
		{"a query parameter selects rows rather than supplying a value stored in one",
			"CreateWidgetInput.query.state"},
		{"an operation that only reads stores nothing, whatever its fields are called",
			"LookupWidgetInput.body.state"},
	} {
		if neighbour.field == "CreateWidgetInput.body.state" {
			if !placed[neighbour.field] {
				t.Errorf("did not place %s: %s", neighbour.field, neighbour.why)
			}
			continue
		}
		if placed[neighbour.field] {
			t.Errorf("placed %s on an ENUM column: %s", neighbour.field, neighbour.why)
		}
	}
	// Another resource's column is its own, and this one states it in full,
	// so it has to be placed and has to pass.
	if !placed["CreateGadgetInput.body.mode"] {
		t.Error("a second resource's field was not placed; two packages spell the same noun, " +
			"and each of their columns is its own")
	}
}

// enumTree is the column half of the derivation run over the repository.
type enumTree struct {
	fields      []Field
	placements  []Placement
	enumColumns int
}

// readEnumTree runs the derivation over the committed sources and proves
// every input set it rests on is non-empty.
//
// This check reads files by path. A handler tree that moved, a schema dump
// that stopped being generated, a query annotation that changed shape —
// each of them empties one of these sets, and an empty set passes every
// comparison it is asked to make.
func readEnumTree(t *testing.T) enumTree {
	t.Helper()
	root, err := RepoRoot()
	if err != nil {
		t.Fatalf("locate repository root: %v", err)
	}

	dump, err := columnbounds.ReadSchema(root)
	if err != nil {
		t.Fatalf("read sql/schema.sql: %v", err)
	}
	schema := columnbounds.ParseSchema(dump)
	if schema.EnumCount() == 0 {
		t.Fatal("sql/schema.sql declared no ENUM column; the dump or the reader changed shape, " +
			"rather than the schema having stopped constraining values")
	}

	writeSets, err := columnbounds.WriteSets(root)
	if err != nil {
		t.Fatalf("read sql/queries: %v", err)
	}
	written := 0
	for _, tables := range writeSets {
		written += len(tables)
	}
	if written == 0 {
		t.Fatal("no statement under sql/queries was read as writing a table; the name rule's " +
			"second half has nothing to confirm against, so nothing can resolve")
	}

	statements, err := columnbounds.StatementWrites(root)
	if err != nil {
		t.Fatalf("read the statements under sql/queries: %v", err)
	}
	writing := 0
	for _, tables := range statements {
		writing += len(tables)
	}
	if writing == 0 {
		t.Fatal("no named statement under sql/queries was read as writing a table; the call " +
			"rule can place nothing")
	}

	calls := columnbounds.CallIndex{}
	var fields []Field
	for _, rel := range Roots {
		found, ferr := Fields(root, []string{rel})
		if ferr != nil {
			t.Fatalf("read %s: %v", rel, ferr)
		}
		if len(found) == 0 {
			t.Fatalf("no input field was read under %s; the handler tree moved or the "+
				"derivation stopped matching, rather than the inputs having gone away", rel)
		}
		fields = append(fields, found...)

		made, cerr := columnbounds.HandlerCallIndex(root, rel)
		if cerr != nil {
			t.Fatalf("read the handler calls under %s: %v", rel, cerr)
		}
		if len(made) == 0 {
			t.Fatalf("no handler under %s was read as taking an input; the handlers are written "+
				"some other way now, and the call rule places nothing", rel)
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

	placements := PlaceOnEnums(fields, columnbounds.Evidence{
		Schema:          schema,
		WriteSets:       writeSets,
		Calls:           calls,
		StatementWrites: statements,
	})
	if len(placements) == 0 {
		t.Fatal("no input field resolved to an ENUM column, so nothing was compared against a " +
			"value set; the resolution has stopped matching this repository's naming rather " +
			"than the enum-backed fields having gone away")
	}

	return enumTree{fields: fields, placements: placements, enumColumns: schema.EnumCount()}
}
