package calendaroccurrence

import (
	"database/sql"
	"encoding/json"
	"testing"
	"time"

	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/generated/calendar"
	apierrors "github.com/libraz/nodate-flow/apps/flow-api/internal/errors"
)

// base is an ordinary timed occurrence for the merge tests to fold over.
func base() Fields {
	start := time.Date(2030, 6, 3, 10, 0, 0, 0, time.UTC)
	return Fields{
		Kind:       calendar.CalendarEventsKindEvent,
		Title:      "Stand-up",
		StartAt:    sql.NullTime{Time: start, Valid: true},
		EndAt:      sql.NullTime{Time: start.Add(30 * time.Minute), Valid: true},
		Timezone:   "Asia/Tokyo",
		Location:   sql.NullString{String: "Room 1", Valid: true},
		Memo:       sql.NullString{String: "bring the board", Valid: true},
		BlockLabel: sql.NullString{String: "focus", Valid: true},
	}
}

func TestParseScopeReadsAnOmittedScopeAsTheWholeSeries(t *testing.T) {
	t.Parallel()

	for _, raw := range []string{"", "series"} {
		scope, ok := ParseScope(raw)
		if !ok || scope != ScopeSeries {
			t.Errorf("ParseScope(%q) = %q, %v; a caller that names no scope means the whole series",
				raw, scope, ok)
		}
	}
}

func TestParseScopeRefusesAValueTheClosedSetDoesNotName(t *testing.T) {
	t.Parallel()

	// The refusal matters more than the value: a scope that fell through
	// to the default would rewrite every occurrence of the series.
	if scope, ok := ParseScope("thisOne"); ok {
		t.Errorf("ParseScope(%q) accepted and resolved to %q; an unknown scope must not reach the series path",
			"thisOne", scope)
	}
}

func TestScopeRefusalNamesWhyAnOccurrenceCannotBeSingledOut(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name            string
		scope           Scope
		occurrenceStart int64
		hasRule         bool
		isOverride      bool
		want            *apierrors.Spec
		wantField       string
	}{
		{
			name:  "a series scope asks nothing of the row",
			scope: ScopeSeries,
		},
		{
			name:      "an occurrence scope with no occurrence names nothing",
			scope:     ScopeOccurrence,
			hasRule:   true,
			want:      apierrors.CalendarEventOccurrenceStartRequired,
			wantField: "occurrenceStart",
		},
		{
			name:            "an override already stands in for one occurrence",
			scope:           ScopeOccurrence,
			occurrenceStart: 1_900_000_000,
			hasRule:         true,
			isOverride:      true,
			want:            apierrors.CalendarEventAlreadyOccurrenceOverride,
			wantField:       "scope",
		},
		{
			name:            "a row that repeats not at all produces no occurrence",
			scope:           ScopeThisAndFollowing,
			occurrenceStart: 1_900_000_000,
			want:            apierrors.CalendarEventNotRecurring,
			wantField:       "scope",
		},
		{
			name:            "a recurring master can be split",
			scope:           ScopeThisAndFollowing,
			occurrenceStart: 1_900_000_000,
			hasRule:         true,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			spec, field := ScopeRefusal(tc.scope, tc.occurrenceStart, tc.hasRule, tc.isOverride)
			if spec != tc.want {
				t.Errorf("spec = %v, want %v", spec, tc.want)
			}
			if field != tc.wantField {
				t.Errorf("field = %q, want %q; the member is what a caller corrects", field, tc.wantField)
			}
		})
	}
}

func TestApplyBorrowsTheOtherEndOfTheWindowFromTheOccurrence(t *testing.T) {
	t.Parallel()

	// Moving only the start is the affordance an agent has; the end it
	// keeps has to be the occurrence's own, not the master's.
	newStart := time.Date(2030, 6, 3, 9, 0, 0, 0, time.UTC)
	got, err := Patch{StartAt: &newStart}.Apply(base())
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if !got.StartAt.Time.Equal(newStart) {
		t.Errorf("start = %v, want %v", got.StartAt.Time, newStart)
	}
	if !got.EndAt.Time.Equal(base().EndAt.Time) {
		t.Errorf("end = %v, want the occurrence's own %v", got.EndAt.Time, base().EndAt.Time)
	}
}

func TestApplyRefusesAWindowMovedPastItsOtherEnd(t *testing.T) {
	t.Parallel()

	// Dragging the start beyond the end reaches
	// chk_calendar_events_chronology, which comes back as a write failure
	// naming nothing. The rule answers by name instead.
	past := time.Date(2030, 6, 3, 11, 0, 0, 0, time.UTC)
	_, err := Patch{StartAt: &past}.Apply(base())
	if err == nil {
		t.Fatal("Apply accepted a start after the occurrence's end")
	}
	if err.Spec != apierrors.CalendarEventEndBeforeStart {
		t.Errorf("spec = %v, want %v", err.Spec, apierrors.CalendarEventEndBeforeStart)
	}
}

func TestApplyLeavesAnUntouchedWindowAlone(t *testing.T) {
	t.Parallel()

	// A patch that renames an occurrence says nothing about its window, so
	// nothing about the window is decided — including whether it is
	// ordered, which the row it came from already answers.
	title := "Retro"
	got, err := Patch{Title: &title}.Apply(base())
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if got.Title != title {
		t.Errorf("title = %q, want %q", got.Title, title)
	}
	if !got.StartAt.Time.Equal(base().StartAt.Time) || !got.EndAt.Time.Equal(base().EndAt.Time) {
		t.Errorf("window = %v..%v, want the occurrence's own", got.StartAt.Time, got.EndAt.Time)
	}
}

