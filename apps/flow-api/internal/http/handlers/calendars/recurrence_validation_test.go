package calendars

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

// rawRule wraps a JSON literal in a *json.RawMessage for table tests.
func rawRule(s string) *json.RawMessage {
	m := json.RawMessage(s)
	return &m
}

func TestValidateRecurrenceRule(t *testing.T) {
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
		{"lowercase byDay rejected", rawRule(`{"freq":"weekly","byDay":["mo"]}`), true},
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
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := validateRecurrenceRule(tt.rule)
			if (got != nil) != tt.wantErr {
				t.Fatalf("validateRecurrenceRule(%v) = %v, wantErr = %v", tt.rule, got, tt.wantErr)
			}
		})
	}
}

// TestValidateRecurrenceExceptions pins the accepted spellings to the ones
// buildExceptions in packages/go-shared/recurrence resolves. Anything the
// expander skips has to be refused here, because a skipped exception is an
// occurrence the client reported as deleted and the calendar kept showing.
func TestValidateRecurrenceExceptions(t *testing.T) {
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
			got := validateRecurrenceExceptions(tt.value)
			if (got != nil) != tt.wantErr {
				t.Fatalf("validateRecurrenceExceptions(%v) = %v, wantErr = %v", tt.value, got, tt.wantErr)
			}
		})
	}
}

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
		RecurrenceRule recurrenceRulePayload `json:"recurrenceRule"`
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
		if b, err := os.ReadFile(candidate); err == nil {
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
