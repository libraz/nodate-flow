package precondition

import (
	"os"
	"path/filepath"
	"sort"
	"testing"
)

// The checks in this file are the control for the table half of the
// derivation. A walk parameterised by table that has only ever been run
// against one table is not evidence the parameter is read at all: a
// derivation that still matched a single hard-coded table, or one that
// matched on column names alone, would pass every check written against
// the calendar rules.
//
// So the two fixture rules below name different tables and the *same*
// column. A derivation that ignored the table would put every write in
// both rules' scope; one that read it puts each write in exactly one.

// tableRules are two fixture rules over two tables. They share a column
// name deliberately, and they carry different markers so a marker written
// for one is not read as an exemption from the other.
var tableRules = []Rule{
	{
		Name:      "window",
		Table:     "fixture_events",
		Columns:   []string{"start_at"},
		Enforcers: []string{modulePath + "/internal/fixturerules.RequireWindow"},
		Marker:    "events-precondition",
		Why:       "the fixture rule about an event window",
	},
	{
		Name:      "order",
		Table:     "fixture_items",
		Columns:   []string{"start_at"},
		Enforcers: []string{modulePath + "/internal/fixturerules.RequireOrder"},
		Marker:    "items-precondition",
		Why:       "the fixture rule about an item order",
	},
}

// tableSQL writes the same column on two tables, plus a third table whose
// name extends one of theirs, plus a read of each.
const tableSQL = `-- name: WriteFixtureEvent :execlastid
INSERT INTO fixture_events (
  public_id,
  start_at
) VALUES (?, ?);

-- name: PatchFixtureItem :execrows
UPDATE fixture_items
SET start_at = ?
WHERE public_id = ?;

-- name: WriteFixtureEventNote :execrows
UPDATE fixture_events_notes
SET start_at = ?
WHERE id = ?;

-- name: ReadFixtureEvents :many
SELECT public_id, start_at FROM fixture_events WHERE start_at >= ?;

-- name: ReadFixtureItems :many
SELECT public_id, start_at FROM fixture_items WHERE start_at >= ?;
`

// TestSinksAreAttributedByTable is the control for the SQL half: two
// rules over one column name resolve to different statements.
func TestSinksAreAttributedByTable(t *testing.T) {
	t.Parallel()

	statements := parseQueryFile("sql/queries/tables.sql", tableSQL)
	if len(statements) != 5 {
		t.Fatalf("the fixture parsed into %d statements, want 5; the header scan is not reading the file", len(statements))
	}

	for _, tc := range []struct {
		rule Rule
		want []string
	}{
		{rule: tableRules[0], want: []string{"WriteFixtureEvent"}},
		{rule: tableRules[1], want: []string{"PatchFixtureItem"}},
	} {
		got := sinkNames(Sinks(statements, tc.rule))
		if len(got) != len(tc.want) {
			t.Fatalf("rule %q derived sinks %v, want %v", tc.rule.Name, got, tc.want)
		}
		for i := range got {
			if got[i] != tc.want[i] {
				t.Errorf("rule %q derived sink %d as %q, want %q", tc.rule.Name, i, got[i], tc.want[i])
			}
		}
	}
}

// TestTableNameEndsWhereTheTableEnds pins that a write to a table whose
// name extends the rule's is not the rule's sink. Matching on a prefix
// would report a column that statement never wrote on that table.
func TestTableNameEndsWhereTheTableEnds(t *testing.T) {
	t.Parallel()

	statements := parseQueryFile("sql/queries/tables.sql", tableSQL)
	for _, rule := range tableRules {
		if _, ok := Sinks(statements, rule)["WriteFixtureEventNote"]; ok {
			t.Errorf("rule %q claims a write to fixture_events_notes; the table name is being matched as a prefix", rule.Name)
		}
	}
}

