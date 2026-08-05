package recurrence

import (
	"testing"
	"time"
)

// The cases here mirror the browser expander's suite
// (packages/ui/src/calendar/__tests__/recurrence.test.ts). The two
// implementations expand the same stored rules for the same product, so
// a divergence is a bug wherever it appears, and the properties most
// likely to diverge in a reimplementation are the arithmetic ones:
// anchor-based month clamping and wall-clock preservation across DST.

func mustLoad(t *testing.T, name string) *time.Location {
	t.Helper()
	loc, err := time.LoadLocation(name)
	if err != nil {
		t.Fatalf("load %s: %v", name, err)
	}
	return loc
}

func intPtr(v int) *int { return &v }

// starts renders the occurrences as RFC3339 in the given zone, which is
// what the failure messages need to be readable.
func starts(occ []Occurrence, loc *time.Location) []string {
	out := make([]string, len(occ))
	for i, o := range occ {
		out[i] = o.StartAt.In(loc).Format(time.RFC3339)
	}
	return out
}

func requireStarts(t *testing.T, got []Occurrence, loc *time.Location, want []string) {
	t.Helper()
	g := starts(got, loc)
	if len(g) != len(want) {
		t.Fatalf("occurrence count = %d, want %d\n got: %v\nwant: %v", len(g), len(want), g, want)
	}
	for i := range want {
		if g[i] != want[i] {
			t.Fatalf("occurrence %d = %s, want %s\n got: %v\nwant: %v", i, g[i], want[i], g, want)
		}
	}
}

func TestExpandDailyWithInterval(t *testing.T) {
	t.Parallel()
	loc := time.UTC
	start := time.Date(2027, 3, 1, 9, 0, 0, 0, loc)
	occ := Expand(Event{
		StartAt: start,
		EndAt:   start.Add(time.Hour),
		Rule:    &Rule{Freq: FreqDaily, Interval: intPtr(3)},
	}, start, time.Date(2027, 3, 11, 0, 0, 0, 0, loc))

	requireStarts(t, occ, loc, []string{
		"2027-03-01T09:00:00Z",
		"2027-03-04T09:00:00Z",
		"2027-03-07T09:00:00Z",
		"2027-03-10T09:00:00Z",
	})
}

// TestExpandWeeklyByDayExpands covers the RFC 5545 rule that BYDAY makes
// a weekly series produce one occurrence per listed weekday, not just on
// the anchor's weekday.
func TestExpandWeeklyByDayExpands(t *testing.T) {
	t.Parallel()
	loc := time.UTC
	// 2027-03-01 is a Monday.
	start := time.Date(2027, 3, 1, 9, 0, 0, 0, loc)
	occ := Expand(Event{
		StartAt: start,
		EndAt:   start.Add(time.Hour),
		Rule:    &Rule{Freq: FreqWeekly, ByDay: []string{"MO", "WE"}},
	}, start, time.Date(2027, 3, 15, 0, 0, 0, 0, loc))

	requireStarts(t, occ, loc, []string{
		"2027-03-01T09:00:00Z",
		"2027-03-03T09:00:00Z",
		"2027-03-08T09:00:00Z",
		"2027-03-10T09:00:00Z",
	})
}

// TestExpandWeeklyByDayHonoursInterval pins the half of the weekly-BYDAY
// path that a day-by-day scan can lose: with INTERVAL=2 the odd weeks
// are skipped entirely, and it is the week offset from the anchor that
// decides, not the count of emitted days.
func TestExpandWeeklyByDayHonoursInterval(t *testing.T) {
	t.Parallel()
	loc := time.UTC
	start := time.Date(2027, 3, 1, 9, 0, 0, 0, loc)
	occ := Expand(Event{
		StartAt: start,
		EndAt:   start.Add(time.Hour),
		Rule:    &Rule{Freq: FreqWeekly, Interval: intPtr(2), ByDay: []string{"MO", "WE"}},
	}, start, time.Date(2027, 4, 1, 0, 0, 0, 0, loc))

	requireStarts(t, occ, loc, []string{
		"2027-03-01T09:00:00Z",
		"2027-03-03T09:00:00Z",
		"2027-03-15T09:00:00Z",
		"2027-03-17T09:00:00Z",
		"2027-03-29T09:00:00Z",
		"2027-03-31T09:00:00Z",
	})
}

