package mcp

import (
	"database/sql"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/libraz/nodate-flow/apps/flow-api/internal/calendaroccurrence"
	apierrors "github.com/libraz/nodate-flow/apps/flow-api/internal/errors"
)

// The occurrence machinery is shared with the REST calendar routes, so
// what these tools contribute to it is small: which tool argument stands
// for which column, that either end of the window may arrive as unix
// seconds or as an all-day date, and what an omitted scope means. Those
// are the parts a shared package cannot pin, and each of them is a
// silent failure — a scope read wrong rewrites a whole series while
// answering success, and a column no argument names would be emptied
// rather than left alone.

// mergeBase is an occurrence the merge falls back to, carrying values in
// every column no tool argument can name.
func mergeBase(allDay bool, start, end time.Time) calendaroccurrence.Fields {
	return calendaroccurrence.Fields{
		Title:              "Daily stand-up",
		AllDay:             allDay,
		StartAt:            sql.NullTime{Time: start, Valid: true},
		EndAt:              sql.NullTime{Time: end, Valid: true},
		Timezone:           "Asia/Tokyo",
		Location:           sql.NullString{String: "Room 3", Valid: true},
		Memo:               sql.NullString{String: "bring the board", Valid: true},
		URL:                sql.NullString{String: "https://example.test/room", Valid: true},
		BlockLabel:         sql.NullString{String: "focus", Valid: true},
		NotificationOffset: sql.NullInt32{Int32: 15, Valid: true},
	}
}

func unixPtr(t time.Time) *int64 {
	secs := t.Unix()
	return &secs
}

func strPtr(s string) *string { return &s }

// TestDecodeOccurrenceScopeResolvesAnOmittedScopeToTheSeries pins the
// default an absent argument falls to.
//
// It is the one value with a consequence in both directions. Read as the
// series, a call that says nothing about occurrences reaches all of them,
// which is what these tools always did and what every caller that has
// never sent the argument relies on. Read as anything else, the same call
// would be refused for naming no occurrence, and an unrecognised value
// falling through to the series path would rewrite a series nobody asked
// to rewrite.
func TestDecodeOccurrenceScopeResolvesAnOmittedScopeToTheSeries(t *testing.T) {
	t.Parallel()

	accepted := map[string]occurrenceScope{
		"":                 scopeSeries,
		"series":           scopeSeries,
		"occurrence":       scopeOccurrence,
		"thisAndFollowing": scopeThisAndFollowing,
	}
	for raw, want := range accepted {
		got, err := decodeOccurrenceScope(raw)
		require.NoErrorf(t, err, "scope %q has to be accepted", raw)
		require.Equalf(t, want, got, "scope %q resolved to the wrong set of occurrences", raw)
	}

	for _, raw := range []string{"everything", "SERIES", "this_and_following", " series"} {
		_, err := decodeOccurrenceScope(raw)
		requireErrorSpec(t, err, apierrors.McpToolArgumentsInvalid)
	}

	// What the tools publish and what they accept are the same set, so a
	// caller reading the schema cannot be refused for using it.
	for _, raw := range occurrenceScopes {
		_, err := decodeOccurrenceScope(raw)
		require.NoErrorf(t, err, "the schema advertises %q but the tool refuses it", raw)
	}
}

