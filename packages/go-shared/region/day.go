package region

import (
	"database/sql/driver"
	"fmt"
	"time"
)

// dayLayout is the wire and column form of a calendar date, the `*_on`
// shape the API convention fixes.
const dayLayout = "2006-01-02"

// Day is a wall-clock calendar date: a year, a month and a day, with no
// time of day and no zone.
//
// It is deliberately not a [time.Time]. A date carried as a time.Time is
// indistinguishable at the type level from the instant it was derived
// from, so the two get passed to the same functions and the compiler has
// nothing to say about it. That is the whole defect class this type
// closes: a task deadline read out of an event's start instant, an
// all-day row bucketed by the reader's local date rather than the
// author's, a "due today" that flips at a midnight belonging to nobody.
//
// The only ways across the boundary are [DayOf] (instant to date) and
// [Day.Start] / [Day.EndExclusive] / [Day.At] (date to instant), and
// every one of them takes a [Zone] parameter. There is no zone-free
// conversion to omit, so a day boundary computed without saying whose
// day it is does not compile.
//
// [ParseDay] and [Day.DateColumn] are not conversions in that sense:
// both sides of those are already zone-free, a `YYYY-MM-DD` string on
// one and a MySQL DATE column on the other.
type Day struct {
	year  int
	month time.Month
	day   int
}

// DayOf returns the calendar date the instant t falls on when read in z.
//
// This is the answer to "which day is this", and it has no answer without
// a zone: a Tokyo 08:00 meeting is on the previous day in UTC, which is
// how a task deadline ended up a day earlier than the meeting that set
// it.
func DayOf(t time.Time, z Zone) Day {
	y, m, d := t.In(z.Location()).Date()
	return Day{year: y, month: m, day: d}
}

// NewDay returns the calendar date with the given components, normalised
// the way [time.Date] normalises: NewDay(2026, time.January, 32) is
// 2026-02-01.
func NewDay(year int, month time.Month, day int) Day {
	t := time.Date(year, month, day, 0, 0, 0, 0, time.UTC)
	return Day{year: t.Year(), month: t.Month(), day: t.Day()}
}

// ParseDay reads the `YYYY-MM-DD` wire form of an `*_on` field.
//
// No zone is involved because none is missing: the string states a
// calendar date and nothing else. Parsing it into a time.Time is where a
// zone has to be guessed, and this returns a [Day] so that there is
// nothing to guess.
func ParseDay(s string) (Day, error) {
	t, err := time.Parse(dayLayout, s)
	if err != nil {
		return Day{}, fmt.Errorf("region: %q is not a YYYY-MM-DD date: %w", s, err)
	}
	return Day{year: t.Year(), month: t.Month(), day: t.Day()}, nil
}

// DayFromDateColumn reads a MySQL DATE column back into a [Day].
//
// A DATE column holds a date with no zone, and the driver hands it over
// as midnight in its configured location. Only the calendar components
// are read, so the value survives whichever location that is.
func DayFromDateColumn(t time.Time) Day {
	y, m, d := t.Date()
	return Day{year: y, month: m, day: d}
}

// Start returns the instant at which the day begins in z.
//
// Built through [time.Date] so a zone whose midnight does not exist —
// some zones spring forward at 00:00 — is normalised by the zoneinfo
// database rather than landing an hour into the previous day.
func (d Day) Start(z Zone) time.Time {
	return time.Date(d.year, d.month, d.day, 0, 0, 0, 0, z.Location())
}

// EndExclusive returns the instant at which the next day begins in z, the
// upper bound of the half-open range [Start, EndExclusive) that covers
// exactly this day.
//
// The next midnight is constructed rather than derived by adding 24h, so
// a DST transition day is 23 or 25 hours wide as it actually was.
func (d Day) EndExclusive(z Zone) time.Time {
	return time.Date(d.year, d.month, d.day+1, 0, 0, 0, 0, z.Location())
}

