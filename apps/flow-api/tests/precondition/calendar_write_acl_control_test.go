package precondition

import (
	"os"
	"path/filepath"
	"sort"
	"testing"
)

// The checks in this file are the positive control for the calendar-write
// ACL scan. A derived guard that reports nothing looks identical whether
// the tree is clean or the derivation stopped matching, and this
// repository has shipped both kinds of hole: a scan that credited a
// fixture named only in a comment, and a loop whose body never ran. So the
// scan is pointed at a tree built to contain each failure it is supposed
// to report, and at the near misses it must not report — a prose mention
// of the rule, a same-named method on a value, a marker with nothing after
// it, and a write REST itself performs outside the gate.

// controlWriteSQL is the synthetic query file: a write to a calendar's
// contents, a write REST performs without the gate, a read, and a write to
// a table that is not a calendar's.
const controlWriteSQL = `-- name: WriteContents :execlastid
INSERT INTO calendar_events (
  public_id,
  title
) VALUES (?, ?);

-- name: WriteOwnRsvp :execrows
UPDATE calendar_event_attendees
SET rsvp = ?
WHERE id = ?;

-- name: ReadContents :many
SELECT public_id, title FROM calendar_events WHERE calendar_id = ?;

-- name: WriteElsewhere :execrows
UPDATE tasks
SET title = ?
WHERE id = ?;
`

// TestWriteACLSinkDerivationSeparatesWritesFromReads is the control for
// the sink half: what counts as a place a calendar's contents are written.
func TestWriteACLSinkDerivationSeparatesWritesFromReads(t *testing.T) {
	t.Parallel()

	root := writeWriteACLControlTree(t)
	src, err := Parse(root)
	if err != nil {
		t.Fatalf("parse control tree: %v", err)
	}
	statements := parseQueryFile("sql/queries/control.sql", controlWriteSQL)
	if len(statements) != 4 {
		t.Fatalf("the fixture parsed into %d statements, want 4; the header scan is not reading the file", len(statements))
	}

	got := map[string]WriteSink{}
	for _, s := range CalendarWriteSinks(src, statements) {
		got[s.Name] = s
	}

	for _, want := range []string{"WriteContents", "WriteOwnRsvp", "DeleteEvent"} {
		if _, ok := got[want]; !ok {
			t.Errorf("%s writes a calendar table but is not derived as a sink", want)
		}
	}
	for _, unwanted := range []string{"ReadContents", "WriteElsewhere", "describesADelete"} {
		if _, ok := got[unwanted]; ok {
			t.Errorf("%s is derived as a sink; it reads a calendar table, writes another table, or only describes a write in prose", unwanted)
		}
	}
	if sink := got["DeleteEvent"]; sink.Symbol == "" {
		t.Error("the literal-built delete is derived by name rather than by symbol; matching it by name would credit any same-named method on a value")
	}
	if sink := got["WriteContents"]; sink.Symbol != "" {
		t.Error("a sqlc statement is derived by symbol; it is performed through a method on a generated querier and can only be matched by name")
	}
}

// TestWriteACLClassificationReadsREST is the control for the
// classification half: which sinks the MCP tools are held to, decided from
// what the REST operations do rather than from a table name.
func TestWriteACLClassificationReadsREST(t *testing.T) {
	t.Parallel()

	root := writeWriteACLControlTree(t)
	src, err := Parse(root)
	if err != nil {
		t.Fatalf("parse control tree: %v", err)
	}
	statements := parseQueryFile("sql/queries/control.sql", controlWriteSQL)
	governed, ungoverned := GovernedWriteSinks(reachAll(src), CalendarWriteSinks(src, statements))

	names := make([]string, 0, len(governed))
	for _, s := range governed {
		names = append(names, s.Name)
	}
	sort.Strings(names)

	want := []string{"DeleteEvent", "WriteContents"}
	if len(names) != len(want) {
		t.Fatalf("classified %v as writes to a calendar's contents, want %v", names, want)
	}
	for i := range names {
		if names[i] != want[i] {
			t.Errorf("classified sink %d is %q, want %q", i, names[i], want[i])
		}
	}
	if writers := ungoverned["WriteOwnRsvp"]; len(writers) != 1 || writers[0] != "rsvp" {
		t.Errorf("the RSVP write was disqualified by %v, want the one REST operation that performs it outside the gate", writers)
	}
}

