package recurrence

import (
	"errors"
	"testing"
	"time"
)

// TestValidateRule pins the grammar the write side enforces. Every case
// here is one the expander in this package has an answer for, so a rule
// that passes is a rule the calendar, the agent surface and the
// notification scheduler all read the same way.
func TestValidateRule(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		rule    []byte
		wantErr bool
	}{
		{"nil rule ok", nil, false},
		{"empty rule ok", []byte(``), false},
		{"null rule ok", []byte(`null`), false},
		{"valid daily", []byte(`{"freq":"daily","interval":1}`), false},
		{"valid weekly byDay", []byte(`{"freq":"weekly","interval":1,"byDay":["MO","WE"]}`), false},
		{"valid monthly byMonthDay", []byte(`{"freq":"monthly","interval":1,"byMonthDay":[15]}`), false},
		{"valid yearly", []byte(`{"freq":"yearly"}`), false},
		{"valid no interval", []byte(`{"freq":"daily"}`), false},
		{"valid until date", []byte(`{"freq":"daily","until":"2025-12-31"}`), false},
		{"valid until rfc3339", []byte(`{"freq":"daily","until":"2025-12-31T23:59:59Z"}`), false},
		{"valid count", []byte(`{"freq":"daily","count":10}`), false},
		{"interval at the upper bound", []byte(`{"freq":"daily","interval":999}`), false},
		{"count at the upper bound", []byte(`{"freq":"daily","count":1000}`), false},
		{"byMonthDay at the bounds", []byte(`{"freq":"monthly","byMonthDay":[1,31]}`), false},
		{"valid monthly nth weekday", []byte(`{"freq":"monthly","byDay":["MO"],"bySetPos":[2]}`), false},
		{"valid monthly last weekday", []byte(`{"freq":"monthly","byDay":["FR"],"bySetPos":[-1]}`), false},
		{"valid monthly fifth weekday", []byte(`{"freq":"monthly","byDay":["TH"],"bySetPos":[5]}`), false},
		{"valid wkst SU", []byte(`{"freq":"weekly","interval":2,"byDay":["TU","SU"],"wkst":"SU"}`), false},
		{"valid wkst MO", []byte(`{"freq":"weekly","interval":2,"byDay":["TU","SU"],"wkst":"MO"}`), false},
		{"byDay holding all seven weekdays", []byte(`{"freq":"weekly","byDay":["MO","TU","WE","TH","FR","SA","SU"]}`), false},

		// The CHECK refuses a member holding null, so a rule carrying one
		// cannot be stored however the decode reads it.
		{"null member rejected", []byte(`{"freq":"daily","interval":null}`), true},
		{"null until rejected", []byte(`{"freq":"daily","until":null}`), true},
		{"unknown member rejected", []byte(`{"freq":"daily","foo":1}`), true},
		{"integral float interval rejected", []byte(`{"freq":"daily","interval":1.0}`), true},
		{"fractional interval rejected", []byte(`{"freq":"daily","interval":1.5}`), true},
		{"exponent interval rejected", []byte(`{"freq":"daily","interval":1e1}`), true},
		{"boolean interval rejected", []byte(`{"freq":"daily","interval":true}`), true},
		{"count above the bound rejected", []byte(`{"freq":"daily","count":1001}`), true},
		{"count 2^31 rejected", []byte(`{"freq":"daily","count":2147483648}`), true},
		{"count 2^32 rejected", []byte(`{"freq":"daily","count":4294967296}`), true},
		{"scalar byMonthDay rejected", []byte(`{"freq":"monthly","byMonthDay":5}`), true},
		{"empty byMonthDay rejected", []byte(`{"freq":"monthly","byMonthDay":[]}`), true},
		{"repeated byMonthDay rejected", []byte(`{"freq":"monthly","byMonthDay":[5,5]}`), true},
		{"empty byDay rejected", []byte(`{"freq":"weekly","byDay":[]}`), true},
		{"repeated byDay rejected", []byte(`{"freq":"weekly","byDay":["MO","MO"]}`), true},
		{"scalar byDay rejected", []byte(`{"freq":"weekly","byDay":"MO"}`), true},
		{"until with count rejected", []byte(`{"freq":"daily","until":"2026-03-01","count":3}`), true},
		{"short until rejected", []byte(`{"freq":"daily","until":"2026-3-1"}`), true},
		{"non-ASCII until rejected", []byte(`{"freq":"daily","until":"２０２６-03-01"}`), true},
		{"lowercase wkst rejected", []byte(`{"freq":"weekly","wkst":"su"}`), true},
		{"other wkst rejected", []byte(`{"freq":"weekly","wkst":"TU"}`), true},

		// The combinations the CHECK leaves to the applications.
		{"byMonthDay on weekly rejected", []byte(`{"freq":"weekly","byMonthDay":[5]}`), true},
		{"byMonthDay on daily rejected", []byte(`{"freq":"daily","byMonthDay":[5]}`), true},
		{"byMonthDay on yearly rejected", []byte(`{"freq":"yearly","byMonthDay":[5]}`), true},
		{"byDay on daily rejected", []byte(`{"freq":"daily","byDay":["MO"]}`), true},
		{"byDay on yearly rejected", []byte(`{"freq":"yearly","byDay":["MO"]}`), true},
		{"bySetPos on weekly rejected", []byte(`{"freq":"weekly","byDay":["MO"],"bySetPos":[2]}`), true},
		{"monthly byDay without bySetPos rejected", []byte(`{"freq":"monthly","byDay":["MO"]}`), true},
		{"monthly bySetPos with two weekdays rejected", []byte(`{"freq":"monthly","byDay":["MO","TU"],"bySetPos":[2]}`), true},
		{"monthly byDay with byMonthDay rejected", []byte(`{"freq":"monthly","byDay":["MO"],"bySetPos":[2],"byMonthDay":[5]}`), true},

		{"malformed json rejected", []byte(`{not json`), true},
		{"array is not a rule object", []byte(`[{"freq":"daily"}]`), true},
		{"string is not a rule object", []byte(`"daily"`), true},
		{"wrong member type rejected", []byte(`{"freq":"daily","interval":"2"}`), true},
		{"uppercase freq rejected", []byte(`{"freq":"DAILY","interval":1}`), true},
		{"capitalized freq rejected", []byte(`{"freq":"Daily","interval":1}`), true},
		{"unknown freq rejected", []byte(`{"freq":"hourly","interval":1}`), true},
		{"empty freq rejected", []byte(`{"freq":"","interval":1}`), true},
		// A rule object with no freq is not "not recurring": the caller
		// sent a rule and meant something by it. The read side treats the
		// same stored value as non-recurring, which is why sending one
		// has to fail here rather than be quietly filed as no rule at
		// all. See TestReadSideToleratesWhatTheWriteSideRefuses.
		{"rule object with no freq rejected", []byte(`{}`), true},
		{"rule members without a freq rejected", []byte(`{"interval":2}`), true},
		{"interval zero rejected", []byte(`{"freq":"daily","interval":0}`), true},
		{"interval negative rejected", []byte(`{"freq":"daily","interval":-1}`), true},
		{"interval too large rejected", []byte(`{"freq":"daily","interval":1000}`), true},
		{"count zero rejected", []byte(`{"freq":"daily","count":0}`), true},
		{"count negative rejected", []byte(`{"freq":"daily","count":-1}`), true},
		{"count too large rejected", []byte(`{"freq":"daily","count":5000}`), true},
		{"bad byDay rejected", []byte(`{"freq":"weekly","byDay":["XX"]}`), true},
		// One unusable token among usable ones is still a rule nobody can
		// act on as written: the expander drops it and keeps the rest, so
		// "Monday and XX" quietly becomes "Monday".
		{"partially bad byDay rejected", []byte(`{"freq":"weekly","byDay":["MO","XX"]}`), true},
		{"byMonthDay zero rejected", []byte(`{"freq":"monthly","byMonthDay":[0]}`), true},
		{"byMonthDay out of range rejected", []byte(`{"freq":"monthly","byMonthDay":[40]}`), true},
		// RFC 5545 gives a negative BYMONTHDAY the meaning "counting back
		// from the end of the month". No expander implements that, and a
		// stored -1 matches no day at all, so the series silently holds
		// no occurrences.
		{"negative byMonthDay rejected", []byte(`{"freq":"monthly","byMonthDay":[-1]}`), true},
		// bySetPos is a one-item array of 1..5 or -1, and only beside byDay.
		{"scalar bySetPos rejected", []byte(`{"freq":"monthly","byDay":["MO"],"bySetPos":2}`), true},
		{"bySetPos zero rejected", []byte(`{"freq":"monthly","byDay":["MO"],"bySetPos":[0]}`), true},
		{"bySetPos minus two rejected", []byte(`{"freq":"monthly","byDay":["MO"],"bySetPos":[-2]}`), true},
		{"bySetPos too large rejected", []byte(`{"freq":"monthly","byDay":["MO"],"bySetPos":[6]}`), true},
		{"bySetPos with two positions rejected", []byte(`{"freq":"monthly","byDay":["MO"],"bySetPos":[1,2]}`), true},
		{"bySetPos without byDay rejected", []byte(`{"freq":"monthly","bySetPos":[2]}`), true},
		{"unparseable until rejected", []byte(`{"freq":"daily","until":"not-a-date"}`), true},
		{"unpadded until rejected", []byte(`{"freq":"daily","until":"2026-3-5"}`), true},
		{"impossible until rejected", []byte(`{"freq":"daily","until":"2026-02-30"}`), true},

		// The CHECK stores the uppercase codes only. The expanders still
		// read other spellings, but a write has to be one the CHECK takes.
		{"lowercase byDay rejected", []byte(`{"freq":"weekly","byDay":["mo"]}`), true},
		{"mixed case byDay rejected", []byte(`{"freq":"weekly","byDay":["Mo"]}`), true},
		{"padded byDay rejected", []byte(`{"freq":"weekly","byDay":[" MO "]}`), true},
		// UNTIL is a date or an RFC 3339 instant; a wall clock with no
		// offset is neither.
		{"until without an offset rejected", []byte(`{"freq":"daily","until":"2026-03-01T09:00:00"}`), true},
		{"until with an offset accepted", []byte(`{"freq":"daily","until":"2026-03-01T09:00:00+09:00"}`), false},
		{"empty until rejected", []byte(`{"freq":"daily","until":""}`), true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			err := ValidateRule(tt.rule)
			if (err != nil) != tt.wantErr {
				t.Fatalf("ValidateRule(%q) = %v, wantErr = %v", tt.rule, err, tt.wantErr)
			}
		})
	}
}

