package precondition

import (
	"os"
	"path/filepath"
	"sort"
	"testing"
)

// The checks in this file are the positive control. A derived guard that
// reports nothing looks identical whether the tree is clean or the
// derivation stopped matching, and this repository has shipped both: a
// scan that credited a fixture mentioned in a comment, and a loop whose
// body never ran. So the scan is pointed at a tree built to contain each
// failure it is supposed to report, and at the near misses it must not
// report — a prose mention of the rule, a same-named method on a value,
// and a marker with nothing after it.

// controlRule is the rule the synthetic tree is checked against. It is
// shaped like a real one but names only symbols the fixture declares, so
// the control keeps working when the real rules change.
var controlRule = Rule{
	Name:      "chronology",
	Table:     "calendar_events",
	Columns:   []string{"start_at"},
	Enforcers: []string{modulePath + "/internal/calendarrules.RequireEventChronology"},
	Marker:    "calendar-precondition",
	Why:       "the fixture rule",
}

// controlStatements are the synthetic SQL statements: one write, one
// read, and one write to a different table.
const controlSQL = `-- name: WriteWindow :execlastid
INSERT INTO calendar_events (
  public_id,
  start_at,
  end_at
) VALUES (?, ?, ?);

-- name: PreserveWindow :execrows
UPDATE calendar_events
SET title = COALESCE(sqlc.narg('title'), title),
    start_at = COALESCE(sqlc.narg('start_at'), start_at)
WHERE public_id = ?;

-- name: RenameOnly :execrows
UPDATE calendar_events
SET title = ?
WHERE start_at = ?;

-- name: ReadWindow :many
SELECT public_id, start_at, end_at FROM calendar_events WHERE start_at >= ?;

-- name: WriteElsewhere :execrows
UPDATE calendar_event_memos
SET start_at = ?
WHERE id = ?;
`

// TestSinkDerivationSeparatesWritesFromReads is the control for the SQL
// half: which statements put a caller in scope.
func TestSinkDerivationSeparatesWritesFromReads(t *testing.T) {
	t.Parallel()

	statements := parseQueryFile("sql/queries/control.sql", controlSQL)
	if len(statements) != 5 {
		t.Fatalf("the fixture parsed into %d statements, want 5; the header scan is not reading the file", len(statements))
	}
	sinks := Sinks(nil, statements, controlRule)

	got := make([]string, 0, len(sinks))
	for name := range sinks {
		got = append(got, name)
	}
	sort.Strings(got)

	want := []string{"PreserveWindow", "WriteWindow"}
	if len(got) != len(want) {
		t.Fatalf("derived sinks %v, want %v", got, want)
	}
	for i := range got {
		if got[i] != want[i] {
			t.Errorf("derived sink %d is %q, want %q", i, got[i], want[i])
		}
	}
}

// TestScanReportsWhatItIsMeantToReport is the control for the Go half.
//
// It pins, in one pass over one tree: an entry that applies the rule, one
// that does not, one that reaches it through a helper, one exempted by a
// marker, one whose marker states no reason, one whose marker covers
// nothing, an entry that only names the rule in prose, an entry that
// calls a same-named method on a value, and an entry whose write is built
// as a Go string literal rather than declared in sql/queries. Only the
// ones that genuinely skip the rule may be reported.
//
// The literal-writing entry is not a variation on the others. It is the
// form a sink set derived from sql/queries alone cannot see at all, so
// without it the whole check would keep passing over every write that
// stops following the convention.
func TestScanReportsWhatItIsMeantToReport(t *testing.T) {
	t.Parallel()

	root := writeControlTree(t)
	src, err := Parse(root)
	if err != nil {
		t.Fatalf("parse control tree: %v", err)
	}

	entries := map[string]Entry{}
	for _, e := range src.Entries {
		entries[e.Name] = e
	}
	for _, want := range []string{
		"applies", "skips", "via_helper", "marked", "reasonless",
		"stale_marker", "mentions_in_prose", "same_named_method",
		"writes_via_literal", "rest-skips",
	} {
		if _, ok := entries[want]; !ok {
			t.Fatalf("the control tree did not yield entry %q; the registry walk is not reading the fixture", want)
		}
	}
	if len(src.Entries) != 10 {
		t.Fatalf("the control tree yielded %d entries, want 10", len(src.Entries))
	}

	statements := parseQueryFile("sql/queries/control.sql", controlSQL)
	findings, scope := Check(src, statements, []Rule{controlRule})

	type reported struct {
		name string
		kind FindingKind
	}
	var got []reported
	for _, f := range findings {
		got = append(got, reported{name: f.Entry.Name, kind: f.Kind})
	}
	sort.Slice(got, func(i, j int) bool {
		if got[i].name != got[j].name {
			return got[i].name < got[j].name
		}
		return got[i].kind < got[j].kind
	})

	want := []reported{
		{name: "mentions_in_prose", kind: Unenforced},
		{name: "reasonless", kind: Unenforced},
		{name: "rest-skips", kind: Unenforced},
		{name: "same_named_method", kind: Unenforced},
		{name: "skips", kind: Unenforced},
		{name: "stale_marker", kind: StaleMarker},
		{name: "writes_via_literal", kind: Unenforced},
	}
	if len(got) != len(want) {
		t.Fatalf("the scan reported %v, want %v", got, want)
	}
	for i := range got {
		if got[i] != want[i] {
			t.Errorf("finding %d is %+v, want %+v", i, got[i], want[i])
		}
	}

	// The findings above are only meaningful if the entries that are not
	// reported were actually looked at, so the scope is asserted too.
	// Eight of the nine tools write the window; stale_marker only reads
	// it, which is why its marker covers nothing.
	if n := len(scope[controlRule.Name]["MCP tool"]); n != 8 {
		t.Errorf("the rule was held against %d MCP tools, want 8", n)
	}
	if n := len(scope[controlRule.Name]["REST operation"]); n != 1 {
		t.Errorf("the rule was held against %d REST operations, want 1", n)
	}
}

