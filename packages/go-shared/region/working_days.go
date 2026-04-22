// Package region — working-day / working-hour resolver (R5.12).
//
// A workspace and each user carry a 7-character working-day string
// (Mon..Sun) plus working-hour window. The string format is letter =
// working, underscore = off (e.g. "MTWTF__" = Mon–Fri). This file
// exposes the plain-function API itemkit, reconciler, and UI helpers
// use to answer "is date D a working day for user U?" without
// reaching into sqlc types (the columns are plain string / time.Time,
// so no generated enum is needed).
package region

import (
	"fmt"
	"strings"
	"time"
)

// WorkingDaysDefault is the fallback string when neither user nor
// workspace sets one. Monday through Friday, weekends off.
const WorkingDaysDefault = "MTWTF__"

// WorkingDaysLength is the fixed length of a working-days string
// (Mon..Sun). Validation rejects any other length.
const WorkingDaysLength = 7

// WorkingHoursStartDefault and WorkingHoursEndDefault are the fallback
// window when neither user nor workspace sets one. 09:00 – 18:00.
var (
	WorkingHoursStartDefault = mustParseClock("09:00")
	WorkingHoursEndDefault   = mustParseClock("18:00")
)

// ValidateWorkingDays checks the CHAR(7) format: exactly 7 runes, each
// either a letter (working) or '_' (off). Letters are not
// case-validated against their weekday position because users may
// prefer "MTWTF__" (romaji) or "月火水木金__" (kanji) — any non-'_' rune
// means "on". Empty string is accepted and resolved to the default.
func ValidateWorkingDays(s string) error {
	if s == "" {
		return nil
	}
	if len(s) != WorkingDaysLength {
		return fmt.Errorf("working_days must be exactly %d chars, got %d", WorkingDaysLength, len(s))
	}
	return nil
}

// EffectiveWorkingDays returns the resolved working-days string for a
// user in a workspace. Empty user value inherits from workspace;
// empty workspace value inherits from WorkingDaysDefault.
func EffectiveWorkingDays(userDays, workspaceDays string) string {
	if len(userDays) == WorkingDaysLength {
		return userDays
	}
	if len(workspaceDays) == WorkingDaysLength {
		return workspaceDays
	}
	return WorkingDaysDefault
}

// EffectiveWorkingHours resolves the working-hour window. A nil user
// value falls back to the workspace window; nil workspace window
// falls back to the hard-coded 09:00–18:00 default. Inputs are TIME
// values (date ignored); outputs preserve only the hour/minute of day.
func EffectiveWorkingHours(userStart, userEnd, wsStart, wsEnd *time.Time) (start, end time.Time) {
	pick := func(u, w *time.Time, fallback time.Time) time.Time {
		if u != nil && !u.IsZero() {
			return *u
		}
		if w != nil && !w.IsZero() {
			return *w
		}
		return fallback
	}
	return pick(userStart, wsStart, WorkingHoursStartDefault),
		pick(userEnd, wsEnd, WorkingHoursEndDefault)
}

// IsWorkingDay returns true when d falls on a day marked working in
// the effective working-days string AND is not in the holidays set.
// Holidays is a set of ISO-date strings (YYYY-MM-DD) in the target
// timezone; IsWorkingDay renders d in loc to compare. treatHolidays
// controls whether the holidays set suppresses the day; when false,
// holidays are still working days even if subscribed.
func IsWorkingDay(days string, d time.Time, loc *time.Location, holidays map[string]struct{}, treatHolidays bool) bool {
	if loc == nil {
		loc = time.UTC
	}
	local := d.In(loc)
	idx := weekdayIndex(local.Weekday())
	effective := days
	if len(effective) != WorkingDaysLength {
		effective = WorkingDaysDefault
	}
	if rune(effective[idx]) == '_' {
		return false
	}
	if treatHolidays && len(holidays) > 0 {
		key := local.Format("2006-01-02")
		if _, ok := holidays[key]; ok {
			return false
		}
	}
	return true
}

// NextWorkingDay returns the first working day on or after d. The
// returned time preserves d's clock fields (hour/minute/second) but
// rolls the date forward until IsWorkingDay is true. Caps at 366
// iterations so a pathological all-off string can't loop forever —
// returns d unchanged in that case.
func NextWorkingDay(days string, d time.Time, loc *time.Location, holidays map[string]struct{}, treatHolidays bool) time.Time {
	cursor := d
	for i := 0; i < 366; i++ {
		if IsWorkingDay(days, cursor, loc, holidays, treatHolidays) {
			return cursor
		}
		cursor = cursor.AddDate(0, 0, 1)
	}
	return d
}

// SnapMode mirrors users.snap_to_working_day.
type SnapMode string

const (
	SnapOff  SnapMode = "off"
	SnapWarn SnapMode = "warn"
	SnapAuto SnapMode = "auto"
)

// ParseSnapMode returns the enum for a DB string; unknown values
// default to SnapWarn (the column default).
func ParseSnapMode(s string) SnapMode {
	switch strings.ToLower(s) {
	case "off":
		return SnapOff
	case "auto":
		return SnapAuto
	default:
		return SnapWarn
	}
}

// weekdayIndex maps time.Weekday (Sunday=0..Saturday=6) to our 0=Mon
// index so the working-days string aligns with how humans usually
// write a week (Mon first).
func weekdayIndex(w time.Weekday) int {
	// time.Sunday = 0, time.Monday = 1, ..., time.Saturday = 6
	// We want Monday=0, ..., Sunday=6.
	return (int(w) + 6) % 7
}

func mustParseClock(hhmm string) time.Time {
	t, err := time.Parse("15:04", hhmm)
	if err != nil {
		panic("region: invalid default clock: " + hhmm)
	}
	return t
}
