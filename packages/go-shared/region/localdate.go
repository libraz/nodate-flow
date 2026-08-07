package region

import (
	"sync"
	"time"
)

// LoadLocation resolves an IANA timezone name to a [time.Location],
// memoising the result.
//
// [time.LoadLocation] reads the zoneinfo database on every call, and the
// callers here are loops: the drift reconciler resolves a zone per row
// on every pass, and every reschedule resolves one per write. Both
// failures and successes are cached, because an unresolvable name means
// a broken stored row, and a broken row is exactly what a loop revisits
// on a schedule.
func LoadLocation(tz string) (*time.Location, error) {
	if v, ok := locationCache.Load(tz); ok {
		e := v.(locationEntry)
		return e.loc, e.err
	}
	loc, err := time.LoadLocation(tz)
	if tz == "" {
		// time.LoadLocation("") answers UTC. Callers are meant to run the
		// name through EffectiveTimezone first, so an empty string here
		// is a caller that skipped the chain rather than a user who chose
		// UTC — reject it instead of guessing on their behalf.
		loc, err = nil, ErrInvalidTimezone
	}
	locationCache.Store(tz, locationEntry{loc: loc, err: err})
	return loc, err
}

type locationEntry struct {
	loc *time.Location
	err error
}

var locationCache sync.Map

// LocalDate returns the calendar date on which the instant t falls when
// read in tz, carried as midnight UTC.
//
// The carrier matters. A DATE column holds a date with no zone, and the
// MySQL driver converts a [time.Time] parameter into its configured
// location before formatting it — so handing it midnight in Asia/Tokyo
// stores the previous day. Midnight UTC is the shape the rest of the
// codebase already uses for `*_on` values (a bare `time.Parse` of
// "2006-01-02" produces exactly this), so it round-trips through both
// the driver and the wire without a shift.
//
// This is the answer to "which day is this event on", and it must be
// asked in the event's own timezone. Reading a Tokyo 08:00 meeting in
// UTC dates it to the day before, which is how a task's deadline ended
// up one day earlier than the meeting that set it.
func LocalDate(t time.Time, tz string) (time.Time, error) {
	loc, err := LoadLocation(tz)
	if err != nil {
		return time.Time{}, err
	}
	y, m, d := t.In(loc).Date()
	return time.Date(y, m, d, 0, 0, 0, 0, time.UTC), nil
}

// LocalDateString renders [LocalDate] as `YYYY-MM-DD`, the wire shape
// for `*_on` fields.
func LocalDateString(t time.Time, tz string) (string, error) {
	d, err := LocalDate(t, tz)
	if err != nil {
		return "", err
	}
	return d.Format("2006-01-02"), nil
}

// SameLocalDate reports whether two instants fall on the same calendar
// date in tz.
func SameLocalDate(a, b time.Time, tz string) (bool, error) {
	da, err := LocalDate(a, tz)
	if err != nil {
		return false, err
	}
	db, err := LocalDate(b, tz)
	if err != nil {
		return false, err
	}
	return da.Equal(db), nil
}

// LocalDayDelta returns the number of whole calendar days between the
// dates on which a and b fall in tz (b - a).
//
// Counting days as an instant difference divided by 24h is wrong twice
// over: a DST transition makes a day 23 or 25 hours long, and two
// instants twelve hours apart can sit on the same local day or on
// different ones depending on where midnight falls. Both matter here
// because the delta is applied to task dates, which have day precision.
func LocalDayDelta(a, b time.Time, tz string) (int, error) {
	da, err := LocalDate(a, tz)
	if err != nil {
		return 0, err
	}
	db, err := LocalDate(b, tz)
	if err != nil {
		return 0, err
	}
	// Both sides are midnight UTC, so the subtraction is exact whole
	// days regardless of what the zone did in between.
	return int(db.Sub(da).Hours() / 24), nil
}
