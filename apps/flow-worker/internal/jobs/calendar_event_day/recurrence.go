package calendar_event_day

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"
)

// recurrenceRule is the worker-side decode of the calendar_events
// recurrence_rule JSON column. It mirrors the canonical grammar the
// flow-api validator accepts (apps/flow-api/.../recurrence_validation.go)
// and the client expander consumes (packages/ui/src/calendar/recurrence.ts):
// freq is the lowercase FREQ token, interval defaults to 1, byDay/byMonthDay
// filter candidate occurrences, and until/count cap the sequence.
//
// bySetPos is decoded for completeness but not applied — see expandOccurrences
// for the supported-rule boundary.
type recurrenceRule struct {
	Freq       string   `json:"freq"`
	Interval   *int     `json:"interval"`
	ByDay      []string `json:"byDay"`
	ByMonthDay []int    `json:"byMonthDay"`
	BySetPos   *int     `json:"bySetPos"`
	Until      *string  `json:"until"`
	Count      *int     `json:"count"`
}

// isoWeekdayForToken maps an RFC 5545 two-letter weekday token to the Go
// time.Weekday it selects. Tokens are upper-cased before lookup so the
// client's uppercase byDay values resolve regardless of source casing.
var isoWeekdayForToken = map[string]time.Weekday{
	"SU": time.Sunday,
	"MO": time.Monday,
	"TU": time.Tuesday,
	"WE": time.Wednesday,
	"TH": time.Thursday,
	"FR": time.Friday,
	"SA": time.Saturday,
}

// parseRecurrenceRule decodes the recurrence_rule JSON column. It returns
// (nil, nil) when the column is SQL NULL, an empty byte slice, or the JSON
// literal null so a non-recurring row flows through the caller's
// single-occurrence path. A malformed or unsupported freq yields an error
// so the scan skips the row loudly rather than silently emitting nothing.
func parseRecurrenceRule(raw []byte) (*recurrenceRule, error) {
	trimmed := strings.TrimSpace(string(raw))
	if trimmed == "" || trimmed == "null" {
		return nil, nil
	}
	var rule recurrenceRule
	if err := json.Unmarshal([]byte(trimmed), &rule); err != nil {
		return nil, fmt.Errorf("decode recurrence_rule: %w", err)
	}
	switch rule.Freq {
	case "daily", "weekly", "monthly", "yearly":
	default:
		return nil, fmt.Errorf("unsupported recurrence freq %q", rule.Freq)
	}
	return &rule, nil
}

type recurrenceExceptions struct {
	instants     map[int64]struct{}
	localDayKeys map[string]struct{}
}

// parseRecurrenceExceptions decodes the recurrence_exceptions JSON column
// (an array of ISO 8601 date / datetime strings). RFC3339 timestamps exclude
// that exact UTC instant. Bare YYYY-MM-DD dates exclude every occurrence whose
// event-local calendar day matches the date, so timed events are suppressed
// as users expect.
func parseRecurrenceExceptions(raw []byte, loc *time.Location) (*recurrenceExceptions, error) {
	trimmed := strings.TrimSpace(string(raw))
	if trimmed == "" || trimmed == "null" {
		return nil, nil
	}
	var values []string
	if err := json.Unmarshal([]byte(trimmed), &values); err != nil {
		return nil, fmt.Errorf("decode recurrence_exceptions: %w", err)
	}
	out := &recurrenceExceptions{
		instants:     make(map[int64]struct{}, len(values)),
		localDayKeys: make(map[string]struct{}, len(values)),
	}
	for _, v := range values {
		v = strings.TrimSpace(v)
		if v == "" {
			continue
		}
		if t, err := time.Parse(time.RFC3339, v); err == nil {
			out.instants[t.Unix()] = struct{}{}
			continue
		}
		if t, err := time.ParseInLocation("2006-01-02", v, loc); err == nil {
			out.localDayKeys[localDayKey(t)] = struct{}{}
			continue
		}
		// Unparseable exception: ignore it. Skipping is safer than
		// dropping every occurrence on one typo'd exclusion.
	}
	return out, nil
}

func (e *recurrenceExceptions) excludes(t time.Time, loc *time.Location) bool {
	if e == nil {
		return false
	}
	if _, ok := e.instants[t.Unix()]; ok {
		return true
	}
	_, ok := e.localDayKeys[localDayKey(t.In(loc))]
	return ok
}

func localDayKey(t time.Time) string {
	y, m, d := t.Date()
	return strconv.Itoa(y) + "-" + twoDigits(int(m)) + "-" + twoDigits(d)
}

func twoDigits(v int) string {
	if v < 10 {
		return "0" + strconv.Itoa(v)
	}
	return strconv.Itoa(v)
}

// maxOccurrences is the minimum candidate-step budget expandOccurrences
// walks. The actual scan budget expands when the requested window is farther
// from the event's base occurrence, so old recurring masters still reach the
// current window instead of silently stopping after about eleven years.
const maxOccurrences = 4000

