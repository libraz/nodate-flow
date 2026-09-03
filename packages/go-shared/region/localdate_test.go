package region

import (
	"testing"
	"time"
)

func mustZone(t *testing.T, tz string) Zone {
	t.Helper()
	z, err := Resolve(tz)
	if err != nil {
		t.Fatalf("Resolve(%q): %v", tz, err)
	}
	return z
}

func TestDayOfReadsTheDateInTheGivenZone(t *testing.T) {
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
		if got := DayOf(instant, mustZone(t, c.tz)).String(); got != c.want {
			t.Errorf("DayOf(%s) = %s, want %s", c.tz, got, c.want)
		}
	}
}

func TestDayOfCrossesTheDayLineFromTheOtherSide(t *testing.T) {
	// 2026-08-10 20:00 Los Angeles is 2026-08-11 03:00 UTC — the UTC
	// reading is a day ahead rather than a day behind, so a fix that
	// merely subtracts a day would break this case.
	instant := time.Date(2026, 8, 11, 3, 0, 0, 0, time.UTC)
	got := DayOf(instant, mustZone(t, "America/Los_Angeles")).String()
	if got != "2026-08-10" {
		t.Errorf("DayOf = %s, want 2026-08-10", got)
	}
}

func TestDateColumnIsCarriedAsMidnightUTC(t *testing.T) {
	// The carrier is what makes the value survive the MySQL driver,
	// which converts a time.Time parameter into its configured location
	// before formatting it. Midnight in Asia/Tokyo would be stored as
	// the previous day.
	d := DayOf(time.Date(2026, 8, 10, 23, 0, 0, 0, time.UTC), mustZone(t, "Asia/Tokyo")).DateColumn()
	if d.Location() != time.UTC {
		t.Errorf("location = %v, want UTC", d.Location())
	}
	if h, m, s := d.Clock(); h != 0 || m != 0 || s != 0 {
		t.Errorf("clock = %02d:%02d:%02d, want midnight", h, m, s)
	}
}

func TestResolveRejectsAnUnknownZone(t *testing.T) {
	if _, err := Resolve("Mars/Olympus"); err == nil {
		t.Fatal("expected an error for an unknown zone")
	}
	// Empty is not an error: it is the absence of a candidate, and the
	// chain's own fallback answers it. Silently answering UTC for a name
	// that was supplied and did not resolve is the bug; answering UTC
	// for a name that was never supplied is the documented default.
	z, err := Resolve("")
	if err != nil {
		t.Fatalf("Resolve(\"\"): %v", err)
	}
	if z.Name() != DefaultTimezone {
		t.Errorf("Resolve(\"\") = %q, want %q", z.Name(), DefaultTimezone)
	}
	if got := EffectiveTimezone(""); got != DefaultTimezone {
		t.Errorf("EffectiveTimezone(\"\") = %q, want %q", got, DefaultTimezone)
	}
}

