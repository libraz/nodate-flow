package calendaroccurrence

import (
	"database/sql"
	"encoding/json"
	"time"

	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/generated/calendar"
	apierrors "github.com/libraz/nodate-flow/apps/flow-api/internal/errors"
	"github.com/libraz/nodate-flow/packages/go-shared/recurrence"
)

// SeriesState is the part of a master row that decides which instants its
// occurrences fall on.
type SeriesState struct {
	StartAt    sql.NullTime
	EndAt      sql.NullTime
	Timezone   string
	AllDay     bool
	Rule       json.RawMessage
	End        sql.NullTime
	Exceptions json.RawMessage
}

// SeriesStateOf reads the series columns off a master row.
func SeriesStateOf(row calendar.FindCalendarEventByPublicIdRow) SeriesState {
	return SeriesState{
		StartAt:    row.StartAt,
		EndAt:      row.EndAt,
		Timezone:   row.Timezone,
		AllDay:     row.AllDay,
		Rule:       row.RecurrenceRule,
		End:        row.RecurrenceEnd,
		Exceptions: row.RecurrenceExceptions,
	}
}

// lockedState is a master's series columns as the series lock read them,
// over the window and zone of the master row.
func lockedState(master calendar.FindCalendarEventByPublicIdRow, locked calendar.LockCalendarEventSeriesRow) SeriesState {
	s := SeriesStateOf(master)
	s.Rule = locked.RecurrenceRule
	s.End = locked.RecurrenceEnd
	s.Exceptions = locked.RecurrenceExceptions
	return s
}

// ValidateOccurrence is the one answer to whether at names an occurrence
// of the series: an instant the rule produces within its bounds that no
// exception has cancelled. A nil spec means it does.
//
// An occurrence an override stands in for is still addressed by its
// original start and passes; the instant the override was moved to does
// not.
func ValidateOccurrence(state SeriesState, at time.Time) *apierrors.Spec {
	evt, ok := state.event()
	if !ok {
		return apierrors.CalendarEventNotRecurring
	}
	if !recurrence.Produces(evt, at) {
		return apierrors.CalendarEventOccurrenceStartNotInSeries
	}
	for _, raw := range recurrence.ParseExceptions(state.Exceptions) {
		if recurrence.EntryNames(raw, state.zone(), at) {
			return apierrors.CalendarEventOccurrenceStartNotInSeries
		}
	}
	return nil
}

// occurrenceRefusal is [ValidateOccurrence] as the refusal a transform
// answers with, naming the member it is about.
func occurrenceRefusal(state SeriesState, at time.Time) *apierrors.APIError {
	spec := ValidateOccurrence(state, at)
	if spec == nil {
		return nil
	}
	field := "occurrenceStart"
	if spec == apierrors.CalendarEventNotRecurring {
		field = "scope"
	}
	return apierrors.New(spec).WithDetail("field", field)
}

// zone is the timezone the state's occurrences and stored entries are
// read in.
func (s SeriesState) zone() string {
	return recurrence.ZoneName(s.Timezone, s.AllDay)
}

// event projects the state onto what the expander reads, without the
// exception list. False means the state produces no occurrences to map:
// no rule, a rule that does not parse, or no anchor.
func (s SeriesState) event() (recurrence.Event, bool) {
	if !s.StartAt.Valid || !HasRule(s.Rule) {
		return recurrence.Event{}, false
	}
	rule, err := recurrence.ParseRule(s.Rule)
	if err != nil || rule == nil || !rule.Freq.Valid() {
		return recurrence.Event{}, false
	}
	evt := recurrence.Event{
		StartAt:  s.StartAt.Time,
		EndAt:    s.StartAt.Time,
		Timezone: s.Timezone,
		AllDay:   s.AllDay,
		Rule:     rule,
	}
	if s.EndAt.Valid {
		evt.EndAt = s.EndAt.Time
	}
	if s.End.Valid {
		end := s.End.Time
		evt.RecurrenceEnd = &end
	}
	return evt, true
}
