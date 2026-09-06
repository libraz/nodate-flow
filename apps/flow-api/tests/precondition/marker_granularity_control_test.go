package precondition

import (
	"os"
	"path/filepath"
	"sort"
	"testing"
)

// The checks in this file are the control for the granularity of an
// exemption: what one marker covers.
//
// A marker is written about a write, having looked at it. That is what
// makes it worth reading, and it is also what bounds it — the reason says
// nothing about a write nobody had seen. A check that answered per entry
// rather than per write would let the second sink added to an entry ride
// on the reason written for the first, and it would report a decision
// nobody made, silently, because the finding it would otherwise raise is
// the only thing that reports the new write at all.
//
// So the fixture below writes the rule's columns twice from one entry, and
// pins what has to happen: two writes need two reasons, a reason that does
// not say which write it is about says nothing once there are two, and a
// reason naming a write the entry does not make is reported rather than
// ignored.

// granularityRule is the rule the fixture is checked against. It names
// only symbols the fixture declares, so the control keeps working when the
// real rules change.
var granularityRule = Rule{
	Name:      "chronology",
	Table:     "calendar_events",
	Columns:   []string{"start_at"},
	Enforcers: []string{modulePath + "/internal/calendarrules.RequireEventChronology"},
	Marker:    "calendar-precondition",
	Why:       "the fixture rule",
}

// granularitySQL declares two statements that write the rule's column, so
// one entry can reach both.
const granularitySQL = `-- name: WriteWindow :execlastid
INSERT INTO calendar_events (
  public_id,
  start_at
) VALUES (?, ?);

-- name: PatchWindow :execrows
UPDATE calendar_events
SET start_at = ?
WHERE public_id = ?;
`

// TestOneMarkerCoversOneWrite pins the pairing between markers and sinks.
//
// It drives one tree holding, per entry: two sinks and no marker, two
// sinks under one marker naming neither, two sinks with a marker for each,
// two sinks with a marker for one, a marker naming a sink the entry does
// not reach, two markers for the same sink, and — the case the granularity
// exists for — an entry whose single sink is covered by a marker naming
// none, which stays covered.
func TestOneMarkerCoversOneWrite(t *testing.T) {
	t.Parallel()

	root := writeGranularityTree(t)
	src, err := Parse(root)
	if err != nil {
		t.Fatalf("parse control tree: %v", err)
	}

	entries := map[string]Entry{}
	for _, e := range src.Entries {
		entries[e.Name] = e
	}
	for _, want := range []string{
		"two_sinks_bare", "two_sinks_one_bare_marker", "two_sinks_two_markers",
		"two_sinks_one_marker", "marker_names_a_stranger", "two_markers_one_sink",
		"one_sink_bare_marker",
	} {
		if _, ok := entries[want]; !ok {
			t.Fatalf("the control tree did not yield entry %q; the registry walk is not reading the fixture", want)
		}
	}

	statements := parseQueryFile("sql/queries/granularity.sql", granularitySQL)
	if len(statements) != 2 {
		t.Fatalf("the fixture parsed into %d statements, want 2", len(statements))
	}
	sinks := Sinks(src, statements, granularityRule)
	if len(sinks) != 2 {
		t.Fatalf("the fixture derived %d sinks, want 2; one entry cannot reach two of them", len(sinks))
	}

	findings, _ := Check(src, statements, []Rule{granularityRule})

	type reported struct {
		entry  string
		via    string
		reason string
		kind   FindingKind
	}
	var got []reported
	for _, f := range findings {
		got = append(got, reported{
			entry:  f.Entry.Name,
			via:    f.Via.Name,
			reason: f.Reason,
			kind:   f.Kind,
		})
	}
	sort.Slice(got, func(i, j int) bool {
		if got[i].entry != got[j].entry {
			return got[i].entry < got[j].entry
		}
		if got[i].kind != got[j].kind {
			return got[i].kind < got[j].kind
		}
		return got[i].via < got[j].via
	})

	// two_sinks_two_markers and one_sink_bare_marker are absent, and their
	// absence is half the evidence: a rule that reported every marked
	// entry would be no more granular than one that reported none.
	want := []reported{
		// A marker naming a write the entry does not make exempts nothing,
		// and the write it does make is still unanswered.
		{entry: "marker_names_a_stranger", via: "PatchWindow", kind: Unenforced},
		{entry: "marker_names_a_stranger", reason: markerNamesNoSink, kind: StaleMarker},
		// A second reason for a write already exempted stands for no
		// decision, and the other write is still unanswered.
		{entry: "two_markers_one_sink", via: "WriteWindow", kind: Unenforced},
		{entry: "two_markers_one_sink", reason: markerRepeatsAnExemption, kind: StaleMarker},
		// Two writes, no reason for either.
		{entry: "two_sinks_bare", via: "PatchWindow", kind: Unenforced},
		{entry: "two_sinks_bare", via: "WriteWindow", kind: Unenforced},
		// One reason over two writes does not say which it is about, so it
		// covers neither and both are still unanswered.
		{entry: "two_sinks_one_bare_marker", via: "PatchWindow", kind: Unenforced},
		{entry: "two_sinks_one_bare_marker", via: "WriteWindow", kind: Unenforced},
		{entry: "two_sinks_one_bare_marker", reason: markerIsAmbiguous, kind: StaleMarker},
		// A reason for one of the two writes leaves the other reported,
		// which is what makes adding a sink fail until it is named.
		{entry: "two_sinks_one_marker", via: "WriteWindow", kind: Unenforced},
	}
	if len(got) != len(want) {
		t.Fatalf("the scan reported %+v, want %+v", got, want)
	}
	for i := range got {
		if got[i] != want[i] {
			t.Errorf("finding %d is %+v, want %+v", i, got[i], want[i])
		}
	}
}

