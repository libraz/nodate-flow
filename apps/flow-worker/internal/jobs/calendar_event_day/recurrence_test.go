package calendar_event_day

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// ptrInt returns a pointer to v for the optional rule fields.
func ptrInt(v int) *int { return &v }

// ptrStr returns a pointer to v for the optional rule fields.
func ptrStr(v string) *string { return &v }

// occUnix maps the expanded occurrence instants to unix seconds so the
// assertions read as a flat list rather than time.Time literals.
func occUnix(occ []time.Time) []int64 {
	out := make([]int64, 0, len(occ))
	for _, o := range occ {
		out = append(out, o.Unix())
	}
	return out
}

// dayRange returns the UTC [start, end) bounds of one calendar day in loc,
// matching how the scanner derives an arriving day's window.
func dayRange(t *testing.T, loc *time.Location, y int, m time.Month, d int) (time.Time, time.Time) {
	t.Helper()
	start := time.Date(y, m, d, 0, 0, 0, 0, loc)
	end := time.Date(y, m, d+1, 0, 0, 0, 0, loc)
	return start.UTC(), end.UTC()
}

// TestParseRecurrenceRule_NullVariants verifies the decode treats SQL NULL,
// empty bytes, and the JSON null literal as "no rule" so a non-recurring row
// flows through the single-occurrence path.
func TestParseRecurrenceRule_NullVariants(t *testing.T) {
	t.Parallel()
	for _, in := range [][]byte{nil, {}, []byte("  "), []byte("null"), []byte(" null ")} {
		rule, err := parseRecurrenceRule(in)
		require.NoError(t, err)
		require.Nil(t, rule, "input %q must decode to no rule", string(in))
	}
}

// TestParseRecurrenceRule_UnsupportedFreq rejects an unknown freq so the scan
// fails loudly rather than silently emitting nothing.
func TestParseRecurrenceRule_UnsupportedFreq(t *testing.T) {
	t.Parallel()
	_, err := parseRecurrenceRule([]byte(`{"freq":"hourly"}`))
	require.Error(t, err)
}

// TestExpandDaily_FiresOnNonBaseDay is the core P2-8 fix: a daily rule must
// produce an occurrence on a day after the base start day.
func TestExpandDaily_FiresOnNonBaseDay(t *testing.T) {
	t.Parallel()
	loc := time.UTC
	base := time.Date(2026, time.July, 1, 9, 0, 0, 0, time.UTC)
	rule := &recurrenceRule{Freq: "daily"}

	// Window = 2026-07-05 (four days after base). The daily occurrence at
	// 09:00 on 07-05 must land in the range.
	ws, we := dayRange(t, loc, 2026, time.July, 5)
	occ := expandOccurrences(rule, base, loc, time.Time{}, time.Time{}, nil, ws, we)
	require.Equal(t, []int64{
		time.Date(2026, time.July, 5, 9, 0, 0, 0, time.UTC).Unix(),
	}, occUnix(occ), "the 07-05 occurrence of a daily rule must fire")
}

// TestExpandDaily_BaseDayStillFires confirms the base day is unchanged by the
// expansion: an occurrence on the base day lands in its own window.
func TestExpandDaily_BaseDayStillFires(t *testing.T) {
	t.Parallel()
	loc := time.UTC
	base := time.Date(2026, time.July, 1, 9, 0, 0, 0, time.UTC)
	rule := &recurrenceRule{Freq: "daily"}

	ws, we := dayRange(t, loc, 2026, time.July, 1)
	occ := expandOccurrences(rule, base, loc, time.Time{}, time.Time{}, nil, ws, we)
	require.Equal(t, []int64{base.Unix()}, occUnix(occ))
}

// TestExpandDaily_ExceptionSuppresses proves an occurrence whose instant is
// in the recurrence_exceptions set does not fire.
func TestExpandDaily_ExceptionSuppresses(t *testing.T) {
	t.Parallel()
	loc := time.UTC
	base := time.Date(2026, time.July, 1, 9, 0, 0, 0, time.UTC)
	rule := &recurrenceRule{Freq: "daily"}

	excludedInstant := time.Date(2026, time.July, 5, 9, 0, 0, 0, time.UTC)
	exceptions := &recurrenceExceptions{instants: map[int64]struct{}{excludedInstant.Unix(): {}}}

	ws, we := dayRange(t, loc, 2026, time.July, 5)
	occ := expandOccurrences(rule, base, loc, time.Time{}, time.Time{}, exceptions, ws, we)
	require.Empty(t, occ, "an excluded occurrence must not fire")
}

