package calendar_event_day

import (
	"database/sql"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/libraz/nodate-flow/packages/go-shared/region"
)

// The expansion arithmetic belongs to packages/go-shared/recurrence and is
// pinned there against testdata/recurrence_golden.json, the same fixture
// the browser expander is held to. What is left here is what this scanner
// adds on top of it: which column feeds which field, which occurrences
// count as arriving on the day being materialised, and which malformed
// rows are reported rather than silently expanded to nothing.

// arriving builds the day window the scan materialises, the same way
// ListEventsForDays derives it from a local date.
func arriving(z region.Zone, y int, m time.Month, d int) arrivingDay {
	day := region.NewDay(y, m, d)
	return arrivingDay{
		day:      day.String(),
		utcStart: day.Start(z).UTC(),
		utcEnd:   day.EndExclusive(z).UTC(),
	}
}

// row builds a scanned candidate. duration is how long each occurrence
// runs, which is what the expander reads out of end_at.
func row(startAt time.Time, duration time.Duration, tz, rule string) candidateRow {
	return candidateRow{
		event:          Event{StartAt: startAt.UTC()},
		endAt:          sql.NullTime{Time: startAt.Add(duration).UTC(), Valid: true},
		timezone:       tz,
		recurrenceRule: []byte(rule),
	}
}

// startsOf renders the emitted tuples as UTC RFC3339 so a failure reads as
// a list of instants rather than as time.Time literals.
func startsOf(tuples []EventOnDay) []string {
	out := make([]string, 0, len(tuples))
	for _, tuple := range tuples {
		out = append(out, tuple.Event.StartAt.UTC().Format(time.RFC3339))
	}
	return out
}

func expand(t *testing.T, c candidateRow, d arrivingDay) []EventOnDay {
	t.Helper()
	tuples, err := (&Scanner{}).expandCandidate(c, d)
	require.NoError(t, err)
	return tuples
}

// TestExpandCandidate_NonRecurringRowEmitsItsOwnDay pins the path a row
// without a rule takes: the SQL filter already decided it belongs to this
// day, so it contributes exactly one tuple and no expansion runs.
func TestExpandCandidate_NonRecurringRowEmitsItsOwnDay(t *testing.T) {
	t.Parallel()
	day := arriving(region.UTC(), 2026, time.July, 1)
	base := time.Date(2026, time.July, 1, 9, 0, 0, 0, time.UTC)

	for _, raw := range []string{"", "null", "  "} {
		c := row(base, time.Hour, "UTC", raw)
		tuples := expand(t, c, day)
		require.Len(t, tuples, 1, "input %q must take the single-occurrence path", raw)
		require.Equal(t, "2026-07-01", tuples[0].Day)
		require.Equal(t, day.utcEnd.Unix(), tuples[0].ExpiresAtUnix)
	}
}

// TestExpandCandidate_FiresOnADayAfterTheBase is the whole reason the
// scanner expands at all: a recurring row has to reach days its base start
// never touches.
func TestExpandCandidate_FiresOnADayAfterTheBase(t *testing.T) {
	t.Parallel()
	base := time.Date(2026, time.July, 1, 9, 0, 0, 0, time.UTC)
	c := row(base, time.Hour, "UTC", `{"freq":"daily"}`)

	tuples := expand(t, c, arriving(region.UTC(), 2026, time.July, 5))
	require.Equal(t, []string{"2026-07-05T09:00:00Z"}, startsOf(tuples))
	require.Equal(t, "2026-07-05", tuples[0].Day,
		"the tuple must be labelled with the day it arrived on, not the base day")
}

// TestExpandCandidate_OnlyOccurrencesStartingInTheDayArrive states the
// rule the scanner applies on top of the expander. The expander answers
// which occurrences meet a window, including one that began earlier and is
// still running; a day arrives for an occurrence that starts in it. An
// overnight series would otherwise announce itself twice — once on the day
// it starts and again on the day it ends.
func TestExpandCandidate_OnlyOccurrencesStartingInTheDayArrive(t *testing.T) {
	t.Parallel()
	// A nightly series running 22:00 -> 02:00 the next day.
	base := time.Date(2026, time.July, 1, 22, 0, 0, 0, time.UTC)
	c := row(base, 4*time.Hour, "UTC", `{"freq":"daily"}`)

	tuples := expand(t, c, arriving(region.UTC(), 2026, time.July, 5))
	require.Equal(t, []string{"2026-07-05T22:00:00Z"}, startsOf(tuples),
		"only the occurrence starting on 07-05 arrives; the one that started on 07-04 does not")
}