// TestValidateRuleNamesTheField checks that a rejection says which member
// is at fault, so a transport can report it without guessing.
func TestValidateRuleNamesTheField(t *testing.T) {
	t.Parallel()
	tests := []struct {
		rule  string
		field string
	}{
		{`{"freq":"hourly"}`, "freq"},
		{`{"freq":"daily","interval":0}`, "interval"},
		{`{"freq":"daily","count":0}`, "count"},
		{`{"freq":"weekly","byDay":["XX"]}`, "byDay"},
		{`{"freq":"monthly","byMonthDay":[40]}`, "byMonthDay"},
		{`{"freq":"monthly","bySetPos":[2]}`, "bySetPos"},
		{`{"freq":"daily","until":"not-a-date"}`, "until"},
		{`{not json`, "recurrenceRule"},
	}
	for _, tt := range tests {
		t.Run(tt.field, func(t *testing.T) {
			t.Parallel()
			err := ValidateRule([]byte(tt.rule))
			var invalidErr *InvalidRuleError
			if !errors.As(err, &invalidErr) {
				t.Fatalf("ValidateRule(%s) = %v, want an InvalidRuleError", tt.rule, err)
			}
			if invalidErr.Field != tt.field {
				t.Fatalf("field = %q, want %q", invalidErr.Field, tt.field)
			}
		})
	}
}

