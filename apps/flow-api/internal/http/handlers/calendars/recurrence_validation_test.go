package calendars

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	apierrors "github.com/libraz/nodate-flow/apps/flow-api/internal/errors"
	"github.com/libraz/nodate-flow/packages/go-shared/recurrence"
)

// rawRule wraps a JSON literal in a *json.RawMessage for table tests.
func rawRule(s string) *json.RawMessage {
	m := json.RawMessage(s)
	return &m
}

// TestValidateRecurrenceRule covers the REST side of the recurrence
// grammar. The rules themselves are pinned in
// packages/go-shared/recurrence, which is the one place they are written
// down; what is checked here is that the write route asks that package
// and reports its verdict as a body-field rejection.
func TestValidateRecurrenceRule(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		rule    *json.RawMessage
		wantErr bool
	}{
		{"nil rule ok", nil, false},
		{"empty rule ok", rawRule(``), false},
		{"null rule ok", rawRule(`null`), false},
		{"valid daily", rawRule(`{"freq":"daily","interval":1}`), false},
		{"valid weekly byDay", rawRule(`{"freq":"weekly","interval":1,"byDay":["MO","WE"]}`), false},
		{"valid monthly byMonthDay", rawRule(`{"freq":"monthly","interval":1,"byMonthDay":[15]}`), false},
		{"valid yearly", rawRule(`{"freq":"yearly"}`), false},
		{"valid no interval", rawRule(`{"freq":"daily"}`), false},
		{"valid until date", rawRule(`{"freq":"daily","until":"2025-12-31"}`), false},
		{"valid until rfc3339", rawRule(`{"freq":"daily","until":"2025-12-31T23:59:59Z"}`), false},
		{"valid count", rawRule(`{"freq":"daily","count":10}`), false},

		{"malformed json rejected", rawRule(`{not json`), true},
		{"uppercase freq rejected", rawRule(`{"freq":"DAILY","interval":1}`), true},
		{"capitalized freq rejected", rawRule(`{"freq":"Daily","interval":1}`), true},
		{"unknown freq rejected", rawRule(`{"freq":"hourly","interval":1}`), true},
		{"empty freq rejected", rawRule(`{"freq":"","interval":1}`), true},
		{"interval zero rejected", rawRule(`{"freq":"daily","interval":0}`), true},
		{"interval negative rejected", rawRule(`{"freq":"daily","interval":-1}`), true},
		{"interval too large rejected", rawRule(`{"freq":"daily","interval":1000}`), true},
		{"count zero rejected", rawRule(`{"freq":"daily","count":0}`), true},
		{"count too large rejected", rawRule(`{"freq":"daily","count":5000}`), true},
		{"bad byDay rejected", rawRule(`{"freq":"weekly","byDay":["XX"]}`), true},
		// A lowercase weekday token used to be refused here while both
		// expanders resolved it, which put a rule inside the read grammar
		// and outside the write one. The token set is now read by the one
		// function the expanders use, and it lowercases before looking
		// up, so the route accepts what they act on.
		{"lowercase byDay accepted", rawRule(`{"freq":"weekly","byDay":["mo"]}`), false},
		{"byMonthDay zero rejected", rawRule(`{"freq":"monthly","byMonthDay":[0]}`), true},
		{"byMonthDay out of range rejected", rawRule(`{"freq":"monthly","byMonthDay":[40]}`), true},
		// No expander implements bySetPos, so accepting an in-range value
		// stored a rule that then expanded as if the selector were absent:
		// "second Monday" came back as every Monday, silently.
		{"bySetPos rejected", rawRule(`{"freq":"monthly","byDay":["MO"],"bySetPos":2}`), true},
		{"bySetPos zero rejected", rawRule(`{"freq":"monthly","byDay":["MO"],"bySetPos":0}`), true},
		{"bySetPos negative rejected", rawRule(`{"freq":"monthly","byDay":["MO"],"bySetPos":-1}`), true},
		{"bySetPos too large rejected", rawRule(`{"freq":"monthly","byDay":["MO"],"bySetPos":6}`), true},
		{"unparseable until rejected", rawRule(`{"freq":"daily","until":"not-a-date"}`), true},
		// An UNTIL without an offset used to be refused here while both
		// expanders read it as an instant in the event's own timezone,
		// and while the exception list on this same route accepted the
		// identical spelling.
		{"until without an offset accepted", rawRule(`{"freq":"daily","until":"2026-03-01T09:00:00"}`), false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := validateRecurrenceRule(tt.rule)
			if (got != nil) != tt.wantErr {
				t.Fatalf("validateRecurrenceRule(%v) = %v, wantErr = %v", tt.rule, got, tt.wantErr)
			}
			if got != nil && got != apierrors.ValidationBodyFieldInvalid {
				t.Fatalf("validateRecurrenceRule(%v) = %v, want ValidationBodyFieldInvalid", tt.rule, got)
			}
		})
	}
}

