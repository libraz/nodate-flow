package calendaroccurrence

import (
	"database/sql"
	"encoding/json"
	"testing"
	"time"

	apierrors "github.com/libraz/nodate-flow/apps/flow-api/internal/errors"
)

// An occurrence is named by an instant the rule produces within its
// bounds that no exception cancelled; anything else is refused before a
// scoped write reaches the store.
func TestValidateOccurrenceAcceptsOnlyWhatTheSeriesProduces(t *testing.T) {
	t.Parallel()
	end := seriesAnchor.AddDate(0, 0, 10)
	state := SeriesState{
		StartAt:    sql.NullTime{Time: seriesAnchor, Valid: true},
		EndAt:      sql.NullTime{Time: seriesAnchor.Add(time.Hour), Valid: true},
		Timezone:   "UTC",
		Rule:       json.RawMessage(`{"freq":"daily"}`),
		End:        sql.NullTime{Time: end, Valid: true},
		Exceptions: json.RawMessage(`["` + seriesAnchor.AddDate(0, 0, 2).Format(time.RFC3339) + `"]`),
	}
	cases := []struct {
		name string
		at   time.Time
		want *apierrors.Spec
	}{
		{"an occurrence", seriesAnchor.AddDate(0, 0, 1), nil},
		{"the anchor", seriesAnchor, nil},
		{"off the rule", seriesAnchor.Add(30 * time.Minute), apierrors.CalendarEventOccurrenceStartNotInSeries},
		{"before the start", seriesAnchor.AddDate(0, 0, -1), apierrors.CalendarEventOccurrenceStartNotInSeries},
		{"after the recurrence end", end.AddDate(0, 0, 1), apierrors.CalendarEventOccurrenceStartNotInSeries},
		{"cancelled", seriesAnchor.AddDate(0, 0, 2), apierrors.CalendarEventOccurrenceStartNotInSeries},
	}
	for _, tc := range cases {
		if got := ValidateOccurrence(state, tc.at); got != tc.want {
			t.Errorf("%s: ValidateOccurrence(%s) = %v, want %v", tc.name, tc.at, got, tc.want)
		}
	}

	if got := ValidateOccurrence(SeriesState{StartAt: state.StartAt, Rule: json.RawMessage(`null`)}, seriesAnchor); got != apierrors.CalendarEventNotRecurring {
		t.Errorf("a row without a rule = %v, want not recurring", got)
	}
}

// An all-day series in a DST zone is addressed by UTC midnights, and a
// bare-date exception cancels that UTC day.
func TestValidateOccurrenceReadsAllDaySeriesInUTC(t *testing.T) {
	t.Parallel()
	start := time.Date(2027, 3, 1, 0, 0, 0, 0, time.UTC)
	state := SeriesState{
		StartAt:    sql.NullTime{Time: start, Valid: true},
		EndAt:      sql.NullTime{Time: start.AddDate(0, 0, 1), Valid: true},
		Timezone:   "America/New_York",
		AllDay:     true,
		Rule:       json.RawMessage(`{"freq":"weekly"}`),
		Exceptions: json.RawMessage(`["2027-03-22"]`),
	}
	if got := ValidateOccurrence(state, time.Date(2027, 3, 15, 0, 0, 0, 0, time.UTC)); got != nil {
		t.Errorf("UTC midnight after DST refused: %v", got)
	}
	if got := ValidateOccurrence(state, time.Date(2027, 3, 14, 23, 0, 0, 0, time.UTC)); got == nil {
		t.Error("the drifted local-evening instant was accepted")
	}
	if got := ValidateOccurrence(state, time.Date(2027, 3, 22, 0, 0, 0, 0, time.UTC)); got == nil {
		t.Error("the occurrence a bare-date exception cancels was accepted")
	}
}