func TestApplyPinsAnAllDayWindowToUTCMidnight(t *testing.T) {
	t.Parallel()

	// An all-day row is a date. Storing the instant the caller happened to
	// send puts the same day on a different square for a reader in another
	// zone.
	allDay := true
	start := time.Date(2030, 6, 3, 15, 0, 0, 0, time.UTC)
	end := time.Date(2030, 6, 3, 18, 0, 0, 0, time.UTC)
	got, err := Patch{AllDay: &allDay, StartAt: &start, EndAt: &end}.Apply(base())
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	midnight := time.Date(2030, 6, 3, 0, 0, 0, 0, time.UTC)
	if !got.StartAt.Time.Equal(midnight) || !got.EndAt.Time.Equal(midnight) {
		t.Errorf("window = %v..%v, want both pinned to %v", got.StartAt.Time, got.EndAt.Time, midnight)
	}
}

func TestApplyRefusesAnInvertedAllDayPairRatherThanCollapsingIt(t *testing.T) {
	t.Parallel()

	// Both ends fall on days that truncate to different midnights, so a
	// check that ran after the pinning would still catch this one — but the
	// answer has to say the window is the wrong way round rather than
	// silently making it one day.
	allDay := true
	start := time.Date(2030, 6, 5, 9, 0, 0, 0, time.UTC)
	end := time.Date(2030, 6, 3, 9, 0, 0, 0, time.UTC)
	_, err := Patch{AllDay: &allDay, StartAt: &start, EndAt: &end}.Apply(base())
	if err == nil {
		t.Fatal("Apply accepted an all-day pair with the days the wrong way round")
	}
	if err.Spec != apierrors.CalendarEventEndBeforeStart {
		t.Errorf("spec = %v, want %v", err.Spec, apierrors.CalendarEventEndBeforeStart)
	}
}

func TestApplyLetsAClearWinOverAValueForTheSameMember(t *testing.T) {
	t.Parallel()

	// Sending both is contradictory, and the destructive reading is the
	// one that cannot silently leave a value the caller asked to be rid of.
	sent := "Room 2"
	got, err := Patch{Location: &sent, Clear: Clears{Location: true}}.Apply(base())
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if got.Location.Valid {
		t.Errorf("location = %q, want cleared", got.Location.String)
	}
}

func TestApplyKeepsMembersTheRequestDidNotMention(t *testing.T) {
	t.Parallel()

	// An override is not a delta: a column left unset would read as the
	// absence of a value rather than as the occurrence's own.
	got, err := Patch{}.Apply(base())
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if got.Memo != base().Memo || got.BlockLabel != base().BlockLabel || got.Timezone != base().Timezone {
		t.Errorf("unmentioned members changed: %+v", got)
	}
}

func TestTruncationPointKeepsAMasterThatAlreadyStopsEarlier(t *testing.T) {
	t.Parallel()

	split := time.Date(2030, 6, 10, 9, 0, 0, 0, time.UTC)
	earlier := time.Date(2030, 6, 1, 0, 0, 0, 0, time.UTC)
	master := calendar.FindCalendarEventByPublicIdRow{
		RecurrenceEnd: sql.NullTime{Time: earlier, Valid: true},
	}
	if got := TruncationPoint(master, split); !got.Equal(earlier) {
		t.Errorf("truncation = %v, want the master's own %v; a split must not revive a finished series",
			got, earlier)
	}
}

func TestTruncationPointStopsJustBeforeTheSplit(t *testing.T) {
	t.Parallel()

	split := time.Date(2030, 6, 10, 9, 0, 0, 0, time.UTC)
	got := TruncationPoint(calendar.FindCalendarEventByPublicIdRow{}, split)
	if !got.Before(split) {
		t.Errorf("truncation = %v, want a bound before the split at %v", got, split)
	}
	if split.Sub(got) != time.Millisecond {
		t.Errorf("truncation = %v, want one millisecond before %v so the split's own occurrence is the first one dropped",
			got, split)
	}
}

func TestAppendExceptionDoesNotRepeatAnInstantAlreadyCancelled(t *testing.T) {
	t.Parallel()

	start := time.Date(2030, 6, 10, 9, 0, 0, 0, time.UTC)
	first, changed, err := AppendException(nil, start)
	if err != nil || !changed {
		t.Fatalf("first append: changed=%v err=%v", changed, err)
	}
	// The same instant written in another zone is the same occurrence.
	tokyo := start.In(time.FixedZone("JST", 9*3600))
	if _, changed, err := AppendException(first, tokyo); err != nil || changed {
		t.Errorf("second append: changed=%v err=%v; the list already cancels that occurrence", changed, err)
	}
}

func TestAppendExceptionRefusesAListItCannotRead(t *testing.T) {
	t.Parallel()

	// Starting over would drop every occurrence the series had already
	// cancelled, and they would all come back.
	if _, _, err := AppendException(json.RawMessage(`{"broken":true}`), time.Now()); err == nil {
		t.Fatal("AppendException accepted a stored list it could not parse")
	}
}

func TestHasRuleReadsBothSpellingsOfNoRule(t *testing.T) {
	t.Parallel()

	for _, raw := range []json.RawMessage{nil, {}, json.RawMessage("null")} {
		if HasRule(raw) {
			t.Errorf("HasRule(%q) = true; a row with no rule produces no occurrences to single out", raw)
		}
	}
	if !HasRule(json.RawMessage(`{"freq":"WEEKLY"}`)) {
		t.Error("HasRule reported a stored rule as absent")
	}
}
