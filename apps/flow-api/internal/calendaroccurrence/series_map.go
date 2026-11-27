package calendaroccurrence

import (
	"encoding/json"
	"math"
	"time"

	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/generated/calendar"
	"github.com/libraz/nodate-flow/packages/go-shared/recurrence"
)

// continuingRule is the master's rule as the continuing series carries
// it: a COUNT shortened by the occurrences the truncated master already
// produced, so the two halves add up to the series the rule defined.
// UNTIL and recurrence_end are instants and carry over unchanged.
func continuingRule(raw json.RawMessage, old recurrence.Event, splitStart time.Time) (json.RawMessage, error) {
	if old.Rule == nil || old.Rule.Count == nil {
		return raw, nil
	}
	remaining := *old.Rule.Count - recurrence.CountBefore(old, splitStart)
	if remaining < 0 {
		remaining = 0
	}
	var members map[string]json.RawMessage
	if err := json.Unmarshal(raw, &members); err != nil {
		return nil, err
	}
	count, err := json.Marshal(remaining)
	if err != nil {
		return nil, err
	}
	members["count"] = count
	return json.Marshal(members)
}

// seriesMap carries occurrences of one series onto another.
//
// When the rule's pattern is unchanged an occurrence keeps its position:
// the third meeting from the split is still the third, at whatever instant
// the new anchor or zone gives it. Otherwise, and for anything past the
// new series' end, an occurrence survives only where the new series still
// produces its instant.
type seriesMap struct {
	next      recurrence.Event
	timezone  string
	oldStarts []time.Time
	newStarts []time.Time
	position  map[int64]int
}

// newSeriesMap builds the map for occurrences of old at or after from,
// up to reach. A nil old maps nothing by position.
func newSeriesMap(old *recurrence.Event, from time.Time, next recurrence.Event, reach time.Time) seriesMap {
	m := seriesMap{next: next, timezone: recurrence.ZoneName(next.Timezone, next.AllDay)}
	if old == nil {
		return m
	}
	m.timezone = recurrence.ZoneName(old.Timezone, old.AllDay)
	if !samePattern(old.Rule, next.Rule) {
		return m
	}
	m.oldStarts = recurrence.Starts(*old, from, math.MaxInt32, reach.Add(time.Second))
	m.newStarts = recurrence.Starts(next, next.StartAt, len(m.oldStarts), time.Time{})
	m.position = make(map[int64]int, len(m.oldStarts))
	for k, s := range m.oldStarts {
		m.position[s.UnixMilli()] = k
	}
	return m
}

// instant maps one occurrence start of the old series.
func (m seriesMap) instant(at time.Time) (time.Time, bool) {
	if k, ok := m.position[at.UnixMilli()]; ok && k < len(m.newStarts) {
		return m.newStarts[k], true
	}
	if recurrence.Produces(m.next, at) {
		return at, true
	}
	return time.Time{}, false
}

// entry maps one stored exception entry, keeping its spelling wherever
// the occurrence it names did not move.
func (m seriesMap) entry(raw string) (string, bool) {
	for k, s := range m.oldStarts {
		if !recurrence.EntryNames(raw, m.timezone, s) {
			continue
		}
		if k < len(m.newStarts) {
			if sameInstant(m.newStarts[k], s) {
				return raw, true
			}
			return m.newStarts[k].UTC().Format(time.RFC3339), true
		}
		break
	}
	if recurrence.ProducesEntry(m.next, raw) {
		return raw, true
	}
	return "", false
}

// carryExceptions maps a stored exception list, and reports whether the
// result differs from it.
func (m seriesMap) carryExceptions(list []string) ([]string, bool) {
	out := make([]string, 0, len(list))
	seen := map[string]bool{}
	changed := false
	for _, raw := range list {
		mapped, ok := m.entry(raw)
		if !ok || seen[mapped] {
			changed = true
			continue
		}
		if mapped != raw {
			changed = true
		}
		seen[mapped] = true
		out = append(out, mapped)
	}
	return out, changed
}

// keepExceptionsBefore drops the entries naming an occurrence at or after
// split, and reports whether any went. An entry that names nothing is
// kept: it is not this write's to judge.
func keepExceptionsBefore(list []string, timezone string, split time.Time) ([]string, bool) {
	out := make([]string, 0, len(list))
	for _, raw := range list {
		if latest, ok := recurrence.EntryLatest(raw, timezone); ok && !latest.Before(split) {
			continue
		}
		out = append(out, raw)
	}
	return out, len(out) != len(list)
}

// reachOf is the latest instant any exception or override names, which is
// how far the old series has to be walked to place them all.
func reachOf(timezone string, exceptions []string, overrides []calendar.ListCalendarEventOverridesFromStartRow) time.Time {
	var reach time.Time
	for _, raw := range exceptions {
		if latest, ok := recurrence.EntryLatest(raw, timezone); ok && latest.After(reach) {
			reach = latest
		}
	}
	for _, r := range overrides {
		if r.RecurrenceOriginalStart.Valid && r.RecurrenceOriginalStart.Time.After(reach) {
			reach = r.RecurrenceOriginalStart.Time
		}
	}
	return reach
}

// samePattern reports whether two rules lay occurrences out the same way,
// whatever their bounds: only then does an occurrence's position in one
// name an occurrence of the other.
func samePattern(a, b *recurrence.Rule) bool {
	if a == nil || b == nil || a.Freq != b.Freq || intervalOf(a) != intervalOf(b) {
		return false
	}
	return sameStrings(a.ByDay, b.ByDay) && sameInts(a.ByMonthDay, b.ByMonthDay) &&
		sameInts(a.BySetPos, b.BySetPos) && wkstOf(a) == wkstOf(b)
}

// wkstOf is the week start a rule expands with; an unset one is Monday.
func wkstOf(r *recurrence.Rule) string {
	if r.Wkst == "" {
		return "MO"
	}
	return r.Wkst
}

func intervalOf(r *recurrence.Rule) int {
	if r.Interval == nil || *r.Interval < 1 {
		return 1
	}
	return *r.Interval
}

func sameStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func sameInts(a, b []int) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// encodeExceptions writes an exception list back in the column's shape.
func encodeExceptions(list []string) (json.RawMessage, error) {
	if list == nil {
		list = []string{}
	}
	return json.Marshal(list)
}

// sameInstant compares two instants at the millisecond precision the
// columns store.
func sameInstant(a, b time.Time) bool {
	return a.UnixMilli() == b.UnixMilli()
}