// TestValidateRecurrenceExceptions covers the exception list on the REST
// route. The accepted spellings are the ones the expander resolves; an
// entry it would skip has to be refused here, because a skipped exception
// is an occurrence the caller was told was deleted and the calendar kept
// showing.
func TestValidateRecurrenceExceptions(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		value   *json.RawMessage
		wantErr bool
	}{
		{"nil ok", nil, false},
		{"empty ok", rawRule(``), false},
		{"null ok", rawRule(`null`), false},
		{"empty array ok", rawRule(`[]`), false},
		{"date only", rawRule(`["2026-03-01"]`), false},
		{"rfc3339", rawRule(`["2026-03-01T09:00:00Z"]`), false},
		{"rfc3339 with offset", rawRule(`["2026-03-01T09:00:00+09:00"]`), false},
		{"naive local instant", rawRule(`["2026-03-01T09:00:00"]`), false},
		{"mixed forms", rawRule(`["2026-03-01","2026-03-08T09:00:00Z"]`), false},

		{"not an array", rawRule(`{"a":1}`), true},
		{"array of numbers", rawRule(`[1,2]`), true},
		{"malformed json", rawRule(`[not json`), true},
		{"empty entry", rawRule(`[""]`), true},
		{"impossible date", rawRule(`["2026-02-30"]`), true},
		{"unpadded date the expander would skip", rawRule(`["2026-3-1"]`), true},
		{"free text", rawRule(`["next tuesday"]`), true},
		{"date with trailing junk", rawRule(`["2026-03-01 morning"]`), true},
		{"epoch seconds as a string", rawRule(`["1772000000"]`), true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := validateRecurrenceExceptions(tt.value)
			if (got != nil) != tt.wantErr {
				t.Fatalf("validateRecurrenceExceptions(%v) = %v, wantErr = %v", tt.value, got, tt.wantErr)
			}
			if got != nil && got != apierrors.ValidationBodyFieldInvalid {
				t.Fatalf("validateRecurrenceExceptions(%v) = %v, want ValidationBodyFieldInvalid", tt.value, got)
			}
		})
	}
}