func TestResolveTakesTheFirstNonEmptyCandidate(t *testing.T) {
	z, err := Resolve("", "Asia/Tokyo", "Europe/Berlin")
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if z.Name() != "Asia/Tokyo" {
		t.Errorf("Resolve = %q, want Asia/Tokyo", z.Name())
	}
	// A winning candidate that does not resolve is an error rather than
	// a fall-through to the tier below it: the tier below would answer
	// plausibly and wrongly, with nothing said.
	if _, err := Resolve("Mars/Olympus", "Asia/Tokyo"); err == nil {
		t.Fatal("expected the unresolvable winner to be an error")
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

func TestDaySubCountsCalendarDaysNotHours(t *testing.T) {
	tokyo := mustZone(t, "Asia/Tokyo")
	// 08:00 → 20:00 on the same Tokyo day. The two instants straddle a
	// UTC midnight, so an hours-based delta answers one day.
	from := time.Date(2026, 8, 10, 23, 0, 0, 0, time.UTC) // 08-11 08:00 JST
	to := time.Date(2026, 8, 11, 11, 0, 0, 0, time.UTC)   // 08-11 20:00 JST
	if got := DayOf(to, tokyo).Sub(DayOf(from, tokyo)); got != 0 {
		t.Errorf("Day.Sub = %d, want 0 (same Tokyo day)", got)
	}

	// One Tokyo day later, measured from the same start.
	next := time.Date(2026, 8, 11, 23, 0, 0, 0, time.UTC) // 08-12 08:00 JST
	if got := DayOf(next, tokyo).Sub(DayOf(from, tokyo)); got != 1 {
		t.Errorf("Day.Sub = %d, want 1", got)
	}

	// Backwards moves are negative.
	if got := DayOf(from, tokyo).Sub(DayOf(next, tokyo)); got != -1 {
		t.Errorf("Day.Sub = %d, want -1", got)
	}
}

func TestDaySubSurvivesADSTTransition(t *testing.T) {
	// 2026-03-08 is the US spring-forward: that local day is 23 hours
	// long, so dividing an elapsed duration by 24h under-counts it.
	la := mustZone(t, "America/Los_Angeles")
	from := time.Date(2026, 3, 7, 20, 0, 0, 0, time.UTC) // 03-07 12:00 PST
	to := time.Date(2026, 3, 8, 19, 0, 0, 0, time.UTC)   // 03-08 12:00 PDT
	if got := DayOf(to, la).Sub(DayOf(from, la)); got != 1 {
		t.Errorf("Day.Sub across spring-forward = %d, want 1", got)
	}
	if hours := to.Sub(from).Hours(); hours != 23 {
		t.Fatalf("test premise broken: elapsed %v hours, expected 23", hours)
	}
}

func TestDayEqualityAcrossZones(t *testing.T) {
	a := time.Date(2026, 8, 10, 23, 0, 0, 0, time.UTC)
	b := time.Date(2026, 8, 11, 10, 0, 0, 0, time.UTC)
	tokyo := mustZone(t, "Asia/Tokyo")
	if !DayOf(a, tokyo).Equal(DayOf(b, tokyo)) {
		t.Error("expected both instants to fall on the same Tokyo day")
	}
	utc := UTC()
	if DayOf(a, utc).Equal(DayOf(b, utc)) {
		t.Error("expected the two instants to fall on different UTC days")
	}
}

func TestDayBoundsSpanTheLocalDayAcrossDST(t *testing.T) {
	la := mustZone(t, "America/Los_Angeles")
	// Spring forward: 2026-03-08 is 23 hours long in Los Angeles.
	spring := NewDay(2026, time.March, 8)
	if h := spring.EndExclusive(la).Sub(spring.Start(la)).Hours(); h != 23 {
		t.Errorf("spring-forward day = %v hours, want 23", h)
	}
	// Fall back: 2026-11-01 is 25 hours long.
	fall := NewDay(2026, time.November, 1)
	if h := fall.EndExclusive(la).Sub(fall.Start(la)).Hours(); h != 25 {
		t.Errorf("fall-back day = %v hours, want 25", h)
	}
}

func TestParseDayRoundTripsTheWireForm(t *testing.T) {
	d, err := ParseDay("2026-02-29")
	if err == nil {
		t.Fatalf("expected 2026-02-29 to be rejected, got %s", d)
	}
	d, err = ParseDay("2026-08-11")
	if err != nil {
		t.Fatalf("ParseDay: %v", err)
	}
	if got := d.String(); got != "2026-08-11" {
		t.Errorf("round trip = %s, want 2026-08-11", got)
	}
	if _, err := ParseDay("11/08/2026"); err == nil {
		t.Fatal("expected a non-ISO date to be rejected")
	}
}

func TestDayAddDaysNormalisesAcrossMonthEnds(t *testing.T) {
	if got := NewDay(2026, time.January, 31).AddDays(1).String(); got != "2026-02-01" {
		t.Errorf("AddDays(1) = %s, want 2026-02-01", got)
	}
	if got := NewDay(2026, time.March, 1).AddDays(-1).String(); got != "2026-02-28" {
		t.Errorf("AddDays(-1) = %s, want 2026-02-28", got)
	}
}

func TestDayAtBuildsTheWallClockInTheZone(t *testing.T) {
	tokyo := mustZone(t, "Asia/Tokyo")
	got := NewDay(2026, time.August, 11).At(tokyo, 9, 30, 0)
	want := time.Date(2026, 8, 11, 0, 30, 0, 0, time.UTC)
	if !got.Equal(want) {
		t.Errorf("At = %s, want the same instant as %s", got, want)
	}
}

func TestDayBindsAsADateColumn(t *testing.T) {
	// The parameter lists a Day reaches are `...any`, so nothing but this
	// keeps a Day landing in a `?` from being rejected by the driver or,
	// worse, from being rewritten by the driver's own location.
	v, err := NewDay(2026, time.August, 11).Value()
	if err != nil {
		t.Fatalf("Value: %v", err)
	}
	bound, ok := v.(time.Time)
	if !ok {
		t.Fatalf("Value = %T, want time.Time", v)
	}
	if bound.Location() != time.UTC {
		t.Errorf("bound location = %v, want UTC", bound.Location())
	}
	if got := bound.Format("2006-01-02T15:04:05Z07:00"); got != "2026-08-11T00:00:00Z" {
		t.Errorf("bound value = %s, want 2026-08-11T00:00:00Z", got)
	}

	// The zero Day names no date, so it binds as NULL rather than as
	// year 1, which no DATE column can hold.
	v, err = Day{}.Value()
	if err != nil {
		t.Fatalf("Value (zero): %v", err)
	}
	if v != nil {
		t.Errorf("zero Day bound as %v, want NULL", v)
	}
}

func TestDayScansBackFromADateColumn(t *testing.T) {
	var d Day
	// The driver hands a DATE back as a time.Time in its own configured
	// location; only the calendar components survive, which is the point.
	if err := d.Scan(time.Date(2026, 8, 11, 0, 0, 0, 0, time.FixedZone("X", 3600))); err != nil {
		t.Fatalf("Scan(time.Time): %v", err)
	}
	if got := d.String(); got != "2026-08-11" {
		t.Errorf("Scan(time.Time) = %s, want 2026-08-11", got)
	}
	if err := d.Scan([]byte("2026-08-12")); err != nil {
		t.Fatalf("Scan([]byte): %v", err)
	}
	if got := d.String(); got != "2026-08-12" {
		t.Errorf("Scan([]byte) = %s, want 2026-08-12", got)
	}
	if err := d.Scan(nil); err != nil {
		t.Fatalf("Scan(nil): %v", err)
	}
	if !d.IsZero() {
		t.Errorf("Scan(nil) = %s, want the zero Day", d)
	}
	if err := d.Scan(42); err == nil {
		t.Fatal("expected an int to be refused")
	}
}