// writeGranularityTree lays out a minimal apps/flow-api/internal tree
// whose entries write the rule's column once or twice, and returns the
// root [Parse] would be given.
func writeGranularityTree(t *testing.T) string {
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

	write("apps/flow-api/internal/mcp/tools.go", `package mcp

func (h *Handler) registerAll() {
	h.register(auth.FloorWorkspaceMember, tool{name: "two_sinks_bare", run: runTwoSinksBare})
	h.register(auth.FloorWorkspaceMember, tool{name: "two_sinks_one_bare_marker", run: runTwoSinksOneBareMarker})
	h.register(auth.FloorWorkspaceMember, tool{name: "two_sinks_two_markers", run: runTwoSinksTwoMarkers})
	h.register(auth.FloorWorkspaceMember, tool{name: "two_sinks_one_marker", run: runTwoSinksOneMarker})
	h.register(auth.FloorWorkspaceMember, tool{name: "marker_names_a_stranger", run: runMarkerNamesAStranger})
	h.register(auth.FloorWorkspaceMember, tool{name: "two_markers_one_sink", run: runTwoMarkersOneSink})
	h.register(auth.FloorWorkspaceMember, tool{name: "one_sink_bare_marker", run: runOneSinkBareMarker})
}

func runTwoSinksBare(q *Queries) {
	_, _ = q.WriteWindow(ctx, params)
	_, _ = q.PatchWindow(ctx, params)
}

// calendar-precondition: chronology not-applicable — one reason written
// over two writes, which does not say which of them it is about
func runTwoSinksOneBareMarker(q *Queries) {
	_, _ = q.WriteWindow(ctx, params)
	_, _ = q.PatchWindow(ctx, params)
}

// calendar-precondition: chronology not-applicable for WriteWindow — the
// window is derived from a stored row rather than taken from the caller
// calendar-precondition: chronology not-applicable for PatchWindow — the
// patch carries the stored window through unchanged
func runTwoSinksTwoMarkers(q *Queries) {
	_, _ = q.WriteWindow(ctx, params)
	_, _ = q.PatchWindow(ctx, params)
}

// calendar-precondition: chronology not-applicable for PatchWindow — the
// patch carries the stored window through unchanged, and nothing here
// accounts for the insert beside it
func runTwoSinksOneMarker(q *Queries) {
	_, _ = q.WriteWindow(ctx, params)
	_, _ = q.PatchWindow(ctx, params)
}

// calendar-precondition: chronology not-applicable for WriteWindow — this
// names a write the entry does not make
func runMarkerNamesAStranger(q *Queries) {
	_, _ = q.PatchWindow(ctx, params)
}

// calendar-precondition: chronology not-applicable for PatchWindow — the
// first reason for this write
// calendar-precondition: chronology not-applicable for PatchWindow — the
// second reason for the same write, which settles nothing new
func runTwoMarkersOneSink(q *Queries) {
	_, _ = q.WriteWindow(ctx, params)
	_, _ = q.PatchWindow(ctx, params)
}

// calendar-precondition: chronology not-applicable — one write and one
// reason, where there is nothing for a name to distinguish
func runOneSinkBareMarker(q *Queries) {
	_, _ = q.WriteWindow(ctx, params)
}
`)

	return root
}
