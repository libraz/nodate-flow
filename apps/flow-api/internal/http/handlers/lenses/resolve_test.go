package lenses

import (
	"encoding/json"
	"strings"
	"testing"
)

// The public share page is unauthenticated, so a filter read more
// loosely than it was written puts tasks nobody selected in front of
// anyone holding the URL. These cases pin the two loosenings that were
// there: a multi-value status reduced to its first entry, and a set of
// priorities widened into the range that spans them.

func TestParseLensFilterKeepsEveryStatusNamed(t *testing.T) {
	f := parseLensFilter(json.RawMessage(`{"status":{"values":["open","done"]}}`))
	if got := strings.Join(f.States, ","); got != "open,done" {
		t.Fatalf("every named status must survive parsing; got %q", got)
	}
}

func TestParseLensFilterKeepsPrioritiesAsASet(t *testing.T) {
	f := parseLensFilter(json.RawMessage(`{"priority":{"values":[1,4]}}`))
	if len(f.Priorities) != 2 || f.Priorities[0] != 1 || f.Priorities[1] != 4 {
		t.Fatalf("a priority set must stay a set; got %v", f.Priorities)
	}
	if f.PriorityMin != nil || f.PriorityMax != nil {
		t.Fatalf("a priority set must not become a range; got min=%v max=%v", f.PriorityMin, f.PriorityMax)
	}

	where, args := f.fragments()
	joined := strings.Join(where, " AND ")
	if !strings.Contains(joined, "v.priority IN (?,?)") {
		t.Fatalf("the rendered predicate must test membership; got %q", joined)
	}
	if strings.Contains(joined, "v.priority >=") || strings.Contains(joined, "v.priority <=") {
		t.Fatalf("the rendered predicate must not bracket the set; got %q", joined)
	}
	if len(args) != 2 {
		t.Fatalf("expected one bind per named priority; got %d", len(args))
	}
}

func TestParseLensFilterHonoursComparisonShape(t *testing.T) {
	f := parseLensFilter(json.RawMessage(`{"priority":{"gte":3}}`))
	if f.PriorityMin == nil || *f.PriorityMin != 3 {
		t.Fatalf("gte must still bracket the column; got %v", f.PriorityMin)
	}
	if len(f.Priorities) != 0 {
		t.Fatalf("a comparison is not a set; got %v", f.Priorities)
	}
}

func TestParseLensFilterRendersTheKnobsItReads(t *testing.T) {
	f := parseLensFilter(json.RawMessage(
		`{"assignee":{"value":"019649b0-0000-7000-8000-000000000000"},"search":{"value":"quarterly"}}`))
	if f.Assignee == "" {
		t.Fatal("an assignee filter must be read")
	}
	if f.Search != "quarterly" {
		t.Fatalf("a search filter must be read; got %q", f.Search)
	}

	where, args := f.fragments()
	joined := strings.Join(where, " AND ")
	if !strings.Contains(joined, "ta.role = 'assignee'") {
		t.Fatalf("the assignee filter must reach SQL; got %q", joined)
	}
	if !strings.Contains(joined, "LOWER(v.title) LIKE ?") {
		t.Fatalf("the search filter must reach SQL; got %q", joined)
	}
	if len(args) != 2 {
		t.Fatalf("expected one bind per rendered knob; got %d", len(args))
	}
}

// A filter naming something no task can match selects nothing. Dropping
// it instead would widen the shared set, which is the direction that
// leaks.
func TestParseLensFilterFailsClosedOnUnmatchableValues(t *testing.T) {
	for name, raw := range map[string]string{
		"unknown status":     `{"status":{"values":["shipped"]}}`,
		"malformed assignee": `{"assignee":{"value":"not-a-uuid"}}`,
	} {
		f := parseLensFilter(json.RawMessage(raw))
		if !f.Impossible {
			t.Fatalf("%s: filter must select nothing, got %+v", name, f)
		}
		where, _ := f.fragments()
		if strings.Join(where, " AND ") != "1 = 0" {
			t.Fatalf("%s: predicate must exclude everything; got %v", name, where)
		}
	}
}

