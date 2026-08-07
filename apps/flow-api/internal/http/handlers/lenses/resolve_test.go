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

func TestParseLensFilterIgnoresUnknownKeys(t *testing.T) {
	f := parseLensFilter(json.RawMessage(`{"colour":{"value":"blue"}}`))
	if f.Impossible {
		t.Fatal("a key outside the grammar is not a contradiction")
	}
	if where, _ := f.fragments(); len(where) != 0 {
		t.Fatalf("an unreadable key must add no predicate; got %v", where)
	}
}