// TestExpandMonthlyClampsFromAnchor is the case that separates
// anchor-based arithmetic from chained arithmetic. Chaining month additions
// off the clamped February value pins every later month to the 28th.
func TestExpandMonthlyClampsFromAnchor(t *testing.T) {
	t.Parallel()
	loc := time.UTC
	start := time.Date(2027, 1, 31, 9, 0, 0, 0, loc)
	occ := Expand(Event{
		StartAt: start,
		EndAt:   start.Add(time.Hour),
		Rule:    &Rule{Freq: FreqMonthly},
	}, start, time.Date(2027, 6, 1, 0, 0, 0, 0, loc))

	requireStarts(t, occ, loc, []string{
		"2027-01-31T09:00:00Z",
		"2027-02-28T09:00:00Z",
		"2027-03-31T09:00:00Z",
		"2027-04-30T09:00:00Z",
		"2027-05-31T09:00:00Z",
	})
}

// TestExpandYearlyClampsLeapDay is the same property one unit up: a
// series anchored on Feb 29 lands on Feb 28 in common years and returns
// to Feb 29 at the next leap year.
func TestExpandYearlyClampsLeapDay(t *testing.T) {
	t.Parallel()
	loc := time.UTC
	start := time.Date(2028, 2, 29, 9, 0, 0, 0, loc)
	occ := Expand(Event{
		StartAt: start,
		EndAt:   start.Add(time.Hour),
		Rule:    &Rule{Freq: FreqYearly},
	}, start, time.Date(2033, 1, 1, 0, 0, 0, 0, loc))

	requireStarts(t, occ, loc, []string{
		"2028-02-29T09:00:00Z",
		"2029-02-28T09:00:00Z",
		"2030-02-28T09:00:00Z",
		"2031-02-28T09:00:00Z",
		"2032-02-29T09:00:00Z",
	})
}

// TestExpandPreservesWallClockAcrossDST is why the arithmetic works on
// wall-clock components in the event's own zone. A 09:00 New York
// standup stays at 09:00 through the March transition; done on instants
// it would drift to 08:00 or 10:00 for the rest of the series.
func TestExpandPreservesWallClockAcrossDST(t *testing.T) {
	t.Parallel()
	loc := mustLoad(t, "America/New_York")
	// 2027-03-14 is the US DST transition.
	start := time.Date(2027, 3, 11, 9, 0, 0, 0, loc)
	occ := Expand(Event{
		StartAt:  start,
		EndAt:    start.Add(time.Hour),
		Timezone: "America/New_York",
		Rule:     &Rule{Freq: FreqDaily},
	}, start, time.Date(2027, 3, 18, 0, 0, 0, 0, loc))

	if len(occ) == 0 {
		t.Fatal("expected occurrences across the DST boundary")
	}
	for _, o := range occ {
		local := o.StartAt.In(loc)
		if local.Hour() != 9 || local.Minute() != 0 {
			t.Fatalf("occurrence %s drifted off 09:00 local", local.Format(time.RFC3339))
		}
	}
}

// TestExpandUntilDateIsInclusive covers the bare-date UNTIL. Read at
// local midnight it would drop every timed occurrence on the final day,
// shortening the series by one without any visible error.
func TestExpandUntilDateIsInclusive(t *testing.T) {
	t.Parallel()
	loc := time.UTC
	start := time.Date(2027, 3, 1, 9, 0, 0, 0, loc)
	occ := Expand(Event{
		StartAt: start,
		EndAt:   start.Add(time.Hour),
		Rule:    &Rule{Freq: FreqDaily, Until: "2027-03-03"},
	}, start, time.Date(2027, 4, 1, 0, 0, 0, 0, loc))

	requireStarts(t, occ, loc, []string{
		"2027-03-01T09:00:00Z",
		"2027-03-02T09:00:00Z",
		"2027-03-03T09:00:00Z",
	})
}

func TestExpandCountLimits(t *testing.T) {
	t.Parallel()
	loc := time.UTC
	start := time.Date(2027, 3, 1, 9, 0, 0, 0, loc)
	occ := Expand(Event{
		StartAt: start,
		EndAt:   start.Add(time.Hour),
		Rule:    &Rule{Freq: FreqDaily, Count: intPtr(2)},
	}, start, time.Date(2027, 4, 1, 0, 0, 0, 0, loc))

	requireStarts(t, occ, loc, []string{
		"2027-03-01T09:00:00Z",
		"2027-03-02T09:00:00Z",
	})
}