// A filter this reader cannot render in full selects nothing. Rendering
// only the part it understands drops predicates, and a dropped predicate
// on an unauthenticated page publishes tasks the lens never named.
func TestParseLensFilterFailsClosedOnAnythingItCannotRender(t *testing.T) {
	for name, raw := range map[string]string{
		"unreadable blob":                                 `{"status":"open"}`,
		"not an object at all":                            `["status"]`,
		"key outside the grammar":                         `{"colour":{"value":"blue"}}`,
		"grammar key this reader does not implement":      `{"labels":{"in":["urgent"]}}`,
		"grammar operator this reader does not implement": `{"status":{"neq":"done"}}`,
		"key carrying no operator":                        `{"status":{}}`,
		"empty status set":                                `{"status":{"values":[]}}`,
		"empty priority set":                              `{"priority":{"in":[]}}`,
		"priority set mixed with a comparison":            `{"priority":{"values":[1,2],"gte":4}}`,
		"priority bounded twice from below":               `{"priority":{"gt":1,"gte":3}}`,
		"due date that is not a date":                     `{"due_on":{"gte":"next tuesday"}}`,
		"assignee naming nobody":                          `{"assignee":{"value":""}}`,
	} {
		f := parseLensFilter(json.RawMessage(raw))
		if !f.Impossible {
			t.Fatalf("%s: filter must select nothing, got %+v", name, f)
		}
		where, _ := f.fragments()
		if strings.Join(where, " AND ") != "1 = 0" {
			t.Fatalf("%s: predicate must exclude everything; got %v", name, where)
		}
	}
}

// The counterweight to the cases above: a filter this reader does render
// still narrows rather than excluding, and an empty filter still means
// "everything in the lens's own scope".
func TestParseLensFilterStillReadsWhatItImplements(t *testing.T) {
	for name, raw := range map[string]string{
		"status set":            `{"status":{"values":["open","done"]}}`,
		"status eq":             `{"status":{"eq":"open"}}`,
		"priority set":          `{"priority":{"in":[1,4]}}`,
		"priority range":        `{"priority":{"gte":1,"lte":3}}`,
		"due date bracket":      `{"due_on":{"gte":"2026-01-01","lte":"2026-12-31"}}`,
		"due date pinned":       `{"due_on":{"eq":"2026-01-01"}}`,
		"status plus search":    `{"status":{"in":["open"]},"search":{"value":"quarterly"}}`,
		"unknown state dropped": `{"status":{"values":["open","shipped"]}}`,
	} {
		f := parseLensFilter(json.RawMessage(raw))
		if f.Impossible {
			t.Fatalf("%s: a filter this reader implements must not be impossible", name)
		}
		if where, _ := f.fragments(); len(where) == 0 {
			t.Fatalf("%s: filter must reach SQL as a predicate", name)
		}
	}

	empty := parseLensFilter(json.RawMessage(`{}`))
	if empty.Impossible {
		t.Fatal("a lens with no filter names its whole scope, not nothing")
	}
	if where, _ := empty.fragments(); len(where) != 0 {
		t.Fatalf("an empty filter must add no predicate; got %v", where)
	}
}

// The write-time refusal and the read-time fail-closed are the same
// reading, so what the reader cannot render is exactly what a lens may
// not be written in, and the refusal can name where it stopped.
func TestReadLensFilterNamesWhatStoppedIt(t *testing.T) {
	for raw, want := range map[string]string{
		`{"state":"open"}`:                  "filter",
		`{"labels":{"in":["urgent"]}}`:      "filter.labels",
		`{"status":{"neq":"done"}}`:         "filter.status.neq",
		`{"status":{"values":["shipped"]}}`: "filter.status",
		`{"priority":{"in":[]}}`:            "filter.priority",
		`{"due_on":{"gte":"soon"}}`:         "filter.due_on",
		`{"status":{"values":["open"]}}`:    "",
		`{}`:                                "",
	} {
		f, unread := readLensFilter(json.RawMessage(raw))
		if unread != want {
			t.Fatalf("%s: expected the reading to stop at %q; got %q", raw, want, unread)
		}
		if unread != "" && !parseLensFilter(json.RawMessage(raw)).Impossible {
			t.Fatalf("%s: a reading that stopped must select nothing", raw)
		}
		if unread == "" && f.Impossible {
			t.Fatalf("%s: a complete reading must not be impossible", raw)
		}
	}
}

// publicLensFilter reads the stored envelope, so a lens_json that cannot
// be decoded at all must reach the resolver as the excluding predicate
// rather than as no filter.
func TestPublicLensFilterFailsClosedOnUnreadableLensJSON(t *testing.T) {
	for name, raw := range map[string]string{
		"truncated blob": `{"filter":`,
		"not an object":  `42`,
		"filter of wrong shape": `{"filter":{"state":"open"},` +
			`"sort":[],"groupBy":null}`,
	} {
		f := publicLensFilter(json.RawMessage(raw))
		if !f.Impossible {
			t.Fatalf("%s: an unreadable lens must select nothing, got %+v", name, f)
		}
	}

	f := publicLensFilter(json.RawMessage(`{"filter":{"status":{"values":["open"]}},"sort":[],"groupBy":null}`))
	if f.Impossible || len(f.States) != 1 || f.States[0] != "open" {
		t.Fatalf("a readable lens must still resolve its filter; got %+v", f)
	}
}
