package calendaroccurrence

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/libraz/nodate-flow/packages/go-shared/recurrence"
)

var seriesAnchor = time.Date(2030, 3, 4, 9, 0, 0, 0, time.UTC)

func dailySeries(t *testing.T, start time.Time, raw string) recurrence.Event {
	t.Helper()
	rule, err := recurrence.ParseRule([]byte(raw))
	if err != nil || rule == nil {
		t.Fatalf("parse %s: %v", raw, err)
	}
	return recurrence.Event{StartAt: start, EndAt: start.Add(time.Hour), Timezone: "UTC", Rule: rule}
}

// A COUNT=10 series split at its fifth occurrence has used four; the
// continuing series carries the other six rather than starting over at
// ten.
func TestContinuingRuleKeepsTheRemainingCount(t *testing.T) {
	t.Parallel()
	raw := json.RawMessage(`{"freq":"daily","interval":1,"count":10}`)
	old := dailySeries(t, seriesAnchor, string(raw))

	got, err := continuingRule(raw, old, seriesAnchor.AddDate(0, 0, 4))
	if err != nil {
		t.Fatal(err)
	}
	rule, err := recurrence.ParseRule(got)
	if err != nil || rule == nil || rule.Count == nil {
		t.Fatalf("continuing rule %s does not parse with a count: %v", got, err)
	}
	if *rule.Count != 6 {
		t.Fatalf("continuing count = %d, want 6", *rule.Count)
	}
	if rule.Freq != recurrence.FreqDaily || rule.Interval == nil || *rule.Interval != 1 {
		t.Fatalf("the rest of the rule must carry over unchanged: %s", got)
	}
}

func TestContinuingRuleLeavesAnUnboundedRuleAlone(t *testing.T) {
	t.Parallel()
	raw := json.RawMessage(`{"freq":"daily","until":"2030-03-20"}`)
	got, err := continuingRule(raw, dailySeries(t, seriesAnchor, string(raw)), seriesAnchor.AddDate(0, 0, 4))
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(raw) {
		t.Fatalf("an until-bounded rule must carry over verbatim, got %s", got)
	}
}

// Moving the continuing series an hour later keeps a cancelled
// occurrence cancelled and a replaced one replaced, at the same position
// in the rule — not on an instant the new series never produces.
func TestSeriesMapCarriesOccurrencesToTheirNewInstants(t *testing.T) {
	t.Parallel()
	rule := `{"freq":"daily","until":"2030-03-08"}`
	old := dailySeries(t, seriesAnchor, rule)
	split := seriesAnchor.AddDate(0, 0, 2)
	next := dailySeries(t, split.Add(time.Hour), rule)

	cancelled := seriesAnchor.AddDate(0, 0, 3)
	replaced := seriesAnchor.AddDate(0, 0, 4)
	m := newSeriesMap(&old, split, next, replaced)

	got, ok := m.instant(replaced)
	if !ok || !got.Equal(replaced.Add(time.Hour)) {
		t.Fatalf("override target = %v (%v), want %v", got, ok, replaced.Add(time.Hour))
	}

	carried, changed := m.carryExceptions([]string{cancelled.Format(time.RFC3339)})
	if !changed || len(carried) != 1 || carried[0] != cancelled.Add(time.Hour).Format(time.RFC3339) {
		t.Fatalf("carried exceptions = %v (changed %v), want the cancelled occurrence an hour later", carried, changed)
	}
}

// A change of pattern leaves no position to keep, so an entry survives
// only where the new series still produces its instant.
func TestSeriesMapDropsWhatAChangedPatternNoLongerProduces(t *testing.T) {
	t.Parallel()
	old := dailySeries(t, seriesAnchor, `{"freq":"daily"}`)
	next := dailySeries(t, seriesAnchor, `{"freq":"weekly"}`)
	m := newSeriesMap(&old, seriesAnchor, next, seriesAnchor.AddDate(0, 0, 14))

	if _, ok := m.instant(seriesAnchor.AddDate(0, 0, 1)); ok {
		t.Fatal("a daily occurrence the weekly series does not produce must not survive")
	}
	if got, ok := m.instant(seriesAnchor.AddDate(0, 0, 7)); !ok || !got.Equal(seriesAnchor.AddDate(0, 0, 7)) {
		t.Fatal("an occurrence the weekly series still produces stays where it is")
	}
}

func TestSeriesMapLeavesAnUnmovedListAlone(t *testing.T) {
	t.Parallel()
	rule := `{"freq":"daily"}`
	old := dailySeries(t, seriesAnchor, rule)
	m := newSeriesMap(&old, seriesAnchor, old, seriesAnchor.AddDate(0, 0, 5))
	list := []string{"2030-03-06", seriesAnchor.AddDate(0, 0, 4).Format(time.RFC3339)}
	if carried, changed := m.carryExceptions(list); changed || len(carried) != 2 || carried[0] != list[0] {
		t.Fatalf("an unmoved series must keep its list as written, got %v (changed %v)", carried, changed)
	}
}

func TestKeepExceptionsBeforeDropsWhatTheTruncationRemoved(t *testing.T) {
	t.Parallel()
	split := seriesAnchor.AddDate(0, 0, 2)
	list := []string{
		seriesAnchor.AddDate(0, 0, 1).Format(time.RFC3339),
		"2030-03-06",
		seriesAnchor.AddDate(0, 0, 3).Format(time.RFC3339),
		"garbage",
	}
	kept, changed := keepExceptionsBefore(list, "UTC", split)
	if !changed || len(kept) != 2 || kept[0] != list[0] || kept[1] != "garbage" {
		t.Fatalf("kept = %v (changed %v)", kept, changed)
	}
}

// Two rules that differ in the position within the period or in the week
// start lay occurrences out differently, so an occurrence's position in
// one names nothing in the other.
func TestSamePatternSeesBySetPosAndWkst(t *testing.T) {
	t.Parallel()

	rule := func(raw string) *recurrence.Rule {
		t.Helper()
		r, err := recurrence.ParseRule([]byte(raw))
		if err != nil || r == nil {
			t.Fatalf("parse %s: %v", raw, err)
		}
		return r
	}
	cases := []struct {
		name string
		a, b string
		want bool
	}{
		{"same", `{"freq":"monthly","byDay":["FR"],"bySetPos":[-1]}`, `{"freq":"monthly","byDay":["FR"],"bySetPos":[-1]}`, true},
		{"set position differs", `{"freq":"monthly","byDay":["FR"],"bySetPos":[-1]}`, `{"freq":"monthly","byDay":["FR"],"bySetPos":[1]}`, false},
		{"set position dropped", `{"freq":"monthly","byDay":["FR"],"bySetPos":[1]}`, `{"freq":"monthly","byDay":["FR"]}`, false},
		{"week start differs", `{"freq":"weekly","interval":2,"byDay":["TU","SU"],"wkst":"MO"}`, `{"freq":"weekly","interval":2,"byDay":["TU","SU"],"wkst":"SU"}`, false},
		{"unset week start is Monday", `{"freq":"weekly","byDay":["TU"]}`, `{"freq":"weekly","byDay":["TU"],"wkst":"MO"}`, true},
	}
	for _, tc := range cases {
		if got := samePattern(rule(tc.a), rule(tc.b)); got != tc.want {
			t.Errorf("%s: samePattern = %v, want %v", tc.name, got, tc.want)
		}
	}
}