// TestExpandExceptionsCountTowardsCount states the interaction between
// the two limits: cancelling an occurrence removes it from the calendar
// but does not extend a COUNT series by one at the far end. "Ten
// meetings, one cancelled" is nine meetings.
func TestExpandExceptionsCountTowardsCount(t *testing.T) {
	t.Parallel()
	loc := time.UTC
	start := time.Date(2027, 3, 1, 9, 0, 0, 0, loc)
	occ := Expand(Event{
		StartAt:    start,
		EndAt:      start.Add(time.Hour),
		Rule:       &Rule{Freq: FreqDaily, Count: intPtr(3)},
		Exceptions: []string{"2027-03-02T09:00:00Z"},
	}, start, time.Date(2027, 4, 1, 0, 0, 0, 0, loc))

	requireStarts(t, occ, loc, []string{
		"2027-03-01T09:00:00Z",
		"2027-03-03T09:00:00Z",
	})
}

// TestExpandExceptionsAcceptBareDates covers the second exception shape
// the column stores.
func TestExpandExceptionsAcceptBareDates(t *testing.T) {
	t.Parallel()
	loc := time.UTC
	start := time.Date(2027, 3, 1, 9, 0, 0, 0, loc)
	occ := Expand(Event{
		StartAt:    start,
		EndAt:      start.Add(time.Hour),
		Rule:       &Rule{Freq: FreqDaily},
		Exceptions: []string{"2027-03-02"},
	}, start, time.Date(2027, 3, 4, 0, 0, 0, 0, loc))

	requireStarts(t, occ, loc, []string{
		"2027-03-01T09:00:00Z",
		"2027-03-03T09:00:00Z",
	})
}

// TestExpandRangeFiltersByOverlap pins the range semantics: an
// occurrence counts when it overlaps the window, so a meeting that
// started before the window opened and is still running is returned.
func TestExpandRangeFiltersByOverlap(t *testing.T) {
	t.Parallel()
	loc := time.UTC
	start := time.Date(2027, 3, 1, 9, 0, 0, 0, loc)
	// Window opens mid-meeting on 3 March.
	rangeStart := time.Date(2027, 3, 3, 9, 30, 0, 0, loc)
	rangeEnd := time.Date(2027, 3, 4, 0, 0, 0, 0, loc)
	occ := Expand(Event{
		StartAt: start,
		EndAt:   start.Add(time.Hour),
		Rule:    &Rule{Freq: FreqDaily},
	}, rangeStart, rangeEnd)

	requireStarts(t, occ, loc, []string{"2027-03-03T09:00:00Z"})
}

func TestExpandWithoutRuleYieldsNothing(t *testing.T) {
	t.Parallel()
	loc := time.UTC
	start := time.Date(2027, 3, 1, 9, 0, 0, 0, loc)
	if occ := Expand(Event{StartAt: start, EndAt: start.Add(time.Hour)},
		start, start.AddDate(0, 1, 0)); len(occ) != 0 {
		t.Fatalf("a non-recurring event expanded to %d occurrences", len(occ))
	}
}

// TestExpandTerminatesWithoutUntilOrCount guards the bound. A rule with
// neither limit and a range far from the anchor must return rather than
// scan forever.
func TestExpandTerminatesWithoutUntilOrCount(t *testing.T) {
	t.Parallel()
	loc := time.UTC
	start := time.Date(2027, 3, 1, 9, 0, 0, 0, loc)
	far := time.Date(2200, 1, 1, 0, 0, 0, 0, loc)
	done := make(chan int, 1)
	go func() {
		done <- len(Expand(Event{
			StartAt: start,
			EndAt:   start.Add(time.Hour),
			Rule:    &Rule{Freq: FreqDaily},
		}, far, far.AddDate(0, 0, 1)))
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("expansion did not terminate on an unbounded rule")
	}
}

func TestParseRuleHandlesNull(t *testing.T) {
	t.Parallel()
	for _, raw := range []string{"", "null", "  null  "} {
		r, err := ParseRule([]byte(raw))
		if err != nil {
			t.Fatalf("ParseRule(%q) err = %v", raw, err)
		}
		if r != nil {
			t.Fatalf("ParseRule(%q) = %+v, want nil", raw, r)
		}
	}
}

func TestParseRuleReadsGrammar(t *testing.T) {
	t.Parallel()
	r, err := ParseRule([]byte(`{"freq":"weekly","interval":2,"byDay":["MO","WE"],"until":"2027-06-01"}`))
	if err != nil {
		t.Fatalf("ParseRule err = %v", err)
	}
	if r == nil || r.Freq != FreqWeekly || r.Interval == nil || *r.Interval != 2 ||
		len(r.ByDay) != 2 || r.Until != "2027-06-01" {
		t.Fatalf("ParseRule = %+v", r)
	}
}