// TestValidateExceptions pins the accepted spellings to the ones
// parseExceptionEntry resolves, which is the same function expansion
// reads the list with. Anything the expander skips has to be refused
// here, because a skipped exception is an occurrence the caller was told
// was deleted and the calendar kept showing.
func TestValidateExceptions(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		value   []byte
		wantErr bool
	}{
		{"nil ok", nil, false},
		{"empty ok", []byte(``), false},
		{"null ok", []byte(`null`), false},
		{"empty array ok", []byte(`[]`), false},
		{"date only", []byte(`["2026-03-01"]`), false},
		{"rfc3339", []byte(`["2026-03-01T09:00:00Z"]`), false},
		{"rfc3339 with offset", []byte(`["2026-03-01T09:00:00+09:00"]`), false},
		{"naive local instant", []byte(`["2026-03-01T09:00:00"]`), false},
		{"mixed forms", []byte(`["2026-03-01","2026-03-08T09:00:00Z"]`), false},
		{"padded entry", []byte(`[" 2026-03-01 "]`), false},

		{"not an array", []byte(`{"a":1}`), true},
		{"array of numbers", []byte(`[1,2]`), true},
		{"malformed json", []byte(`[not json`), true},
		{"empty entry", []byte(`[""]`), true},
		{"impossible date", []byte(`["2026-02-30"]`), true},
		{"unpadded date the expander would skip", []byte(`["2026-3-1"]`), true},
		{"free text", []byte(`["next tuesday"]`), true},
		{"date with trailing junk", []byte(`["2026-03-01 morning"]`), true},
		{"epoch seconds as a string", []byte(`["1772000000"]`), true},
		{"one bad entry rejects the list", []byte(`["2026-03-01","tomorrow"]`), true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			err := ValidateExceptions(tt.value)
			if (err != nil) != tt.wantErr {
				t.Fatalf("ValidateExceptions(%q) = %v, wantErr = %v", tt.value, err, tt.wantErr)
			}
		})
	}
}

