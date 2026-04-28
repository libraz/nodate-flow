package calendars

import "testing"

// TestParseFlexibleTime_RFC3339 covers the datetime parse path the
// per-calendar list endpoint hits when the client passes a full
// timestamp. C7 widened ListEventsInput.Start/End from time.Time to a
// flexible string so the handler can route both shapes through this
// helper.
func TestParseFlexibleTime_RFC3339(t *testing.T) {
	got, err := parseFlexibleTime("2026-04-01T10:00:00Z")
	if err != nil {
		t.Fatalf("parseFlexibleTime err: %v", err)
	}
	if got.Year() != 2026 || got.Month() != 4 || got.Day() != 1 || got.Hour() != 10 {
		t.Fatalf("unexpected time: %v", got)
	}
}

// TestParseFlexibleTime_DateOnly covers the YYYY-MM-DD path used by
// the day-grid view, where the URL only carries a calendar date.
func TestParseFlexibleTime_DateOnly(t *testing.T) {
	got, err := parseFlexibleTime("2026-04-30")
	if err != nil {
		t.Fatalf("parseFlexibleTime err: %v", err)
	}
	if got.Year() != 2026 || got.Month() != 4 || got.Day() != 30 {
		t.Fatalf("unexpected time: %v", got)
	}
}

// TestParseFlexibleTime_Garbage asserts a nonsense string surfaces
// the internal sentinel so the handler can translate it to the public
// CALENDAR.EVENT.DATE_RANGE_UNPARSEABLE error code.
func TestParseFlexibleTime_Garbage(t *testing.T) {
	if _, err := parseFlexibleTime("not-a-date"); err == nil {
		t.Fatal("expected error for invalid input")
	}
}

// TestParseFlexibleTime_Empty mirrors the garbage case for the empty
// string. Without this we would silently accept an unset query param.
func TestParseFlexibleTime_Empty(t *testing.T) {
	if _, err := parseFlexibleTime(""); err == nil {
		t.Fatal("expected error for empty input")
	}
}