// expandOccurrences returns the occurrence start instants of a recurring
// event whose UTC start falls inside the half-open window [windowStart,
// windowEnd). base is the event's first occurrence (calendar_events.start_at,
// UTC). loc is the event timezone, used to anchor freq advances to wall-clock
// time so a DST transition does not drift a daily/weekly meeting by an hour.
//
// rule.Count and rule.Until cap the sequence the same way the client expander
// does: a candidate that fails the byDay/byMonthDay filter still consumes a
// count slot (matching packages/ui/src/calendar/recurrence.ts), and Until is
// an inclusive upper bound on the candidate instant. recurrenceEnd, when
// non-zero, is an additional exclusive upper bound mirroring the DB's
// computed recurrence_end column. exceptions excludes occurrences whose UTC
// instant is in the set.
//
// Supported rules: FREQ daily/weekly/monthly/yearly, INTERVAL, COUNT, UNTIL,
// BYDAY, BYMONTHDAY. The expander deliberately mirrors the client expander
// (packages/ui/src/calendar/recurrence.ts) rather than full RFC 5545, so the
// days the worker fires on are exactly the occurrences the calendar UI
// renders. Two consequences follow from that parity:
//
//   - BYDAY / BYMONTHDAY act as a filter on the freq cursor, not as an
//     intra-period multiplier. A weekly cursor advances a whole week per
//     step, so FREQ=WEEKLY;BYDAY=MO,WE,FR fires only on the base weekday's
//     occurrences, not on the other listed weekdays within each week. Full
//     intra-week BYDAY expansion would require matching the client first.
//   - BYSETPOS is NOT applied — a rule carrying it expands as if it were
//     absent. The client expander also ignores bySetPos today; if BYSETPOS
//     support lands client-side it must be mirrored here.
func expandOccurrences(
	rule *recurrenceRule,
	base time.Time,
	loc *time.Location,
	recurrenceEnd time.Time,
	until time.Time,
	exceptions *recurrenceExceptions,
	windowStart, windowEnd time.Time,
) []time.Time {
	interval := 1
	if rule.Interval != nil && *rule.Interval > 0 {
		interval = *rule.Interval
	}

	maxCount := -1 // unbounded
	if rule.Count != nil && *rule.Count > 0 {
		maxCount = *rule.Count
	}

	var out []time.Time
	cursor := base.In(loc)
	emitted := 0
	scanLimit := scanStepLimit(rule.Freq, interval, base, loc, windowEnd)

	for step := 0; step < scanLimit; step++ {
		if maxCount >= 0 && emitted >= maxCount {
			break
		}
		candidateUTC := cursor.UTC()

		// Inclusive UNTIL and exclusive recurrence_end bound the sequence;
		// once the cursor passes either, no later candidate can qualify.
		if !until.IsZero() && candidateUTC.After(until) {
			break
		}
		if !recurrenceEnd.IsZero() && !candidateUTC.Before(recurrenceEnd) {
			break
		}
		// The cursor only moves forward; once it reaches windowEnd every
		// later candidate is out of range too.
		if !candidateUTC.Before(windowEnd) {
			break
		}

		passesDay := len(rule.ByDay) == 0 || matchesByDay(cursor, rule.ByDay)
		passesMonthDay := len(rule.ByMonthDay) == 0 || matchesByMonthDay(cursor, rule.ByMonthDay)

		if passesDay && passesMonthDay {
			emitted++
			if !exceptions.excludes(candidateUTC, loc) {
				if !candidateUTC.Before(windowStart) {
					out = append(out, candidateUTC)
				}
			}
		}

		cursor = advanceByFreq(cursor, rule.Freq, interval, loc)
	}
	return out
}

func scanStepLimit(freq string, interval int, base time.Time, loc *time.Location, windowEnd time.Time) int {
	if interval <= 0 {
		interval = 1
	}
	if !base.Before(windowEnd) {
		return maxOccurrences
	}

	baseLocal := base.In(loc)
	endLocal := windowEnd.In(loc)
	needed := 0
	switch freq {
	case "daily":
		needed = int(endLocal.Sub(baseLocal).Hours()/24)/interval + 2
	case "weekly":
		needed = int(endLocal.Sub(baseLocal).Hours()/(24*7))/interval + 2
	case "monthly":
		by, bm, _ := baseLocal.Date()
		ey, em, _ := endLocal.Date()
		months := (ey-by)*12 + int(em-bm)
		needed = months/interval + 2
	case "yearly":
		by, _, _ := baseLocal.Date()
		ey, _, _ := endLocal.Date()
		needed = (ey-by)/interval + 2
	}
	if needed > maxOccurrences {
		return needed
	}
	return maxOccurrences
}

// matchesByDay reports whether the local weekday of t is in the byDay token
// list. Tokens are upper-cased before lookup so lowercase client values still
// resolve.
func matchesByDay(t time.Time, byDay []string) bool {
	wd := t.Weekday()
	for _, tok := range byDay {
		if want, ok := isoWeekdayForToken[strings.ToUpper(strings.TrimSpace(tok))]; ok && want == wd {
			return true
		}
	}
	return false
}

// matchesByMonthDay reports whether the local day-of-month of t is in the
// byMonthDay list.
func matchesByMonthDay(t time.Time, byMonthDay []int) bool {
	day := t.Day()
	for _, md := range byMonthDay {
		if md == day {
			return true
		}
	}
	return false
}

// advanceByFreq steps the cursor forward by one interval of freq, preserving
// the wall-clock time-of-day in loc. Constructing the next instant via
// time.Date in loc (rather than adding a fixed duration) keeps a daily 09:00
// meeting at 09:00 local across a DST transition, matching the client
// expander's luxon plus({...}) behaviour.
func advanceByFreq(cursor time.Time, freq string, interval int, loc *time.Location) time.Time {
	local := cursor.In(loc)
	y, m, d := local.Date()
	hh, mm, ss := local.Clock()
	switch freq {
	case "daily":
		d += interval
	case "weekly":
		d += 7 * interval
	case "monthly":
		m += time.Month(interval)
		d = min(d, daysInMonth(y, m, loc))
	case "yearly":
		y += interval
		d = min(d, daysInMonth(y, m, loc))
	}
	return time.Date(y, m, d, hh, mm, ss, local.Nanosecond(), loc)
}

func daysInMonth(year int, month time.Month, loc *time.Location) int {
	return time.Date(year, month+1, 0, 0, 0, 0, 0, loc).Day()
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
