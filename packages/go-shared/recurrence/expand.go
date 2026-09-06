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

	"github.com/libraz/nodate-flow/packages/go-shared/region"
)

// Freq mirrors the freq values the rule grammar allows.
type Freq string

const (
	FreqDaily   Freq = "daily"
	FreqWeekly  Freq = "weekly"
	FreqMonthly Freq = "monthly"
	FreqYearly  Freq = "yearly"
)

// Valid reports whether f is one of the frequencies the grammar defines.
//
// Nothing else can be stepped: occurrenceFromAnchor has no arm for an
// unrecognised value and hands the anchor back unchanged, so a scan of a
// rule carrying one would sit on the same instant and emit it once per
// step. Callers that must reject a malformed stored rule loudly rather
// than expand it to nothing check this before expanding.
func (f Freq) Valid() bool {
	switch f {
	case FreqDaily, FreqWeekly, FreqMonthly, FreqYearly:
		return true
	}
	return false
}

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
	// OverriddenStarts are the occurrences a separate override row
	// already stands in for: the recurrence_original_start of every row
	// naming this event in recurrence_parent_id. Same two shapes as
	// Exceptions.
	//
	// A second input rather than more entries in Exceptions, because
	// the two say different things about the same occurrence: an
	// exception says it does not happen, an overridden start says it
	// happens elsewhere and the override row draws it. Merged into one
	// list the expander could no longer tell a consumer which, and the
	// master would still have to suppress both. Left unread the master
	// emits the original occurrence while the override row renders at
	// its moved time, so the same occurrence appears twice.
	OverriddenStarts []string
	// RecurrenceEnd is the calendar_events.recurrence_end column: an
	// inclusive last instant for the series, stored alongside the rule
	// rather than inside it.
	//
	// It is a second upper bound, not a replacement for the rule's own
	// UNTIL, and whichever comes first wins. Ignoring it — which is what
	// the browser expander does — means a series the API was told to
	// stop keeps being drawn forever, on the authenticated calendar and
	// on the public share page alike.
	RecurrenceEnd *time.Time
}

// Occurrence is one concrete instance of a series.
type Occurrence struct {
	StartAt time.Time
	EndAt   time.Time
}

// minScanSteps and maxScanSteps bound the candidate scan. The scan ends
// on its own as soon as a candidate reaches rangeEnd, so the budget only
// has to cover the walk from the anchor to the window; the ceiling is
// there so a range in some far century still terminates.
const (
	minScanSteps = 8000
	maxScanSteps = 1_000_000
)

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
	if evt.Rule == nil || !evt.Rule.Freq.Valid() {
		return nil
	}
	loc := zoneFor(evt.Timezone).Location()
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
	// Two independent upper bounds, and the earlier one ends the series.
	var until *time.Time
	if u := parseUntil(evt.Rule.Until, loc); u != nil {
		until = u
	}
	if evt.RecurrenceEnd != nil {
		end := evt.RecurrenceEnd.In(loc)
		if until == nil || end.Before(*until) {
			until = &end
		}
	}

	byDay := normalizeByDay(evt.Rule.ByDay)
	byMonthDay := evt.Rule.ByMonthDay

	exactSkips, daySkips := buildExceptions(evt.Exceptions, loc)
	overriddenExact, overriddenDays := buildExceptions(evt.OverriddenStarts, loc)

	// RFC 5545 BYDAY expands a WEEKLY rule: WEEKLY;BYDAY=MO,WE yields an
	// occurrence on each listed weekday of every included week, not just
	// on the anchor's weekday. That path walks day by day and keeps a day
	// whose weekday is listed and whose week is an interval multiple away
	// from the anchor's. For every other combination byDay and byMonthDay
	// narrow the freq cursor instead of expanding it.
	expandsWeekdays := evt.Rule.Freq == FreqWeekly && len(byDay) > 0
	anchorWeekStart := startOfISOWeek(anchor)
	budget := scanBudget(anchor, rangeEnd, evt.Rule.Freq, interval, expandsWeekdays)

	var out []Occurrence
	emitted := 0
	for n := 0; emitted < maxCount && n < budget; n++ {
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
		// eleventh at the end. A replaced one counts for the stronger
		// reason that it still happens — it was moved, not cancelled —
		// so it consumes a count exactly as an ordinary occurrence
		// does, and the ten meetings are still ten.
		emitted++
		if exactSkips[candidate.UTC().Unix()] || daySkips[candidate.Format("2006-01-02")] {
			continue
		}
		// Suppressed by an override row, which renders this occurrence
		// at its own time. A separate check from the exception one, so
		// which of the two applies stays readable here.
		if overriddenExact[candidate.UTC().Unix()] || overriddenDays[candidate.Format("2006-01-02")] {
			continue
		}
		end := candidate.Add(duration)
		if end.After(rangeStart) {
			out = append(out, Occurrence{StartAt: candidate, EndAt: end})
		}
	}
	return out
}

// scanBudget returns how many candidates the scan may examine before
// giving up.
//
// The budget is derived from the distance between the anchor and the far
// edge of the window, because that is how many steps it takes to get
// there. A fixed budget truncates instead, and silently: a daily series
// anchored two decades before the window runs out of steps on the way and
// the window comes back empty, which reads as "nothing is scheduled" to
// an agent and as "no reminder is due" to the scheduler.
//
// perDay marks the weekly-BYDAY path, which walks one day at a time
// regardless of the rule's interval.
func scanBudget(anchor, rangeEnd time.Time, freq Freq, interval int, perDay bool) int {
	if interval <= 0 {
		interval = 1
	}
	// Sub saturates rather than wrapping, so a range centuries out yields
	// a large step count that the ceiling below then caps.
	days := int(rangeEnd.Sub(anchor).Hours() / 24)

	needed := 0
	switch {
	case perDay:
		needed = days
	case freq == FreqDaily:
		needed = days / interval
	case freq == FreqWeekly:
		needed = days / 7 / interval
	case freq == FreqMonthly:
		needed = monthsBetween(anchor, rangeEnd) / interval
	case freq == FreqYearly:
		needed = monthsBetween(anchor, rangeEnd) / 12 / interval
	}
	// Two spare steps absorb the rounding in the estimate itself: a
	// partial period at either end still needs a candidate of its own.
	needed += 2

	if needed < minScanSteps {
		return minScanSteps
	}
	if needed > maxScanSteps {
		return maxScanSteps
	}
	return needed
}

// monthsBetween counts whole months from one instant to another, read on
// the anchor's own calendar so the count is not skewed by month lengths.
func monthsBetween(from, to time.Time) int {
	end := to.In(from.Location())
	return (end.Year()-from.Year())*12 + int(end.Month()) - int(from.Month())
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

// buildExceptions splits a stored list of occurrence starts into the two
// kinds it mixes: exact instants naming one occurrence, and bare dates
// naming a local day.
//
// Exceptions and overridden starts are both read through here, so a
// start written in either list matches the same candidate.
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

// zoneFor resolves the event's stored timezone through the one resolver.
//
// An absent or unresolvable name yields UTC, which is what the schema
// says the column falls back to and what region.DefaultTimezone is.
// Erroring is not open to this function: expansion runs over stored rows
// on the notification path, where refusing a single broken row would
// take the whole scan down with it.
func zoneFor(name string) region.Zone {
	z, err := region.Resolve(name)
	if err != nil {
		return region.UTC()
	}
	return z
}
