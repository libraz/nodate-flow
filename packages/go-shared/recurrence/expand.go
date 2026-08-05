// Package recurrence expands a stored recurrence rule into the concrete
// occurrences that fall inside a time range.
//
// The rule grammar is the closed set the schema stores — freq, interval,
// byDay, byMonthDay, until, count — not RFC 5545 in general. Nothing
// here parses an RRULE string; callers hand over the JSON the
// calendar_events.recurrence_rule column holds.
//
// This is a port of the browser expander (packages/ui/src/calendar/
// recurrence.ts), and the port exists because the two had to agree.
// Clients expand series themselves from the rule the REST list returns,
// while an agent asking "what is on Tuesday" over MCP gets whatever the
// server produces. With no server-side expander the agent surface simply
// dropped every recurring event, so the standing meetings were missing
// from the schedule and their hours were reported as free.
//
// Two properties are easy to lose in a reimplementation and are what the
// tests here mostly cover:
//
//   - Occurrences are computed from the anchor, never from the previous
//     occurrence. Chaining month additions off an already-clamped value
//     turns Jan 31 into Feb 28 and then pins every later month to the
//     28th; from the anchor each occurrence clamps independently and the
//     series reads Jan 31, Feb 28, Mar 31, Apr 30.
//   - Arithmetic is on wall-clock components in the event's own
//     timezone, so a 09:00 meeting stays at 09:00 across a DST boundary
//     rather than sliding to 08:00 or 10:00.
package recurrence

import (
	"encoding/json"
	"math"
	"strings"
	"time"
)

// Freq mirrors the freq values the rule grammar allows.
type Freq string

const (
	FreqDaily   Freq = "daily"
	FreqWeekly  Freq = "weekly"
	FreqMonthly Freq = "monthly"
	FreqYearly  Freq = "yearly"
)

// Rule is the stored recurrence rule.
//
// BySetPos is accepted and ignored: the column's grammar lists it, no
// writer sets it, and the browser expander does not read it either.
// Silently accepting it keeps the two implementations agreeing on the
// rules that actually exist rather than inventing a meaning here.
type Rule struct {
	Freq       Freq     `json:"freq"`
	Interval   *int     `json:"interval"`
	ByDay      []string `json:"byDay"`
	ByMonthDay []int    `json:"byMonthDay"`
	BySetPos   []int    `json:"bySetPos"`
	Until      string   `json:"until"`
	Count      *int     `json:"count"`
}

// Event is the minimal projection of a recurring calendar_events row.
type Event struct {
	StartAt time.Time
	EndAt   time.Time
	// Timezone is the IANA name the event's wall clock is expressed in.
	// Empty means UTC.
	Timezone string
	Rule     *Rule
	// Exceptions are the stored recurrence_exceptions entries: either
	// full timestamps naming one occurrence, or bare YYYY-MM-DD dates
	// naming a local day.
	Exceptions []string
}

// Occurrence is one concrete instance of a series.
type Occurrence struct {
	StartAt time.Time
	EndAt   time.Time
}

// maxIterations bounds the candidate scan so a rule with no Until and no
// Count cannot spin when the range is far from the anchor. Weekly BYDAY
// rules step one day at a time, so the bound is in days: roughly twenty
// years, well past any range a calendar UI or an agent asks for.
const maxIterations = 8000

var dayNumbers = map[string]time.Weekday{
	"su": time.Sunday,
	"mo": time.Monday,
	"tu": time.Tuesday,
	"we": time.Wednesday,
	"th": time.Thursday,
	"fr": time.Friday,
	"sa": time.Saturday,
}

// ParseRule decodes a stored recurrence_rule JSON value. A NULL or empty
// value, or the JSON literal null, yields (nil, nil): not recurring is
// not an error.
func ParseRule(raw []byte) (*Rule, error) {
	trimmed := strings.TrimSpace(string(raw))
	if trimmed == "" || trimmed == "null" {
		return nil, nil
	}
	var r Rule
	if err := json.Unmarshal(raw, &r); err != nil {
		return nil, err
	}
	if r.Freq == "" {
		return nil, nil
	}
	return &r, nil
}

// ParseExceptions decodes a stored recurrence_exceptions JSON array.
func ParseExceptions(raw []byte) []string {
	trimmed := strings.TrimSpace(string(raw))
	if trimmed == "" || trimmed == "null" {
		return nil
	}
	var out []string
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil
	}
	return out
}