// TestValidateExceptionsMatchesTheExpander is the structural half of the
// pin above: every entry the validator accepts has to resolve to a
// suppression the expander applies, and every entry it refuses has to be
// one the expander could not have applied.
func TestValidateExceptionsMatchesTheExpander(t *testing.T) {
	t.Parallel()
	entries := []string{
		"2026-03-01",
		"2026-03-01T09:00:00Z",
		"2026-03-01T09:00:00+09:00",
		"2026-03-01T09:00:00",
		" 2026-03-01 ",
		"",
		"2026-02-30",
		"2026-3-1",
		"next tuesday",
		"2026-03-01 morning",
		"1772000000",
	}
	for _, entry := range entries {
		t.Run(entry, func(t *testing.T) {
			t.Parallel()
			_, resolved := parseExceptionEntry(entry, time.UTC)
			exact, days := buildExceptions([]string{entry}, time.UTC)
			applied := len(exact)+len(days) > 0
			if resolved != applied {
				t.Fatalf("parseExceptionEntry ok = %v but the expander applied = %v", resolved, applied)
			}
			accepted := ValidateExceptions([]byte(`["`+entry+`"]`)) == nil
			if accepted != applied {
				t.Fatalf("ValidateExceptions accepted = %v but the expander applied = %v", accepted, applied)
			}
		})
	}
}

// TestValidateRuleAcceptsGoldenFixtures holds the write gate to the rules
// the shared fixture already expands. A gate that refused one of them
// would be refusing a series every expander in the product agrees on.
func TestValidateRuleAcceptsGoldenFixtures(t *testing.T) {
	t.Parallel()
	for _, fixture := range loadGoldenFixtures(t) {
		t.Run(fixture.Name, func(t *testing.T) {
			t.Parallel()
			if err := fixture.Event.RecurrenceRule.Validate(); err != nil {
				t.Fatalf("Validate(%+v) = %v, want nil", fixture.Event.RecurrenceRule, err)
			}
			if err := ValidateExceptions(exceptionsJSON(fixture.Event.RecurrenceExceptions)); err != nil {
				t.Fatalf("ValidateExceptions(%v) = %v, want nil", fixture.Event.RecurrenceExceptions, err)
			}
		})
	}
}

func exceptionsJSON(values []string) []byte {
	if len(values) == 0 {
		return nil
	}
	out := []byte("[")
	for i, v := range values {
		if i > 0 {
			out = append(out, ',')
		}
		out = append(out, '"')
		out = append(out, v...)
		out = append(out, '"')
	}
	return append(out, ']')
}

// TestReadSideToleratesWhatTheWriteSideRefuses records the one place the
// two sides deliberately answer differently, so that the difference stays
// a decision rather than becoming a second grammar.
//
// Both sides agree that a rule object with no freq is not a rule. A write
// is refused, because the caller is there to correct it. A read of the
// same stored value yields no rule instead of an error, because expansion
// runs over every row on the notification path and failing one row would
// stop the scan for all of them.
func TestReadSideToleratesWhatTheWriteSideRefuses(t *testing.T) {
	t.Parallel()
	for _, raw := range []string{`{}`, `{"interval":2}`} {
		if err := ValidateRule([]byte(raw)); err == nil {
			t.Fatalf("ValidateRule(%s) = nil, want a rejection", raw)
		}
		rule, err := ParseRule([]byte(raw))
		if err != nil {
			t.Fatalf("ParseRule(%s) err = %v, want nil", raw, err)
		}
		if rule != nil {
			t.Fatalf("ParseRule(%s) = %+v, want nil", raw, rule)
		}
	}
}