// TestExpandCandidate_OccurrenceAtLocalMidnightArrives covers the edge of
// that rule: an occurrence starting exactly at the local midnight the day
// begins on belongs to the day that just started.
func TestExpandCandidate_OccurrenceAtLocalMidnightArrives(t *testing.T) {
	t.Parallel()
	loc := mustLoad(t, "Asia/Tokyo")
	base := time.Date(2026, time.July, 1, 0, 0, 0, 0, loc.Location())
	c := row(base, time.Hour, "Asia/Tokyo", `{"freq":"daily"}`)

	tuples := expand(t, c, arriving(loc, 2026, time.July, 5))
	require.Len(t, tuples, 1, "an occurrence at local midnight arrives on the day it opens")
	require.Equal(t, "2026-07-05", tuples[0].Day)
}

// TestExpandCandidate_ExceptionSuppressesTheOccurrence proves the
// recurrence_exceptions column reaches the expander.
func TestExpandCandidate_ExceptionSuppressesTheOccurrence(t *testing.T) {
	t.Parallel()
	base := time.Date(2026, time.July, 1, 9, 0, 0, 0, time.UTC)
	c := row(base, time.Hour, "UTC", `{"freq":"daily"}`)
	c.recurrenceExceptions = []byte(`["2026-07-05T09:00:00Z"]`)

	require.Empty(t, expand(t, c, arriving(region.UTC(), 2026, time.July, 5)),
		"a cancelled occurrence must not arrive")
	require.Len(t, expand(t, c, arriving(region.UTC(), 2026, time.July, 6)), 1,
		"cancelling one occurrence must not end the series")
}

// TestExpandCandidate_RecurrenceEndStopsTheSeries proves the
// recurrence_end column — which lives beside the rule, not inside it —
// reaches the expander as the series' upper bound.
func TestExpandCandidate_RecurrenceEndStopsTheSeries(t *testing.T) {
	t.Parallel()
	base := time.Date(2026, time.July, 1, 9, 0, 0, 0, time.UTC)
	end := time.Date(2026, time.July, 4, 9, 0, 0, 0, time.UTC)
	c := row(base, time.Hour, "UTC", `{"freq":"daily"}`)
	c.recurrenceEnd = sql.NullTime{Time: end, Valid: true}

	require.Len(t, expand(t, c, arriving(region.UTC(), 2026, time.July, 4)), 1,
		"the occurrence landing on recurrence_end is the last one, and it still arrives")
	require.Empty(t, expand(t, c, arriving(region.UTC(), 2026, time.July, 5)),
		"an occurrence past recurrence_end must not arrive")
}

// TestExpandCandidate_UntilAndCountBoundTheSeries covers the two bounds
// the rule itself carries, including the bare-date UNTIL that names a
// whole local day rather than its midnight.
func TestExpandCandidate_UntilAndCountBoundTheSeries(t *testing.T) {
	t.Parallel()
	ny := mustLoad(t, "America/New_York")
	base := time.Date(2026, time.July, 1, 9, 0, 0, 0, ny.Location())

	until := row(base, time.Hour, "America/New_York", `{"freq":"daily","until":"2026-07-10"}`)
	require.Len(t, expand(t, until, arriving(ny, 2026, time.July, 10)), 1,
		"the 09:00 occurrence on a bare-date UNTIL day still arrives")
	require.Empty(t, expand(t, until, arriving(ny, 2026, time.July, 11)),
		"an occurrence past the UNTIL day must not arrive")

	counted := row(base, time.Hour, "America/New_York", `{"freq":"daily","count":3}`)
	require.Len(t, expand(t, counted, arriving(ny, 2026, time.July, 3)), 1,
		"the third occurrence of a count=3 series arrives")
	require.Empty(t, expand(t, counted, arriving(ny, 2026, time.July, 4)),
		"the fourth occurrence of a count=3 series must not arrive")
}