// At returns the instant at the given wall clock on this day in z. It is
// what a default like "09:00 on the due date" needs, and it is the only
// way to get one: composing the same value from [Day.Start] plus a
// duration slides by an hour across a DST transition.
func (d Day) At(z Zone, hour, minute, second int) time.Time {
	return time.Date(d.year, d.month, d.day, hour, minute, second, 0, z.Location())
}

// DateColumn renders the day as the [time.Time] a MySQL DATE column
// round-trips without a shift: midnight UTC.
//
// The carrier matters. The driver converts a time.Time parameter into its
// configured location before formatting it, so midnight in Asia/Tokyo
// stores the previous day. Midnight UTC is also what a bare
// `time.Parse("2006-01-02", ...)` produces, which is the shape the rest
// of the `*_on` handling already uses.
func (d Day) DateColumn() time.Time {
	return time.Date(d.year, d.month, d.day, 0, 0, 0, 0, time.UTC)
}

// String renders the day in the `YYYY-MM-DD` wire form.
func (d Day) String() string {
	return fmt.Sprintf("%04d-%02d-%02d", d.year, int(d.month), d.day)
}

// AddDays returns the day n calendar days later, normalised across month
// and year ends. Negative n moves backwards.
func (d Day) AddDays(n int) Day {
	return NewDay(d.year, d.month, d.day+n)
}

// Sub returns the number of whole calendar days from other to d.
//
// Counted on the calendar, not as an instant difference divided by 24h:
// a DST transition makes a day 23 or 25 hours long, and two instants
// twelve hours apart can sit on the same date or on different ones
// depending on where midnight falls. Both matter because the result is
// applied to task dates, which have day precision.
func (d Day) Sub(other Day) int {
	return int(d.DateColumn().Sub(other.DateColumn()).Hours() / 24)
}

// Equal reports whether the two values name the same calendar date.
func (d Day) Equal(other Day) bool { return d == other }

// Before reports whether d falls earlier in the calendar than other.
func (d Day) Before(other Day) bool { return d.DateColumn().Before(other.DateColumn()) }

// After reports whether d falls later in the calendar than other.
func (d Day) After(other Day) bool { return other.Before(d) }

// Weekday returns the day of the week the date falls on. A calendar date
// has one regardless of zone, so no zone is asked for.
func (d Day) Weekday() time.Weekday { return d.DateColumn().Weekday() }

// IsZero reports whether d is the zero value, which names no date.
func (d Day) IsZero() bool { return d == Day{} }

// Value implements [driver.Valuer] so a Day can be bound straight to a
// DATE column.
//
// Query parameters are `...any`, so the compiler has nothing to say
// about what lands in one. Without this a Day reaching a `?` would be
// rejected by the driver at run time, and — worse — the obvious fix is
// to reach for a time.Time, which is the shape whose location the driver
// rewrites. Binding through [Day.DateColumn] makes the correct carrier
// the one that costs nothing to write.
//
// The zero Day names no date and binds as NULL, matching how the `*_on`
// columns already spell "unset".
func (d Day) Value() (driver.Value, error) {
	if d.IsZero() {
		return nil, nil
	}
	return d.DateColumn(), nil
}

// Scan implements [sql.Scanner] so a DATE column reads back as a Day
// without passing through a time.Time somebody then has to remember not
// to read in a zone.
//
// A NULL column yields the zero Day.
func (d *Day) Scan(src any) error {
	switch v := src.(type) {
	case nil:
		*d = Day{}
		return nil
	case time.Time:
		*d = DayFromDateColumn(v)
		return nil
	case []byte:
		return d.scanString(string(v))
	case string:
		return d.scanString(v)
	}
	return fmt.Errorf("region: cannot scan %T into a Day", src)
}

func (d *Day) scanString(s string) error {
	// A DATE column with a zero date arrives as "0000-00-00" when the
	// server is not in strict mode. It names no date, so it reads as the
	// zero Day rather than as a parse failure.
	if s == "" || s == "0000-00-00" {
		*d = Day{}
		return nil
	}
	parsed, err := ParseDay(s)
	if err != nil {
		return err
	}
	*d = parsed
	return nil
}