// TestWriteACLScanReportsWhatItIsMeantToReport is the control for the Go
// half.
//
// It pins, in one pass over one tree: a tool that applies the rule, one
// that does not, one that reaches it through a helper, one exempted by a
// marker, one whose marker states no reason, one whose marker covers
// nothing, a tool that only names the rule in prose, a tool that calls a
// same-named method on a value, a tool that writes only the sink REST
// itself writes outside the gate, and a tool that reaches the write
// through a literal-built helper. Only the ones that genuinely skip the
// rule may be reported.
func TestWriteACLScanReportsWhatItIsMeantToReport(t *testing.T) {
	t.Parallel()

	root := writeWriteACLControlTree(t)
	src, err := Parse(root)
	if err != nil {
		t.Fatalf("parse control tree: %v", err)
	}

	entries := map[string]Entry{}
	for _, e := range src.Entries {
		entries[e.Name] = e
	}
	for _, want := range []string{
		"applies", "skips", "via_helper", "marked", "reasonless", "stale_marker",
		"mentions_in_prose", "same_named_method", "rsvp_only", "deletes",
		"contents-create", "contents-delete", "rsvp",
	} {
		if _, ok := entries[want]; !ok {
			t.Fatalf("the control tree did not yield entry %q; the registry walk is not reading the fixture", want)
		}
	}

	statements := parseQueryFile("sql/queries/control.sql", controlWriteSQL)
	reach := reachAll(src)
	governed, _ := GovernedWriteSinks(reach, CalendarWriteSinks(src, statements))
	findings, inScope := CheckCalendarWriteACL(src, reach, governed)

	type reported struct {
		name string
		kind FindingKind
	}
	var got []reported
	for _, f := range findings {
		got = append(got, reported{name: f.Entry.Name, kind: f.Kind})
	}

	want := []reported{
		{name: "deletes", kind: Unenforced},
		{name: "mentions_in_prose", kind: Unenforced},
		{name: "reasonless", kind: Unenforced},
		{name: "same_named_method", kind: Unenforced},
		{name: "skips", kind: Unenforced},
		{name: "stale_marker", kind: StaleMarker},
	}
	if len(got) != len(want) {
		t.Fatalf("the scan reported %v, want %v", got, want)
	}
	for i := range got {
		if got[i] != want[i] {
			t.Errorf("finding %d is %+v, want %+v", i, got[i], want[i])
		}
	}

	// The findings above are only meaningful if the tools that were not
	// reported were actually looked at, so the scope is asserted too.
	// Eight of the ten tools write a governed sink; stale_marker only
	// reads one and rsvp_only writes the sink REST leaves outside the
	// gate.
	var scoped []string
	for _, e := range inScope {
		scoped = append(scoped, e.Name)
	}
	sort.Strings(scoped)
	wantScope := []string{
		"applies", "deletes", "marked", "mentions_in_prose",
		"reasonless", "same_named_method", "skips", "via_helper",
	}
	if len(scoped) != len(wantScope) {
		t.Fatalf("the rule was held against %v, want %v", scoped, wantScope)
	}
	for i := range scoped {
		if scoped[i] != wantScope[i] {
			t.Errorf("scoped tool %d is %q, want %q", i, scoped[i], wantScope[i])
		}
	}
}