// TestExpandDaily_PastRecurrenceEnd proves an occurrence at or after the
// recurrence_end bound does not fire.
func TestExpandDaily_PastRecurrenceEnd(t *testing.T) {
	t.Parallel()
	loc := time.UTC
	base := time.Date(2026, time.July, 1, 9, 0, 0, 0, time.UTC)
	rule := &recurrenceRule{Freq: "daily"}

	// recurrence_end falls on 07-04; the 07-05 occurrence is past it.
	recurrenceEnd := time.Date(2026, time.July, 4, 12, 0, 0, 0, time.UTC)
	ws, we := dayRange(t, loc, 2026, time.July, 5)
	occ := expandOccurrences(rule, base, loc, recurrenceEnd, time.Time{}, nil, ws, we)
	require.Empty(t, occ, "an occurrence past recurrence_end must not fire")
}

// TestExpandDaily_PastUntil proves the inclusive UNTIL bound stops the
// sequence: an occurrence after until does not fire.
func TestExpandDaily_PastUntil(t *testing.T) {
	t.Parallel()
	loc := time.UTC
	base := time.Date(2026, time.July, 1, 9, 0, 0, 0, time.UTC)
	rule := &recurrenceRule{Freq: "daily"}

	until := time.Date(2026, time.July, 4, 9, 0, 0, 0, time.UTC)
	ws, we := dayRange(t, loc, 2026, time.July, 5)
	occ := expandOccurrences(rule, base, loc, time.Time{}, until, nil, ws, we)
	require.Empty(t, occ, "an occurrence past until must not fire")
}

// TestExpandDaily_CountCaps proves COUNT limits the number of occurrences:
// with count=3 the fourth day (07-04) is past the sequence.
func TestExpandDaily_CountCaps(t *testing.T) {
	t.Parallel()
	loc := time.UTC
	base := time.Date(2026, time.July, 1, 9, 0, 0, 0, time.UTC)
	rule := &recurrenceRule{Freq: "daily", Count: ptrInt(3)}

	// 07-03 (3rd occurrence) fires; 07-04 (4th) does not.
	ws3, we3 := dayRange(t, loc, 2026, time.July, 3)
	occ3 := expandOccurrences(rule, base, loc, time.Time{}, time.Time{}, nil, ws3, we3)
	require.Len(t, occ3, 1, "the 3rd occurrence (count=3) must fire")

	ws4, we4 := dayRange(t, loc, 2026, time.July, 4)
	occ4 := expandOccurrences(rule, base, loc, time.Time{}, time.Time{}, nil, ws4, we4)
	require.Empty(t, occ4, "the 4th occurrence must not fire when count=3")
}

func TestExpandDaily_OldMasterReachesCurrentWindow(t *testing.T) {
	t.Parallel()
	loc := time.UTC
	base := time.Date(2010, time.January, 1, 9, 0, 0, 0, time.UTC)
	rule := &recurrenceRule{Freq: "daily"}

	ws, we := dayRange(t, loc, 2026, time.July, 4)
	occ := expandOccurrences(rule, base, loc, time.Time{}, time.Time{}, nil, ws, we)

	require.Len(t, occ, 1, "daily recurrences older than maxOccurrences days must still reach the requested window")
	require.Equal(t, time.Date(2026, time.July, 4, 9, 0, 0, 0, time.UTC), occ[0])
}

// TestExpandWeekly_Interval proves FREQ=WEEKLY;INTERVAL=2 skips the off-week:
// base 07-01 (Wed) fires on 07-15, not 07-08.
func TestExpandWeekly_Interval(t *testing.T) {
	t.Parallel()
	loc := time.UTC
	base := time.Date(2026, time.July, 1, 9, 0, 0, 0, time.UTC)
	rule := &recurrenceRule{Freq: "weekly", Interval: ptrInt(2)}

	wsOff, weOff := dayRange(t, loc, 2026, time.July, 8)
	require.Empty(t, expandOccurrences(rule, base, loc, time.Time{}, time.Time{}, nil, wsOff, weOff),
		"the off-week (07-08) must not fire for interval=2")

	wsOn, weOn := dayRange(t, loc, 2026, time.July, 15)
	require.Len(t, expandOccurrences(rule, base, loc, time.Time{}, time.Time{}, nil, wsOn, weOn), 1,
		"the on-week (07-15) must fire for interval=2")
}