// TestBothTransportsAgreeOnTheGrammar is the guard against the rule set
// splitting in two again.
//
// A rule reaches the product through two entry points. The REST write
// route decides whether it may be stored; the agent surface reads the
// stored value back through the expander in packages/go-shared/
// recurrence and answers "what is on Tuesday" from it. When those two
// held separate copies of the grammar, a rule could be accepted by one
// and reinterpreted by the other, and the reinterpretation was silent:
// a member the expander could not use was dropped, and the series came
// back on the wrong days with nothing reporting a problem.
//
// Each row therefore states both answers. restAccepts is the write
// route's verdict. occurrences is what the expander actually produces
// for the same rule over one fixed window — for an accepted rule it is
// the series the rule names, and for a refused one it is the wrong
// answer the route would have shipped had it stored the value. A change
// to either side that does not move the other fails here.
func TestBothTransportsAgreeOnTheGrammar(t *testing.T) {
	t.Parallel()

	// One Monday anchor and a three-week window, so a weekday-sensitive
	// rule and a count-sensitive one both show their shape.
	anchor := time.Date(2026, 3, 2, 9, 0, 0, 0, time.UTC)
	rangeStart := time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC)
	rangeEnd := time.Date(2026, 3, 20, 0, 0, 0, 0, time.UTC)

	tests := []struct {
		name string
		rule string
		// restAccepts is the REST write route's verdict.
		restAccepts bool
		// occurrences is what the expander yields, as YYYY-MM-DD.
		occurrences []string
		// note explains, for a refused rule, what the expander does with
		// it instead of what the rule says.
		note string
	}{
		{
			name:        "daily",
			rule:        `{"freq":"daily","interval":1,"count":3}`,
			restAccepts: true,
			occurrences: []string{"2026-03-02", "2026-03-03", "2026-03-04"},
		},
		{
			name:        "weekly on listed weekdays",
			rule:        `{"freq":"weekly","byDay":["MO","WE"],"count":4}`,
			restAccepts: true,
			occurrences: []string{"2026-03-02", "2026-03-04", "2026-03-09", "2026-03-11"},
		},
		{
			name:        "weekly on a lowercase weekday",
			rule:        `{"freq":"weekly","byDay":["mo"],"count":2}`,
			restAccepts: true,
			occurrences: []string{"2026-03-02", "2026-03-09"},
		},
		{
			name:        "until as a bare date",
			rule:        `{"freq":"daily","until":"2026-03-04"}`,
			restAccepts: true,
			occurrences: []string{"2026-03-02", "2026-03-03", "2026-03-04"},
		},
		{
			name:        "until without an offset",
			rule:        `{"freq":"daily","until":"2026-03-04T09:00:00"}`,
			restAccepts: true,
			occurrences: []string{"2026-03-02", "2026-03-03", "2026-03-04"},
		},
		{
			name:        "monthly on a listed day of the month",
			rule:        `{"freq":"monthly","byMonthDay":[2],"count":1}`,
			restAccepts: true,
			occurrences: []string{"2026-03-02"},
		},
		{
			name:        "unknown weekday token",
			rule:        `{"freq":"weekly","byDay":["XX"],"count":3}`,
			restAccepts: false,
			occurrences: []string{"2026-03-02", "2026-03-09", "2026-03-16"},
			note:        "the token is dropped and the rule falls back to the anchor's own weekday",
		},
		{
			name:        "one unknown weekday token among known ones",
			rule:        `{"freq":"weekly","byDay":["MO","XX"],"count":3}`,
			restAccepts: false,
			occurrences: []string{"2026-03-02", "2026-03-09", "2026-03-16"},
			note:        "the unusable token is dropped and the rest of the list stands",
		},
		{
			name:        "interval below one",
			rule:        `{"freq":"daily","interval":0,"count":3}`,
			restAccepts: false,
			occurrences: []string{"2026-03-02", "2026-03-03", "2026-03-04"},
			note:        "no cadence is named, so the expander substitutes one",
		},
		{
			name:        "count below one",
			rule:        `{"freq":"daily","count":0}`,
			restAccepts: false,
			occurrences: nil,
			note:        "a series that exists and never occurs",
		},
		{
			name:        "negative count",
			rule:        `{"freq":"daily","count":-1}`,
			restAccepts: false,
			occurrences: []string{"2026-03-02", "2026-03-03", "2026-03-04", "2026-03-05", "2026-03-06", "2026-03-07", "2026-03-08", "2026-03-09", "2026-03-10", "2026-03-11", "2026-03-12", "2026-03-13", "2026-03-14", "2026-03-15", "2026-03-16", "2026-03-17", "2026-03-18", "2026-03-19"},
			note:        "the bound is discarded and the series never ends",
		},
		{
			name:        "byMonthDay outside the month",
			rule:        `{"freq":"monthly","byMonthDay":[40]}`,
			restAccepts: false,
			occurrences: nil,
			note:        "no day matches, so the series holds nothing",
		},
		{
			name:        "byMonthDay counting back from the end of the month",
			rule:        `{"freq":"monthly","byMonthDay":[-1]}`,
			restAccepts: false,
			occurrences: nil,
			note:        "the RFC 5545 meaning is not implemented and no day matches",
		},
		{
			name:        "unparseable until",
			rule:        `{"freq":"daily","until":"not-a-date"}`,
			restAccepts: false,
			occurrences: []string{"2026-03-02", "2026-03-03", "2026-03-04", "2026-03-05", "2026-03-06", "2026-03-07", "2026-03-08", "2026-03-09", "2026-03-10", "2026-03-11", "2026-03-12", "2026-03-13", "2026-03-14", "2026-03-15", "2026-03-16", "2026-03-17", "2026-03-18", "2026-03-19"},
			note:        "the end date is discarded and the series runs past it",
		},
		{
			name:        "bySetPos",
			rule:        `{"freq":"monthly","byDay":["MO"],"bySetPos":2}`,
			restAccepts: false,
			occurrences: []string{"2026-03-02"},
			note:        "the selector is not applied, so every matching Monday stands",
		},
		{
			name:        "freq the expander cannot step",
			rule:        `{"freq":"hourly"}`,
			restAccepts: false,
			occurrences: nil,
			note:        "nothing steps the series, so the event drops off the agent surface",
		},
		{
			name:        "rule object with no freq",
			rule:        `{"interval":2}`,
			restAccepts: false,
			occurrences: nil,
			note:        "read back as no rule at all, so the event is silently one-off",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			raw := json.RawMessage(tt.rule)
			accepted := validateRecurrenceRule(&raw) == nil
			if accepted != tt.restAccepts {
				t.Fatalf("REST route accepted = %v, want %v", accepted, tt.restAccepts)
			}

			rule, err := recurrence.ParseRule([]byte(tt.rule))
			var got []string
			if err == nil && rule != nil {
				for _, occ := range recurrence.Expand(recurrence.Event{
					StartAt: anchor,
					EndAt:   anchor.Add(time.Hour),
					Rule:    rule,
				}, rangeStart, rangeEnd) {
					got = append(got, occ.StartAt.UTC().Format("2006-01-02"))
				}
			}
			if len(got) != len(tt.occurrences) {
				t.Fatalf("expander produced %d occurrences %v, want %d %v",
					len(got), got, len(tt.occurrences), tt.occurrences)
			}
			for i := range got {
				if got[i] != tt.occurrences[i] {
					t.Fatalf("occurrence %d = %s, want %s", i, got[i], tt.occurrences[i])
				}
			}

			// The invariant the two columns exist to state: a rule the
			// write route accepts is one the expander acts on as
			// written, and a rule it refuses is one the expander would
			// have answered differently from what was asked for. The
			// note names that difference.
			if !tt.restAccepts && tt.note == "" {
				t.Fatal("a refused rule has to record what the expander does with it instead")
			}
		})
	}
}