// writeWriteACLControlTree lays out a minimal apps/flow-api/internal tree
// and returns the root that [Parse] would be given.
func writeWriteACLControlTree(t *testing.T) string {
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

	write("apps/flow-api/internal/http/handlers/calendars/resolve.go", `package calendars

func DecideCalendarWrite(kind, role string) int { return 0 }

func resolveCalendarWrite(cq *Queries) error {
	_ = DecideCalendarWrite("", "")
	return nil
}
`)

	write("apps/flow-api/internal/http/handlers/calendars/events.go", `package calendars

import "`+modulePath+`/internal/itemkit"

func CreateEvent(deps Deps) func() {
	return func() {
		_ = resolveCalendarWrite(deps.Cal)
		_, _ = deps.Cal.WriteContents(ctx, params)
	}
}

func DeleteEvent(deps Deps) func() {
	return func() {
		_ = resolveCalendarWrite(deps.Cal)
		_ = itemkit.DeleteEvent(ctx, tx)
	}
}

// RsvpEvent is the write this package performs without the calendar write
// gate: answering an invitation is the caller's own row.
func RsvpEvent(deps Deps) func() {
	return func() {
		_, _ = deps.Cal.WriteOwnRsvp(ctx, params)
	}
}
`)

	write("apps/flow-api/internal/itemkit/delete.go", `package itemkit

func DeleteEvent(ctx Ctx, tx TX) error {
	_, _ = tx.ExecContext(ctx, "UPDATE calendar_events SET enabled = FALSE WHERE id = ?")
	return nil
}

// describesADelete performs UPDATE calendar_events SET enabled = FALSE in
// prose only, and must not be derived as a sink.
func describesADelete(ctx Ctx) error {
	return nil
}
`)

	write("apps/flow-api/internal/mcp/tools.go", `package mcp

import (
	"`+modulePath+`/internal/http/handlers/calendars"
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
	h.register(auth.FloorWorkspaceMember, tool{name: "rsvp_only", run: runRsvpOnly})
	h.register(auth.FloorWorkspaceMember, tool{name: "deletes", run: runDeletes})
}

func runApplies(q *Queries) {
	_ = calendars.DecideCalendarWrite("", "")
	_, _ = q.WriteContents(ctx, params)
}

func runSkips(q *Queries) {
	_, _ = q.WriteContents(ctx, params)
}

func runViaHelper(q *Queries) {
	checkWrite()
	_, _ = q.WriteContents(ctx, params)
}

func checkWrite() {
	_ = calendars.DecideCalendarWrite("", "")
}

// calendar-write-acl: not-applicable — the row it writes belongs to the
// caller alone and is not part of what the calendar shows its members
func runMarked(q *Queries) {
	_, _ = q.WriteContents(ctx, params)
}

// calendar-write-acl: not-applicable —
func runReasonless(q *Queries) {
	_, _ = q.WriteContents(ctx, params)
}

// calendar-write-acl: not-applicable — this one writes nothing a calendar
// shows and the marker covers nothing
func runStaleMarker(q *Queries) {
	_, _ = q.ReadContents(ctx, params)
}

// runMentionsInProse names calendars.DecideCalendarWrite in a sentence and
// calls nothing.
func runMentionsInProse(q *Queries) {
	_, _ = q.WriteContents(ctx, params)
}

func runSameNamedMethod(q *Queries, guard *Guard) {
	_ = guard.DecideCalendarWrite("", "")
	_, _ = q.WriteContents(ctx, params)
}

func runRsvpOnly(q *Queries) {
	_, _ = q.WriteOwnRsvp(ctx, params)
}

func runDeletes(q *Queries) {
	_ = itemkit.DeleteEvent(ctx, tx)
}
`)

	write("apps/flow-api/internal/http/router/router.go", `package router

import (
	"github.com/danielgtaylor/huma/v2"

	"`+modulePath+`/internal/http/handlers/calendars"
)

func build(api huma.API, deps Deps) {
	huma.Register(api, huma.Operation{OperationID: "contents-create"}, calendars.CreateEvent(deps))
	huma.Register(api, huma.Operation{OperationID: "contents-delete"}, calendars.DeleteEvent(deps))
	huma.Register(api, huma.Operation{OperationID: "rsvp"}, calendars.RsvpEvent(deps))
}
`)

	return root
}