// TestExpandWeekly_ByDay pins the byDay filter semantics the worker shares
// with the client expander (packages/ui/src/calendar/recurrence.ts): the
// weekly cursor advances a whole week per step and byDay filters the cursor
// instant, so a byDay weekday that is NOT the base weekday is filtered out
// rather than emitted intra-week. base is a Monday with byDay=[MO,WE,FR]:
// the Monday cursor matches MO, but the Wednesday is never visited. This
// keeps the worker's firing days identical to the calendar the UI renders.
func TestExpandWeekly_ByDay(t *testing.T) {
	t.Parallel()
	loc := time.UTC
	// 2026-07-06 is a Monday.
	base := time.Date(2026, time.July, 6, 9, 0, 0, 0, time.UTC)
	rule := &recurrenceRule{Freq: "weekly", ByDay: []string{"MO", "WE", "FR"}}

	// The base Monday matches byDay (MO) and fires.
	wsMon, weMon := dayRange(t, loc, 2026, time.July, 6)
	require.Len(t, expandOccurrences(rule, base, loc, time.Time{}, time.Time{}, nil, wsMon, weMon), 1,
		"the base Monday matches byDay MO and must fire")

	// Wednesday 2026-07-08 is not visited by the weekly cursor → no fire,
	// matching the client expander.
	wsWed, weWed := dayRange(t, loc, 2026, time.July, 8)
	require.Empty(t, expandOccurrences(rule, base, loc, time.Time{}, time.Time{}, nil, wsWed, weWed),
		"intra-week byDay weekdays are not expanded (parity with the client)")

	// The following Monday 2026-07-13 fires again.
	wsNext, weNext := dayRange(t, loc, 2026, time.July, 13)
	require.Len(t, expandOccurrences(rule, base, loc, time.Time{}, time.Time{}, nil, wsNext, weNext), 1,
		"the next weekly cursor (Monday) must fire")
}

// TestExpandDaily_DSTPreservesWallClock proves occurrences advance in the
// event timezone: a daily 09:00 America/New_York meeting stays at 09:00
// local across the 2026 spring-forward, so its UTC instant shifts by an hour
// but the local day boundary still matches.
func TestExpandDaily_DSTPreservesWallClock(t *testing.T) {
	t.Parallel()
	ny, err := time.LoadLocation("America/New_York")
	require.NoError(t, err)

	// Base the day before spring-forward (2026-03-08). 09:00 EST = 14:00Z.
	base := time.Date(2026, time.March, 7, 9, 0, 0, 0, ny)
	rule := &recurrenceRule{Freq: "daily"}

	// The 2026-03-08 occurrence must be 09:00 EDT = 13:00Z, not 14:00Z.
	ws, we := dayRange(t, ny, 2026, time.March, 8)
	occ := expandOccurrences(rule, base.UTC(), ny, time.Time{}, time.Time{}, nil, ws, we)
	require.Len(t, occ, 1)
	require.Equal(t, 13, occ[0].UTC().Hour(),
		"a daily 09:00 New York meeting must stay at 09:00 local (13:00Z after spring-forward)")
}

// TestParseRuleUntil_BareDate proves a YYYY-MM-DD until parses in the event
// timezone to that local midnight.
func TestParseRuleUntil_BareDate(t *testing.T) {
	t.Parallel()
	ny, err := time.LoadLocation("America/New_York")
	require.NoError(t, err)
	got := parseRuleUntil("2026-07-10", ny)
	want := time.Date(2026, time.July, 10, 0, 0, 0, 0, ny).UTC()
	require.Equal(t, want, got)
}

