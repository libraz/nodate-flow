package recurrence

import (
	"testing"
	"time"
)

// countedDaily is a daily series of ten, anchored at 09:00 UTC.
func countedDaily() Event {
	anchor := time.Date(2030, 3, 4, 9, 0, 0, 0, time.UTC)
	return Event{
		StartAt: anchor,
		EndAt:   anchor.Add(time.Hour),
		Rule:    &Rule{Freq: FreqDaily, Count: intPtr(10)},
	}
}

// CountBefore is what a split subtracts from COUNT, so it has to count
// the way Expand consumes COUNT: cancelled occurrences included.
func TestCountBeforeCountsCancelledOccurrences(t *testing.T) {
	t.Parallel()
	evt := countedDaily()
	evt.Exceptions = []string{"2030-03-05T09:00:00Z"}
	split := evt.StartAt.AddDate(0, 0, 4)
	if got := CountBefore(evt, split); got != 4 {
		t.Fatalf("CountBefore = %d, want 4", got)
	}
	evt.Exceptions = nil
	if got := CountBefore(evt, evt.StartAt.AddDate(0, 0, 30)); got != 10 {
		t.Fatalf("CountBefore past the end = %d, want the whole count 10", got)
	}
}

func TestProducesAnswersForTheRuleNotTheExceptions(t *testing.T) {
	t.Parallel()
	evt := countedDaily()
	evt.Exceptions = []string{"2030-03-05T09:00:00Z"}
	if !Produces(evt, evt.StartAt.AddDate(0, 0, 1)) {
		t.Fatal("a cancelled occurrence is still one the rule produces")
	}
	if Produces(evt, evt.StartAt.AddDate(0, 0, 1).Add(time.Hour)) {
		t.Fatal("an instant off the rule's grid must not be produced")
	}
	if Produces(evt, evt.StartAt.AddDate(0, 0, 10)) {
		t.Fatal("the eleventh day is past COUNT")
	}
}

func TestStartsListsPositionsFromAPoint(t *testing.T) {
	t.Parallel()
	evt := countedDaily()
	got := Starts(evt, evt.StartAt.AddDate(0, 0, 8), 5, time.Time{})
	if len(got) != 2 {
		t.Fatalf("Starts = %v, want the last two of ten", got)
	}
	if !got[0].Equal(evt.StartAt.AddDate(0, 0, 8)) {
		t.Fatalf("first start = %v", got[0])
	}
}

func TestProducesEntryReadsBothSpellings(t *testing.T) {
	t.Parallel()
	evt := countedDaily()
	if !ProducesEntry(evt, "2030-03-06") {
		t.Fatal("a bare date naming a day the series meets on must be produced")
	}
	if !ProducesEntry(evt, "2030-03-06T09:00:00Z") {
		t.Fatal("an instant on the grid must be produced")
	}
	if ProducesEntry(evt, "2030-03-06T10:00:00Z") {
		t.Fatal("an instant off the grid must not be produced")
	}
	if ProducesEntry(evt, "not a date") {
		t.Fatal("an entry naming nothing is produced by nothing")
	}
}

// A split that keeps the remaining COUNT is the property the series
// transform relies on: the truncated master and the continuing one add
// up to the occurrences the original rule produced.
func TestSplitHalvesAddUpToTheOriginalCount(t *testing.T) {
	t.Parallel()
	original := countedDaily()
	split := original.StartAt.AddDate(0, 0, 4)
	window := original.StartAt.AddDate(0, 0, 60)

	truncateAt := split.Add(-time.Millisecond)
	head := original
	head.RecurrenceEnd = &truncateAt

	remaining := *original.Rule.Count - CountBefore(original, split)
	tail := Event{
		StartAt: split,
		EndAt:   split.Add(time.Hour),
		Rule:    &Rule{Freq: FreqDaily, Count: intPtr(remaining)},
	}

	total := len(Expand(head, original.StartAt, window)) + len(Expand(tail, original.StartAt, window))
	if total != 10 {
		t.Fatalf("split halves produce %d occurrences, want 10", total)
	}
}