// TestExpandCandidate_PreservesWallClockAcrossDST pins that the event's
// own timezone drives the arithmetic: a 09:00 New York meeting stays at
// 09:00 local across the spring-forward, so its UTC instant moves by an
// hour and its local day does not.
func TestExpandCandidate_PreservesWallClockAcrossDST(t *testing.T) {
	t.Parallel()
	ny := mustLoad(t, "America/New_York")
	// The day before the 2026-03-08 transition. 09:00 EST is 14:00Z.
	base := time.Date(2026, time.March, 7, 9, 0, 0, 0, ny.Location())
	c := row(base, time.Hour, "America/New_York", `{"freq":"daily"}`)

	tuples := expand(t, c, arriving(ny, 2026, time.March, 8))
	require.Equal(t, []string{"2026-03-08T13:00:00Z"}, startsOf(tuples),
		"a daily 09:00 New York meeting is 13:00Z once the clocks move, not 14:00Z")
}

// TestExpandCandidate_UnknownTimezoneFallsBack keeps a row with an
// unreadable zone in the scan. Dropping it would silence the series
// entirely over one bad string.
func TestExpandCandidate_UnknownTimezoneFallsBack(t *testing.T) {
	t.Parallel()
	base := time.Date(2026, time.July, 1, 9, 0, 0, 0, time.UTC)
	c := row(base, time.Hour, "Nowhere/Imaginary", `{"freq":"daily"}`)

	require.Equal(t, []string{"2026-07-05T09:00:00Z"},
		startsOf(expand(t, c, arriving(region.UTC(), 2026, time.July, 5))))
}

// TestExpandCandidate_OldSeriesReachesTheCurrentWindow guards the scan
// budget. A series anchored many years before the window has to be walked
// all the way to it; a fixed step budget would run out on the way and the
// day would come back empty, which reads as "nothing is scheduled".
func TestExpandCandidate_OldSeriesReachesTheCurrentWindow(t *testing.T) {
	t.Parallel()
	base := time.Date(2000, time.January, 1, 9, 0, 0, 0, time.UTC)
	c := row(base, time.Hour, "UTC", `{"freq":"daily"}`)

	require.Equal(t, []string{"2026-07-04T09:00:00Z"},
		startsOf(expand(t, c, arriving(region.UTC(), 2026, time.July, 4))))
}

// TestExpandCandidate_ReportsMalformedRows covers the rows the scan
// refuses to guess about. Each returns an error so ListEventsForDays logs
// it and keeps scanning the workspace, rather than expanding to nothing
// and leaving the day looking empty.
func TestExpandCandidate_ReportsMalformedRows(t *testing.T) {
	t.Parallel()
	base := time.Date(2026, time.July, 1, 9, 0, 0, 0, time.UTC)
	day := arriving(region.UTC(), 2026, time.July, 5)

	badRule := row(base, time.Hour, "UTC", `{"freq":`)
	_, err := (&Scanner{}).expandCandidate(badRule, day)
	require.Error(t, err, "an undecodable recurrence_rule must be reported")

	unsupported := row(base, time.Hour, "UTC", `{"freq":"hourly"}`)
	_, err = (&Scanner{}).expandCandidate(unsupported, day)
	require.Error(t, err, "a freq outside the grammar must be reported, not expanded to nothing")

	badExceptions := row(base, time.Hour, "UTC", `{"freq":"daily"}`)
	badExceptions.recurrenceExceptions = []byte(`{"not":"a list"}`)
	_, err = (&Scanner{}).expandCandidate(badExceptions, day)
	require.Error(t, err, "an undecodable exception list must be reported rather than expanded without exclusions")
}

// TestDecodeRecurrenceExceptions_NullVariants pins the three spellings of
// "no exceptions" the column uses, none of which is an error.
func TestDecodeRecurrenceExceptions_NullVariants(t *testing.T) {
	t.Parallel()
	for _, raw := range [][]byte{nil, {}, []byte("  "), []byte("null"), []byte(" null ")} {
		values, err := decodeRecurrenceExceptions(raw)
		require.NoErrorf(t, err, "input %q", string(raw))
		require.Emptyf(t, values, "input %q", string(raw))
	}
}