// writeControlTree lays out a minimal apps/flow-api/internal tree and
// returns the root that [Parse] and [RepoRoot] would be given.
func writeControlTree(t *testing.T) string {
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

	write("apps/flow-api/internal/calendarrules/rules.go", `package calendarrules

func RequireEventChronology(startAt, endAt *int64) error { return nil }
`)

	write("apps/flow-api/internal/itemkit/schedule.go", `package itemkit

// ScheduleEvent writes the window the way this repository asks it not to
// be written: as SQL in the Go source. The statement is nowhere in
// sql/queries, so a sink set derived from the query files alone contains
// nothing that reaches it.
func ScheduleEvent(ctx Ctx, tx TX) error {
	_, _ = tx.ExecContext(ctx, "INSERT INTO calendar_events (public_id, start_at, end_at) VALUES (?, ?, ?)")
	return nil
}
`)

	write("apps/flow-api/internal/mcp/tools.go", `package mcp

import (
	"`+modulePath+`/internal/calendarrules"
	"`+modulePath+`/internal/itemkit"
)

func (h *Handler) registerAll() {
	h.register(auth.FloorWorkspaceMember, tool{name: "applies", run: runApplies})
	h.register(auth.FloorWorkspaceMember, tool{name: "skips", run: runSkips})
	h.register(auth.FloorWorkspaceMember, tool{name: "via_helper", run: runViaHelper})
	h.register(auth.FloorWorkspaceMember, tool{name: "marked", run: runMarked})
	h.register(auth.FloorWorkspaceMember, tool{name: "reasonless", run: runReasonless})
	h.register(auth.FloorWorkspaceMember, tool{name: "stale_marker", run: runStaleMarker})
	h.register(auth.FloorWorkspaceMember, tool{name: "mentions_in_prose", run: runMentionsInProse})
	h.register(auth.FloorWorkspaceMember, tool{name: "same_named_method", run: runSameNamedMethod})
	h.register(auth.FloorWorkspaceMember, tool{name: "writes_via_literal", run: runWritesViaLiteral})
}

func runApplies(q *Queries) {
	_ = calendarrules.RequireEventChronology(nil, nil)
	_, _ = q.WriteWindow(ctx, params)
}

func runSkips(q *Queries) {
	_, _ = q.WriteWindow(ctx, params)
}

func runViaHelper(q *Queries) {
	checkWindow()
	_, _ = q.PreserveWindow(ctx, params)
}

func checkWindow() {
	_ = calendarrules.RequireEventChronology(nil, nil)
}

// calendar-precondition: chronology not-applicable — the window here is
// derived from a stored row rather than taken from the caller
func runMarked(q *Queries) {
	_, _ = q.WriteWindow(ctx, params)
}

// calendar-precondition: chronology not-applicable —
func runReasonless(q *Queries) {
	_, _ = q.WriteWindow(ctx, params)
}

// calendar-precondition: chronology not-applicable — this one writes no
// window at all and the marker covers nothing
func runStaleMarker(q *Queries) {
	_, _ = q.ReadWindow(ctx, params)
}

// runMentionsInProse names calendarrules.RequireEventChronology in a
// sentence and calls nothing.
func runMentionsInProse(q *Queries) {
	_, _ = q.WriteWindow(ctx, params)
}

func runSameNamedMethod(q *Queries, guard *Guard) {
	_ = guard.RequireEventChronology(nil, nil)
	_, _ = q.WriteWindow(ctx, params)
}

func runWritesViaLiteral(q *Queries) {
	_ = itemkit.ScheduleEvent(ctx, tx)
}
`)

	write("apps/flow-api/internal/http/handlers/calendars/events.go", `package calendars

func PatchEvent(deps Deps) func() {
	return func() {
		_, _ = deps.Queries.PreserveWindow(ctx, params)
	}
}
`)

	write("apps/flow-api/internal/http/router/router.go", `package router

import (
	"github.com/danielgtaylor/huma/v2"

	"`+modulePath+`/internal/http/handlers/calendars"
)

func build(api huma.API, deps Deps) {
	huma.Register(api, huma.Operation{OperationID: "rest-skips"}, calendars.PatchEvent(deps))
}
`)

	return root
}