// TestMergeOccurrenceFieldsFoldsToolArgumentsOntoTheOccurrence pins what
// an update call does to the occurrence it falls back to.
func TestMergeOccurrenceFieldsFoldsToolArgumentsOntoTheOccurrence(t *testing.T) {
	t.Parallel()

	start := time.Date(2030, 3, 4, 9, 0, 0, 0, time.UTC)
	end := start.Add(time.Hour)

	t.Run("one end sent on its own borrows the other from the occurrence", func(t *testing.T) {
		t.Parallel()
		// The base for a single occurrence is that occurrence, so
		// borrowing never moves the window somewhere the call did not
		// name.
		moved := end.Add(2 * time.Hour)
		fields, err := mergeOccurrenceFields(mergeBase(false, start, end), &updateCalendarEventArgs{
			EndAt: unixPtr(moved),
		})
		require.NoError(t, err)
		require.Equal(t, start, fields.StartAt.Time)
		require.Equal(t, moved, fields.EndAt.Time)
	})

	t.Run("an inverted window is refused by name", func(t *testing.T) {
		t.Parallel()
		// Left to the database this reaches chk_calendar_events_chronology
		// and comes back as an execution failure naming nothing.
		_, err := mergeOccurrenceFields(mergeBase(false, start, end), &updateCalendarEventArgs{
			StartAt: unixPtr(end.Add(time.Hour)),
		})
		requireErrorSpec(t, err, apierrors.CalendarEventEndBeforeStart)
	})

	t.Run("an all-day occurrence keeps both ends on a UTC midnight", func(t *testing.T) {
		t.Parallel()
		// No tool takes an allDay argument, so the stored flag decides
		// whether the pair is a date or an instant. Without the pin,
		// moving an all-day occurrence by startAt stores an off-midnight
		// instant and puts it on a different square for readers in
		// another zone.
		day := time.Date(2030, 3, 4, 0, 0, 0, 0, time.UTC)
		fields, err := mergeOccurrenceFields(mergeBase(true, day, day), &updateCalendarEventArgs{
			StartAt: unixPtr(time.Date(2030, 3, 6, 9, 30, 0, 0, time.UTC)),
			EndAt:   unixPtr(time.Date(2030, 3, 6, 18, 0, 0, 0, time.UTC)),
		})
		require.NoError(t, err)
		want := time.Date(2030, 3, 6, 0, 0, 0, 0, time.UTC)
		require.Equal(t, want, fields.StartAt.Time)
		require.Equal(t, want, fields.EndAt.Time)
	})

	t.Run("an all-day date is the other spelling of the same bound", func(t *testing.T) {
		t.Parallel()
		fields, err := mergeOccurrenceFields(mergeBase(false, start, end), &updateCalendarEventArgs{
			StartDate: strPtr("2030-03-06"),
			EndDate:   strPtr("2030-03-07"),
		})
		require.NoError(t, err)
		require.Equal(t, time.Date(2030, 3, 6, 0, 0, 0, 0, time.UTC), fields.StartAt.Time)
		require.Equal(t, time.Date(2030, 3, 7, 0, 0, 0, 0, time.UTC), fields.EndAt.Time)
	})

	t.Run("a date that does not parse names the argument to correct", func(t *testing.T) {
		t.Parallel()
		_, err := mergeOccurrenceFields(mergeBase(false, start, end), &updateCalendarEventArgs{
			EndDate: strPtr("the seventh"),
		})
		requireErrorSpec(t, err, apierrors.McpToolArgumentsInvalid)
	})

	t.Run("columns no tool argument names keep the occurrence's own values", func(t *testing.T) {
		t.Parallel()
		// An override stands in for the occurrence entirely, so a column
		// left unset would read as the absence of a value rather than as
		// the one the occurrence already carries. None of these has an
		// argument, so none of them may move.
		base := mergeBase(false, start, end)
		fields, err := mergeOccurrenceFields(base, &updateCalendarEventArgs{
			Title: strPtr("Stand-up (this week only)"),
		})
		require.NoError(t, err)
		require.Equal(t, "Stand-up (this week only)", fields.Title)
		require.Equal(t, base.Timezone, fields.Timezone)
		require.Equal(t, base.URL, fields.URL)
		require.Equal(t, base.NotificationOffset, fields.NotificationOffset)
		require.Equal(t, base.AllDay, fields.AllDay)
		require.Equal(t, base.Location, fields.Location)
		require.Equal(t, base.Memo, fields.Memo)
		require.Equal(t, base.BlockLabel, fields.BlockLabel)
		require.Equal(t, base.StartAt, fields.StartAt)
		require.Equal(t, base.EndAt, fields.EndAt)
	})
}
