package calendar_event_day

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// fixedNow is the reference instant the day-boundary tests project into
// each test's workspace timezone. 2026-05-17T15:30:00Z is deliberately
// late enough in the UTC day that Asia/Tokyo (+09:00) sees it as the
// next calendar date, exercising the most common boundary bug.
var fixedNow = time.Date(2026, time.May, 17, 15, 30, 0, 0, time.UTC)

// mustLoad is a test helper that fails loudly when the system tzdata is
// missing the requested zone. Every Go toolchain ships with the embedded
// time/tzdata fallback path, but exercising the helper makes a missing
// tz an explicit test failure rather than a confusing nil-deref.
func mustLoad(t *testing.T, name string) *time.Location {
	t.Helper()
	loc, err := time.LoadLocation(name)
	require.NoErrorf(t, err, "tzdata missing zone %q", name)
	return loc
}

// TestEventDayInWorkspaceTz_Tokyo verifies that an early-evening UTC
// instant (15:30Z) projects to the *next* day in Tokyo (+09:00). This is
// the boundary case the W2 brief specifically calls out.
func TestEventDayInWorkspaceTz_Tokyo(t *testing.T) {
	t.Parallel()
	loc := mustLoad(t, "Asia/Tokyo")
	require.Equal(t, "2026-05-18", eventDayString(fixedNow, loc))
}

// TestEventDayInWorkspaceTz_UTC pins the trivial case: UTC observers see
// the same date as the system clock.
func TestEventDayInWorkspaceTz_UTC(t *testing.T) {
	t.Parallel()
	loc := mustLoad(t, "UTC")
	require.Equal(t, "2026-05-17", eventDayString(fixedNow, loc))
}

// TestEventDayInWorkspaceTz_NewYork covers a negative offset under DST.
// On 2026-05-17 New York is on EDT (UTC-04:00), so 15:30Z is 11:30 local
// on the same date.
func TestEventDayInWorkspaceTz_NewYork(t *testing.T) {
	t.Parallel()
	loc := mustLoad(t, "America/New_York")
	require.Equal(t, "2026-05-17", eventDayString(fixedNow, loc))
}

// TestEventDayBoundaryAtMidnight asserts the exact roll-over moment in
// Asia/Tokyo. 14:59:59Z is the last second of 2026-05-17 in Tokyo;
// 15:00:00Z is the first second of 2026-05-18.
func TestEventDayBoundaryAtMidnight(t *testing.T) {
	t.Parallel()
	loc := mustLoad(t, "Asia/Tokyo")

	beforeRoll := time.Date(2026, time.May, 17, 14, 59, 59, 0, time.UTC)
	afterRoll := time.Date(2026, time.May, 17, 15, 0, 0, 0, time.UTC)

	require.Equal(t, "2026-05-17", eventDayString(beforeRoll, loc),
		"14:59:59Z must still be 2026-05-17 in Tokyo")
	require.Equal(t, "2026-05-18", eventDayString(afterRoll, loc),
		"15:00:00Z must roll to 2026-05-18 in Tokyo")
}

// TestLocalDayUTCRange_Tokyo pins the half-open UTC range corresponding
// to the Tokyo local day that contains fixedNow. The query parameters in
// ListTodayEvents depend on these exact instants — a regression here
// would silently emit (or miss) events at the boundary.
func TestLocalDayUTCRange_Tokyo(t *testing.T) {
	t.Parallel()
	loc := mustLoad(t, "Asia/Tokyo")

	utcStart, utcEnd := localDayUTCRange(fixedNow, loc)

	require.Equal(t,
		time.Date(2026, time.May, 17, 15, 0, 0, 0, time.UTC),
		utcStart,
		"Tokyo 2026-05-18 starts at 15:00Z on 2026-05-17",
	)
	require.Equal(t,
		time.Date(2026, time.May, 18, 15, 0, 0, 0, time.UTC),
		utcEnd,
		"Tokyo 2026-05-18 ends at 15:00Z on 2026-05-18",
	)
	require.Equal(t, 24*time.Hour, utcEnd.Sub(utcStart),
		"a UTC range covering one local day must span 24h on a non-DST date")
}