// TestValidateRecurrenceRule_GoldenFixtures holds the write route to the
// rules the shared fixture expands. A gate that refused one of them would
// be refusing a series every expander in the product agrees on.
func TestValidateRecurrenceRule_GoldenFixtures(t *testing.T) {
	t.Parallel()
	for _, fx := range loadRecurrenceValidationGolden(t) {
		t.Run(fx.Name, func(t *testing.T) {
			t.Parallel()
			raw, err := json.Marshal(fx.Event.RecurrenceRule)
			if err != nil {
				t.Fatalf("marshal recurrence rule: %v", err)
			}
			msg := json.RawMessage(raw)
			if got := validateRecurrenceRule(&msg); got != nil {
				t.Fatalf("validateRecurrenceRule(%s) = %v, want nil", string(raw), got)
			}
		})
	}
}

type recurrenceValidationGoldenFixture struct {
	Name  string `json:"name"`
	Event struct {
		RecurrenceRule recurrence.Rule `json:"recurrenceRule"`
	} `json:"event"`
}

func loadRecurrenceValidationGolden(t *testing.T) []recurrenceValidationGoldenFixture {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("could not resolve current test file")
	}
	dir := filepath.Dir(file)
	for {
		candidate := filepath.Join(dir, "testdata", "recurrence_golden.json")
		if b, err := os.ReadFile(candidate); err == nil { //#nosec G304 -- repository path
			var out []recurrenceValidationGoldenFixture
			if err := json.Unmarshal(b, &out); err != nil {
				t.Fatalf("decode %s: %v", candidate, err)
			}
			return out
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("could not find testdata/recurrence_golden.json")
		}
		dir = parent
	}
}