// Expand returns the occurrences of evt that overlap [rangeStart, rangeEnd).
//
// An event with no rule yields nothing; callers merge non-recurring rows
// separately, because those need no expansion and carry their own
// identity.
func Expand(evt Event, rangeStart, rangeEnd time.Time) []Occurrence {
	if evt.Rule == nil || evt.Rule.Freq == "" {
		return nil
	}
	loc := loadLocation(evt.Timezone)
	anchor := evt.StartAt.In(loc)
	duration := evt.EndAt.Sub(evt.StartAt)

	interval := 1
	if evt.Rule.Interval != nil && *evt.Rule.Interval > 0 {
		interval = *evt.Rule.Interval
	}
	maxCount := math.MaxInt32
	if evt.Rule.Count != nil && *evt.Rule.Count >= 0 {
		maxCount = *evt.Rule.Count
	}
	var until *time.Time
	if u := parseUntil(evt.Rule.Until, loc); u != nil {
		until = u
	}

	byDay := normalizeByDay(evt.Rule.ByDay)
	byMonthDay := evt.Rule.ByMonthDay

	exactSkips, daySkips := buildExceptions(evt.Exceptions, loc)

	// RFC 5545 BYDAY expands a WEEKLY rule: WEEKLY;BYDAY=MO,WE yields an
	// occurrence on each listed weekday of every included week, not just
	// on the anchor's weekday. That path walks day by day and keeps a day
	// whose weekday is listed and whose week is an interval multiple away
	// from the anchor's. For every other combination byDay and byMonthDay
	// narrow the freq cursor instead of expanding it.
	expandsWeekdays := evt.Rule.Freq == FreqWeekly && len(byDay) > 0
	anchorWeekStart := startOfISOWeek(anchor)

	var out []Occurrence
	emitted := 0
	for n := 0; emitted < maxCount && n < maxIterations; n++ {
		var candidate time.Time
		if expandsWeekdays {
			candidate = addDaysWallClock(anchor, n)
		} else {
			candidate = occurrenceFromAnchor(anchor, evt.Rule.Freq, n*interval)
		}

		if !candidate.Before(rangeEnd) {
			break
		}
		if until != nil && candidate.After(*until) {
			break
		}

		var passes bool
		if expandsWeekdays {
			passes = matchesByDay(candidate, byDay) &&
				isoWeekOffset(candidate, anchorWeekStart)%interval == 0 &&
				(len(byMonthDay) == 0 || matchesByMonthDay(candidate, byMonthDay))
		} else {
			passes = (len(byDay) == 0 || matchesByDay(candidate, byDay)) &&
				(len(byMonthDay) == 0 || matchesByMonthDay(candidate, byMonthDay))
		}
		if !passes {
			continue
		}

		// A cancelled occurrence still counts against COUNT: the series
		// is "ten meetings", and cancelling one does not conjure an
		// eleventh at the end.
		emitted++
		if exactSkips[candidate.UTC().Unix()] || daySkips[candidate.Format("2006-01-02")] {
			continue
		}
		end := candidate.Add(duration)
		if end.After(rangeStart) {
			out = append(out, Occurrence{StartAt: candidate, EndAt: end})
		}
	}
	return out
}

// occurrenceFromAnchor returns the candidate `offset` freq-units after
// the anchor, computed from the anchor rather than from the previous
// candidate so each one clamps independently.
func occurrenceFromAnchor(anchor time.Time, freq Freq, offset int) time.Time {
	switch freq {
	case FreqDaily:
		return addDaysWallClock(anchor, offset)
	case FreqWeekly:
		return addDaysWallClock(anchor, offset*7)
	case FreqMonthly:
		return addMonthsClamped(anchor, offset)
	case FreqYearly:
		return addMonthsClamped(anchor, offset*12)
	}
	return anchor
}

// addDaysWallClock advances the wall clock by whole days in the event's
// own timezone, so a 09:00 meeting stays at 09:00 across a DST change.
func addDaysWallClock(t time.Time, days int) time.Time {
	y, m, d := t.Date()
	return time.Date(y, m, d+days, t.Hour(), t.Minute(), t.Second(), t.Nanosecond(), t.Location())
}