// TestParseRecurrenceExceptions_MixedFormats proves both RFC 3339 and bare
// date exceptions decode, and a bad value is skipped rather than failing.
func TestParseRecurrenceExceptions_MixedFormats(t *testing.T) {
	t.Parallel()
	loc := time.UTC
	raw := []byte(`["2026-07-05T09:00:00Z","2026-07-06","not-a-date"]`)
	set, err := parseRecurrenceExceptions(raw, loc)
	require.NoError(t, err)
	require.Contains(t, set.instants, time.Date(2026, time.July, 5, 9, 0, 0, 0, time.UTC).Unix())
	require.Contains(t, set.localDayKeys, "2026-07-06")
	require.Len(t, set.instants, 1, "the RFC3339 exception must be tracked as an instant")
	require.Len(t, set.localDayKeys, 1, "the bare date exception must be tracked as a local day")
}

// TestExpandRule_UntilPtr exercises the rule field decode path end-to-end via
// expandCandidate-equivalent inputs: a rule with a pointer until string is
// honoured.
func TestExpandRule_UntilPtr(t *testing.T) {
	t.Parallel()
	loc := time.UTC
	base := time.Date(2026, time.July, 1, 9, 0, 0, 0, time.UTC)
	rule := &recurrenceRule{Freq: "daily", Until: ptrStr("2026-07-03T09:00:00Z")}
	until := parseRuleUntil(*rule.Until, loc)

	// 07-03 fires (inclusive), 07-04 does not.
	ws3, we3 := dayRange(t, loc, 2026, time.July, 3)
	require.Len(t, expandOccurrences(rule, base, loc, time.Time{}, until, nil, ws3, we3), 1)
	ws4, we4 := dayRange(t, loc, 2026, time.July, 4)
	require.Empty(t, expandOccurrences(rule, base, loc, time.Time{}, until, nil, ws4, we4))
}

func TestRecurrenceGoldenFixtures(t *testing.T) {
	t.Parallel()
	for _, fx := range loadRecurrenceGolden(t) {
		t.Run(fx.Name, func(t *testing.T) {
			t.Parallel()
			loc := time.UTC
			if fx.Event.Timezone != "" {
				var err error
				loc, err = time.LoadLocation(fx.Event.Timezone)
				require.NoError(t, err)
			}
			base, err := time.Parse(time.RFC3339, fx.Event.StartAt)
			require.NoError(t, err)
			rangeStart, err := time.Parse(time.RFC3339, fx.RangeStart)
			require.NoError(t, err)
			rangeEnd, err := time.Parse(time.RFC3339, fx.RangeEnd)
			require.NoError(t, err)

			rawExceptions, err := json.Marshal(fx.Event.RecurrenceExceptions)
			require.NoError(t, err)
			exceptions, err := parseRecurrenceExceptions(rawExceptions, loc)
			require.NoError(t, err)

			occ := expandOccurrences(&fx.Event.RecurrenceRule, base, loc, time.Time{}, time.Time{}, exceptions, rangeStart, rangeEnd)
			got := make([]string, 0, len(occ))
			for _, o := range occ {
				got = append(got, o.UTC().Format(time.RFC3339))
			}
			require.Equal(t, fx.ExpectedStartAt, got)
		})
	}
}

type recurrenceGoldenFixture struct {
	Name  string `json:"name"`
	Event struct {
		StartAt              string         `json:"startAt"`
		EndAt                string         `json:"endAt"`
		Timezone             string         `json:"timezone"`
		RecurrenceRule       recurrenceRule `json:"recurrenceRule"`
		RecurrenceExceptions []string       `json:"recurrenceExceptions"`
	} `json:"event"`
	RangeStart      string   `json:"rangeStart"`
	RangeEnd        string   `json:"rangeEnd"`
	ExpectedStartAt []string `json:"expectedStartAt"`
}

func loadRecurrenceGolden(t *testing.T) []recurrenceGoldenFixture {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	require.True(t, ok)
	dir := filepath.Dir(file)
	for {
		candidate := filepath.Join(dir, "testdata", "recurrence_golden.json")
		if b, err := os.ReadFile(candidate); err == nil {
			var out []recurrenceGoldenFixture
			require.NoError(t, json.Unmarshal(b, &out))
			return out
		}
		parent := filepath.Dir(dir)
		require.NotEqual(t, dir, parent, "could not find testdata/recurrence_golden.json")
		dir = parent
	}
}