// TestEntriesAreAttributedToTheirTablesRule is the control for the Go
// half, driven over one tree that writes both tables.
//
// It pins, in one pass: an entry per table that applies its rule, one per
// table that does not, one whose marker names the other table's rule, and
// one that writes only the prefix-sharing table. Each finding has to land
// under the rule whose table the entry wrote.
func TestEntriesAreAttributedToTheirTablesRule(t *testing.T) {
	t.Parallel()

	root := writeTableControlTree(t)
	src, err := Parse(root)
	if err != nil {
		t.Fatalf("parse control tree: %v", err)
	}

	entries := map[string]Entry{}
	for _, e := range src.Entries {
		entries[e.Name] = e
	}
	for _, want := range []string{
		"events_unenforced", "events_enforced", "items_unenforced",
		"items_enforced", "misdirected_marker", "notes_only", "rest-events",
	} {
		if _, ok := entries[want]; !ok {
			t.Fatalf("the control tree did not yield entry %q; the registry walk is not reading the fixture", want)
		}
	}
	if len(src.Entries) != 7 {
		t.Fatalf("the control tree yielded %d entries, want 7", len(src.Entries))
	}

	statements := parseQueryFile("sql/queries/tables.sql", tableSQL)
	findings, scope := Check(src, statements, tableRules)

	type reported struct {
		rule string
		name string
		kind FindingKind
	}
	var got []reported
	for _, f := range findings {
		got = append(got, reported{rule: f.Rule, name: f.Entry.Name, kind: f.Kind})
	}
	sort.Slice(got, func(i, j int) bool {
		if got[i].rule != got[j].rule {
			return got[i].rule < got[j].rule
		}
		return got[i].name < got[j].name
	})

	// misdirected_marker writes fixture_items and carries the events
	// rule's marker. The marker exempts it from nothing it does, so the
	// items rule still reports it and the events rule reports the marker.
	want := []reported{
		{rule: "order", name: "items_unenforced", kind: Unenforced},
		{rule: "order", name: "misdirected_marker", kind: Unenforced},
		{rule: "window", name: "events_unenforced", kind: Unenforced},
		{rule: "window", name: "misdirected_marker", kind: StaleMarker},
		{rule: "window", name: "rest-events", kind: Unenforced},
	}
	if len(got) != len(want) {
		t.Fatalf("the scan reported %v, want %v", got, want)
	}
	for i := range got {
		if got[i] != want[i] {
			t.Errorf("finding %d is %+v, want %+v", i, got[i], want[i])
		}
	}

	// The findings are only meaningful if each rule was held against the
	// entries that wrote its table and no others, so the scope is
	// asserted per rule. notes_only writes neither table and appears in
	// neither bucket.
	for _, tc := range []struct {
		rule    string
		surface string
		want    []string
	}{
		{rule: "window", surface: "MCP tool", want: []string{"events_enforced", "events_unenforced"}},
		{rule: "window", surface: "REST operation", want: []string{"rest-events"}},
		{rule: "order", surface: "MCP tool", want: []string{"items_enforced", "items_unenforced", "misdirected_marker"}},
		{rule: "order", surface: "REST operation", want: nil},
	} {
		var names []string
		for _, e := range scope[tc.rule][tc.surface] {
			names = append(names, e.Name)
		}
		sort.Strings(names)
		if len(names) != len(tc.want) {
			t.Fatalf("rule %q was held against %s %v, want %v", tc.rule, tc.surface, names, tc.want)
		}
		for i := range names {
			if names[i] != tc.want[i] {
				t.Errorf("rule %q %s %d is %q, want %q", tc.rule, tc.surface, i, names[i], tc.want[i])
			}
		}
	}
}

// sinkNames renders a derived sink set in a stable order.
func sinkNames(sinks map[string]Statement) []string {
	out := make([]string, 0, len(sinks))
	for name := range sinks {
		out = append(out, name)
	}
	sort.Strings(out)
	return out
}

// writeTableControlTree lays out a minimal apps/flow-api/internal tree
// whose entries write two different tables, and returns the root [Parse]
// would be given.
func writeTableControlTree(t *testing.T) string {
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

	write("apps/flow-api/internal/fixturerules/rules.go", `package fixturerules

func RequireWindow(startAt *int64) error { return nil }

func RequireOrder(startAt *int64) error { return nil }
`)

	write("apps/flow-api/internal/mcp/tools.go", `package mcp

import "`+modulePath+`/internal/fixturerules"

func (h *Handler) registerAll() {
	h.register(auth.FloorWorkspaceMember, tool{name: "events_unenforced", run: runEventsUnenforced})
	h.register(auth.FloorWorkspaceMember, tool{name: "events_enforced", run: runEventsEnforced})
	h.register(auth.FloorWorkspaceMember, tool{name: "items_unenforced", run: runItemsUnenforced})
	h.register(auth.FloorWorkspaceMember, tool{name: "items_enforced", run: runItemsEnforced})
	h.register(auth.FloorWorkspaceMember, tool{name: "misdirected_marker", run: runMisdirectedMarker})
	h.register(auth.FloorWorkspaceMember, tool{name: "notes_only", run: runNotesOnly})
}

func runEventsUnenforced(q *Queries) {
	_, _ = q.WriteFixtureEvent(ctx, params)
}

func runEventsEnforced(q *Queries) {
	_ = fixturerules.RequireWindow(nil)
	_, _ = q.WriteFixtureEvent(ctx, params)
}

func runItemsUnenforced(q *Queries) {
	_, _ = q.PatchFixtureItem(ctx, params)
}

func runItemsEnforced(q *Queries) {
	_ = fixturerules.RequireOrder(nil)
	_, _ = q.PatchFixtureItem(ctx, params)
}

// events-precondition: window not-applicable — this names the wrong
// rule for what it writes and exempts nothing it does
func runMisdirectedMarker(q *Queries) {
	_, _ = q.PatchFixtureItem(ctx, params)
}

func runNotesOnly(q *Queries) {
	_, _ = q.WriteFixtureEventNote(ctx, params)
}
`)

	write("apps/flow-api/internal/http/handlers/fixtures/events.go", `package fixtures

func CreateEvent(deps Deps) func() {
	return func() {
		_, _ = deps.Queries.WriteFixtureEvent(ctx, params)
	}
}
`)

	write("apps/flow-api/internal/http/router/router.go", `package router

import (
	"github.com/danielgtaylor/huma/v2"

	"`+modulePath+`/internal/http/handlers/fixtures"
)

func build(api huma.API, deps Deps) {
	huma.Register(api, huma.Operation{OperationID: "rest-events"}, fixtures.CreateEvent(deps))
}
`)

	return root
}