// addMonthsClamped advances by whole months, clamping the day to the
// length of the target month.
//
// Go's AddDate normalises overflow instead — Jan 31 plus one month lands
// on Mar 3 — which would make a monthly series anchored on the 31st skip
// February entirely and then land on the wrong day of March.
func addMonthsClamped(t time.Time, months int) time.Time {
	y, m, d := t.Date()
	total := int(m) - 1 + months
	targetYear := y + floorDiv(total, 12)
	targetMonth := time.Month(floorMod(total, 12) + 1)
	if maxDay := daysInMonth(targetYear, targetMonth); d > maxDay {
		d = maxDay
	}
	return time.Date(targetYear, targetMonth, d, t.Hour(), t.Minute(), t.Second(), t.Nanosecond(), t.Location())
}

func floorDiv(a, b int) int {
	q := a / b
	if a%b != 0 && (a < 0) != (b < 0) {
		q--
	}
	return q
}

func floorMod(a, b int) int { return a - floorDiv(a, b)*b }

func daysInMonth(year int, month time.Month) int {
	return time.Date(year, month+1, 0, 0, 0, 0, 0, time.UTC).Day()
}

// startOfISOWeek returns local midnight on the Monday of t's week.
func startOfISOWeek(t time.Time) time.Time {
	offset := (int(t.Weekday()) + 6) % 7 // Monday = 0
	y, m, d := t.Date()
	return time.Date(y, m, d-offset, 0, 0, 0, 0, t.Location())
}

// isoWeekOffset counts whole weeks between the anchor's week and the
// candidate's. Rounded because a DST transition makes the span between
// two week starts a non-integral number of 24-hour blocks.
func isoWeekOffset(candidate, anchorWeekStart time.Time) int {
	delta := startOfISOWeek(candidate).Sub(anchorWeekStart)
	return int(math.Round(delta.Hours() / (24 * 7)))
}

func matchesByDay(t time.Time, byDay []time.Weekday) bool {
	for _, d := range byDay {
		if t.Weekday() == d {
			return true
		}
	}
	return false
}

func matchesByMonthDay(t time.Time, byMonthDay []int) bool {
	for _, d := range byMonthDay {
		if t.Day() == d {
			return true
		}
	}
	return false
}

func normalizeByDay(values []string) []time.Weekday {
	var out []time.Weekday
	for _, v := range values {
		if d, ok := dayNumbers[strings.ToLower(strings.TrimSpace(v))]; ok {
			out = append(out, d)
		}
	}
	return out
}

// parseUntil reads UNTIL as an inclusive upper bound.
//
// A bare date means "through the end of that local day". Read at local
// midnight it would exclude every timed occurrence on the UNTIL day
// itself, which silently shortens the series by one.
func parseUntil(raw string, loc *time.Location) *time.Time {
	v := strings.TrimSpace(raw)
	if v == "" {
		return nil
	}
	if isDateOnly(v) {
		if d, err := time.ParseInLocation("2006-01-02", v, loc); err == nil {
			end := time.Date(d.Year(), d.Month(), d.Day(), 23, 59, 59, int(time.Second-time.Nanosecond), loc)
			return &end
		}
		return nil
	}
	if t, err := time.Parse(time.RFC3339, v); err == nil {
		local := t.In(loc)
		return &local
	}
	if t, err := time.ParseInLocation("2006-01-02T15:04:05", v, loc); err == nil {
		return &t
	}
	return nil
}

// buildExceptions splits the stored exception list into the two kinds it
// mixes: exact instants naming one occurrence, and bare dates naming a
// local day.
func buildExceptions(values []string, loc *time.Location) (map[int64]bool, map[string]bool) {
	exact := map[int64]bool{}
	days := map[string]bool{}
	for _, raw := range values {
		v := strings.TrimSpace(raw)
		if v == "" {
			continue
		}
		if isDateOnly(v) {
			days[v] = true
			continue
		}
		if t, err := time.Parse(time.RFC3339, v); err == nil {
			exact[t.UTC().Unix()] = true
			continue
		}
		if t, err := time.ParseInLocation("2006-01-02T15:04:05", v, loc); err == nil {
			exact[t.UTC().Unix()] = true
		}
	}
	return exact, days
}

func isDateOnly(v string) bool {
	if len(v) != 10 {
		return false
	}
	_, err := time.Parse("2006-01-02", v)
	return err == nil
}

func loadLocation(name string) *time.Location {
	if name == "" {
		return time.UTC
	}
	loc, err := time.LoadLocation(name)
	if err != nil {
		return time.UTC
	}
	return loc
}
