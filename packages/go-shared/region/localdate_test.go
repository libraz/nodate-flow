package region

import (
	"testing"
	"time"
)

func TestLocalDateReadsTheDateInTheGivenZone(t *testing.T) {
	// 2026-08-11 08:00 in Tokyo is 2026-08-10 23:00 UTC. Both are the
	// same instant; only one of them is the day the meeting is on.
	instant := time.Date(2026, 8, 10, 23, 0, 0, 0, time.UTC)

	cases := []struct {
		tz   string
		want string
	}{
		{"Asia/Tokyo", "2026-08-11"},
		{"UTC", "2026-08-10"},
		// A negative offset pushes the other way: still the 10th in UTC,
		// already the 10th in Los Angeles but at 16:00.
		{"America/Los_Angeles", "2026-08-10"},
	}
	for _, c := range cases {
		got, err := LocalDateString(instant, c.tz)
		if err != nil {
			t.Fatalf("LocalDateString(%s): %v", c.tz, err)
		}
		if got != c.want {
			t.Errorf("LocalDateString(%s) = %s, want %s", c.tz, got, c.want)
		}
	}
}

func TestLocalDateCrossesTheDayLineFromTheOtherSide(t *testing.T) {
	// 2026-08-10 20:00 Los Angeles is 2026-08-11 03:00 UTC — the UTC
	// reading is a day ahead rather than a day behind, so a fix that
	// merely subtracts a day would break this case.
	instant := time.Date(2026, 8, 11, 3, 0, 0, 0, time.UTC)
	got, err := LocalDateString(instant, "America/Los_Angeles")
	if err != nil {
		t.Fatalf("LocalDateString: %v", err)
	}
	if got != "2026-08-10" {
		t.Errorf("LocalDateString = %s, want 2026-08-10", got)
	}
}

func TestLocalDateIsCarriedAsMidnightUTC(t *testing.T) {
	// The carrier is what makes the value survive the MySQL driver,
	// which converts a time.Time parameter into its configured location
	// before formatting it. Midnight in Asia/Tokyo would be stored as
	// the previous day.
	d, err := LocalDate(time.Date(2026, 8, 10, 23, 0, 0, 0, time.UTC), "Asia/Tokyo")
	if err != nil {
		t.Fatalf("LocalDate: %v", err)
	}
	if d.Location() != time.UTC {
		t.Errorf("location = %v, want UTC", d.Location())
	}
	if h, m, s := d.Clock(); h != 0 || m != 0 || s != 0 {
		t.Errorf("clock = %02d:%02d:%02d, want midnight", h, m, s)
	}
}

func TestLocalDateRejectsAnUnknownZone(t *testing.T) {
	if _, err := LocalDate(time.Now(), "Mars/Olympus"); err == nil {
		t.Fatal("expected an error for an unknown zone")
	}
	// Empty is rejected too: callers are meant to run the name through
	// EffectiveTimezone, and silently answering UTC here is the bug this
	// helper exists to prevent.
	if _, err := LocalDate(time.Now(), ""); err == nil {
		t.Fatal("expected an error for an empty zone")
	}
	if got := EffectiveTimezone(""); got != DefaultTimezone {
		t.Errorf("EffectiveTimezone(\"\") = %q, want %q", got, DefaultTimezone)
	}
}

func TestLoadLocationCachesBothOutcomes(t *testing.T) {
	a, err := LoadLocation("Asia/Tokyo")
	if err != nil {
		t.Fatalf("LoadLocation: %v", err)
	}
	b, err := LoadLocation("Asia/Tokyo")
	if err != nil {
		t.Fatalf("LoadLocation (cached): %v", err)
	}
	if a != b {
		t.Error("expected the cached call to return the same *time.Location")
	}
	if _, err := LoadLocation("Mars/Olympus"); err == nil {
		t.Fatal("expected an error")
	}
	if _, err := LoadLocation("Mars/Olympus"); err == nil {
		t.Fatal("expected the cached error to still be an error")
	}
}

func TestLocalDayDeltaCountsCalendarDaysNotHours(t *testing.T) {
	tokyo := "Asia/Tokyo"
	// 08:00 → 20:00 on the same Tokyo day. The two instants straddle a
	// UTC midnight, so an hours-based delta answers one day.
	from := time.Date(2026, 8, 10, 23, 0, 0, 0, time.UTC) // 08-11 08:00 JST
	to := time.Date(2026, 8, 11, 11, 0, 0, 0, time.UTC)   // 08-11 20:00 JST
	got, err := LocalDayDelta(from, to, tokyo)
	if err != nil {
		t.Fatalf("LocalDayDelta: %v", err)
	}
	if got != 0 {
		t.Errorf("LocalDayDelta = %d, want 0 (same Tokyo day)", got)
	}

	// One Tokyo day later, measured from the same start.
	next := time.Date(2026, 8, 11, 23, 0, 0, 0, time.UTC) // 08-12 08:00 JST
	got, err = LocalDayDelta(from, next, tokyo)
	if err != nil {
		t.Fatalf("LocalDayDelta: %v", err)
	}
	if got != 1 {
		t.Errorf("LocalDayDelta = %d, want 1", got)
	}

	// Backwards moves are negative.
	got, err = LocalDayDelta(next, from, tokyo)
	if err != nil {
		t.Fatalf("LocalDayDelta: %v", err)
	}
	if got != -1 {
		t.Errorf("LocalDayDelta = %d, want -1", got)
	}
}

func TestLocalDayDeltaSurvivesADSTTransition(t *testing.T) {
	// 2026-03-08 is the US spring-forward: that local day is 23 hours
	// long, so dividing an elapsed duration by 24h under-counts it.
	la := "America/Los_Angeles"
	from := time.Date(2026, 3, 7, 20, 0, 0, 0, time.UTC) // 03-07 12:00 PST
	to := time.Date(2026, 3, 8, 19, 0, 0, 0, time.UTC)   // 03-08 12:00 PDT
	got, err := LocalDayDelta(from, to, la)
	if err != nil {
		t.Fatalf("LocalDayDelta: %v", err)
	}
	if got != 1 {
		t.Errorf("LocalDayDelta across spring-forward = %d, want 1", got)
	}
	if hours := to.Sub(from).Hours(); hours != 23 {
		t.Fatalf("test premise broken: elapsed %v hours, expected 23", hours)
	}
}

func TestSameLocalDate(t *testing.T) {
	a := time.Date(2026, 8, 10, 23, 0, 0, 0, time.UTC)
	b := time.Date(2026, 8, 11, 10, 0, 0, 0, time.UTC)
	same, err := SameLocalDate(a, b, "Asia/Tokyo")
	if err != nil {
		t.Fatalf("SameLocalDate: %v", err)
	}
	if !same {
		t.Error("expected both instants to fall on the same Tokyo day")
	}
	same, err = SameLocalDate(a, b, "UTC")
	if err != nil {
		t.Fatalf("SameLocalDate: %v", err)
	}
	if same {
		t.Error("expected the two instants to fall on different UTC days")
	}
}