// TestLocalDayUTCRange_NewYorkDSTSpringForward covers the only place
// where the range is NOT 24h: a spring-forward transition. On 2026-03-08
// New York advances 02:00 → 03:00 local, so the local day is 23h long
// in UTC terms.
func TestLocalDayUTCRange_NewYorkDSTSpringForward(t *testing.T) {
	t.Parallel()
	loc := mustLoad(t, "America/New_York")

	// noon on the spring-forward day, regardless of zone
	noonLocal := time.Date(2026, time.March, 8, 12, 0, 0, 0, loc)

	utcStart, utcEnd := localDayUTCRange(noonLocal, loc)

	require.Equal(t, 23*time.Hour, utcEnd.Sub(utcStart),
		"the spring-forward day in America/New_York must be 23h in UTC")
}

// TestEndOfDayUnixSeconds asserts the helper used to compute expiresAt
// returns the same instant localDayUTCRange's `end` resolves to. The
// signal's expiresAt is what drives the retention sweep, so a mismatch
// would silently leak signals past the day they describe.
func TestEndOfDayUnixSeconds(t *testing.T) {
	t.Parallel()
	loc := mustLoad(t, "Asia/Tokyo")

	got := endOfDayUnixSeconds(fixedNow, loc)
	want := time.Date(2026, time.May, 18, 15, 0, 0, 0, time.UTC).Unix()
	require.Equal(t, want, got)
}

// TestLocalDaysArriving_MidDayFiresNoDay is the fire-once guarantee: a
// tick that lands in the middle of a local day (no midnight crossed in
// the window) materialises zero days. Without this the scan would re-emit
// the same event-day on every tick (~1440 POST/event/day at a 60s
// cadence).
func TestLocalDaysArriving_MidDayFiresNoDay(t *testing.T) {
	t.Parallel()
	loc := mustLoad(t, "UTC")

	// Noon UTC, 60s window: the day's midnight is 12h behind, far outside
	// the window, so nothing arrives this tick.
	now := time.Date(2026, time.May, 17, 12, 0, 0, 0, time.UTC)
	days := localDaysArriving(now, time.Minute, loc)
	require.Empty(t, days, "a mid-day tick must materialise no event-day")
}

// TestLocalDaysArriving_BoundaryFiresOneDay pins the single-emit case:
// when the tick window straddles a local midnight, exactly the day that
// just began is returned. A subsequent mid-day tick (above) returns
// nothing, so the pair proves at-most-once-per-day at the window level.
func TestLocalDaysArriving_BoundaryFiresOneDay(t *testing.T) {
	t.Parallel()
	loc := mustLoad(t, "UTC")

	// 30s past UTC midnight with a 60s window: the boundary at 00:00Z is
	// inside [now-60s, now), so 2026-05-17 arrives exactly once.
	now := time.Date(2026, time.May, 17, 0, 0, 30, 0, time.UTC)
	days := localDaysArriving(now, time.Minute, loc)
	require.Len(t, days, 1, "the just-started day must arrive exactly once")
	require.Equal(t, "2026-05-17", days[0].day)
	require.Equal(t,
		time.Date(2026, time.May, 17, 0, 0, 0, 0, time.UTC), days[0].utcStart)
	require.Equal(t,
		time.Date(2026, time.May, 18, 0, 0, 0, 0, time.UTC), days[0].utcEnd)
}

// TestLocalDaysArriving_CatchUpYieldsMissedDays proves the self-heal: a
// wide catch-up window (a worker outage across local-day boundaries)
// returns every skipped day, oldest-first, so the next tick backfills
// them. The day-scoped dedupe key keeps the backfill idempotent.
func TestLocalDaysArriving_CatchUpYieldsMissedDays(t *testing.T) {
	t.Parallel()
	loc := mustLoad(t, "UTC")

	// Worker comes back at noon on 2026-05-19 after a multi-day outage; a
	// 3-day window must surface every local midnight it crossed.
	now := time.Date(2026, time.May, 19, 12, 0, 0, 0, time.UTC)
	window := 72 * time.Hour
	days := localDaysArriving(now, window, loc)

	got := make([]string, 0, len(days))
	for _, d := range days {
		got = append(got, d.day)
	}
	// Midnights in (now-72h, now): 2026-05-17, -05-18, -05-19. The 16th's
	// midnight (3.5 days back) is outside the window.
	require.Equal(t, []string{"2026-05-17", "2026-05-18", "2026-05-19"}, got,
		"catch-up must yield each crossed local day oldest-first")
}
