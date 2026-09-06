package calendar

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/libraz/nodate-flow/apps/flow-api/tests/helpers"
)

// seriesBounds is the pair of columns a partial delete writes on the
// master: the occurrences it cancelled and the point it stopped the
// series at. Both are read as strings so the assertions do not depend on
// the driver's parseTime setting or on the session timezone.
type seriesBounds struct {
	exceptions    sql.NullString
	recurrenceEnd sql.NullString
}

func readSeriesBounds(t *testing.T, publicID string) seriesBounds {
	t.Helper()
	var b seriesBounds
	err := testDB.QueryRowContext(
		context.Background(),
		`SELECT CAST(recurrence_exceptions AS CHAR),
		        DATE_FORMAT(recurrence_end, '%Y-%m-%dT%H:%i:%s.%fZ')
		   FROM calendar_events
		  WHERE public_id = UUID_TO_BIN(?, 0)
		  LIMIT 1`,
		publicID,
	).Scan(&b.exceptions, &b.recurrenceEnd)
	require.NoError(t, err, "read series bounds for %s", publicID)
	return b
}

// exceptionInstants parses the stored exception list into instants, so a
// test asserts on the occurrence an entry names rather than on the
// spelling it happens to be written in.
func exceptionInstants(t *testing.T, stored sql.NullString) []time.Time {
	t.Helper()
	if !stored.Valid || stored.String == "" || stored.String == "null" {
		return nil
	}
	var list []string
	require.NoError(t, json.Unmarshal([]byte(stored.String), &list), "parse recurrence_exceptions")
	out := make([]time.Time, 0, len(list))
	for _, raw := range list {
		at, err := time.Parse(time.RFC3339, raw)
		require.NoError(t, err, "parse exception entry %q", raw)
		out = append(out, at.UTC())
	}
	return out
}

// seedOccurrenceOverride writes the row that replaces one occurrence of a
// series, and returns its public ID.
//
// Direct SQL rather than the patch endpoint: what these tests probe is
// what the delete does to an override, and the insert still passes
// through the projection guard trigger that governs the shape of such a
// row.
func seedOccurrenceOverride(
	t *testing.T,
	tt *helpers.CalendarTestTenant,
	calID, masterID string,
	originalStart time.Time,
) string {
	t.Helper()
	calInternalID := helpers.ResolveCalendarInternalID(t, testDB, calID)
	master := readEventRow(t, masterID)

	u := uuid.Must(uuid.NewV7())
	moved := originalStart.Add(2 * time.Hour)
	_, err := testDB.ExecContext(context.Background(), `
		INSERT INTO calendar_events
		  (public_id, workspace_id, calendar_id,
		   recurrence_parent_id, recurrence_original_start,
		   kind, title, start_at, end_at, timezone,
		   owner_user_id, created_by_user_id)
		VALUES (?, ?, ?, ?, ?, 'event', ?, ?, ?, 'UTC', ?, ?)`,
		u[:], tt.WorkspaceID, calInternalID,
		master.id, originalStart,
		fmt.Sprintf("Moved occurrence %s", originalStart.Format(time.RFC3339)),
		moved, moved.Add(30*time.Minute),
		tt.UserInternalID, tt.UserInternalID)
	require.NoError(t, err, "seed occurrence override")
	return u.String()
}

// deleteEventScoped calls DELETE with the scope query parameters and
// returns the status code.
func deleteEventScoped(
	t *testing.T,
	tt *helpers.CalendarTestTenant,
	calID, evtID, query string,
) int {
	t.Helper()
	url := tt.WsPath("calendars", calID, "events", evtID)
	if query != "" {
		url += "?" + query
	}
	status, _ := helpers.DoJSONStatus(t, http.MethodDelete, url, tt.AccessToken, nil)
	return status
}

// TestDeleteEventSeries_DisablesOverrides covers the delete an omitted
// scope performs. The foreign key on recurrence_parent_id cascades on
// DELETE, but a delete here clears `enabled` and never removes a row, so
// nothing reaches the children unless the writer does. An override left
// enabled carries no rule of its own and is selected by the
// non-recurring range queries, which is how a cancelled series comes
// back as loose events.
func TestDeleteEventSeries_DisablesOverrides(t *testing.T) {
	bootstrap(t)
	t.Parallel()

	tt := newTenant(t)
	calID := createCalendar(t, tt)

	first := time.Date(2028, 2, 1, 10, 0, 0, 0, time.UTC)
	masterID := weeklySeries(t, tt, calID, first)
	second := first.AddDate(0, 0, 7)
	third := first.AddDate(0, 0, 14)

	overrideSecond := seedOccurrenceOverride(t, tt, calID, masterID, second)
	overrideThird := seedOccurrenceOverride(t, tt, calID, masterID, third)

	require.Equal(t, http.StatusOK, deleteEventScoped(t, tt, calID, masterID, ""))

	assert.False(t, readEventRow(t, masterID).enabled, "the master must be soft-deleted")
	assert.False(t, readEventRow(t, overrideSecond).enabled,
		"an override of a deleted series must go with it")
	assert.False(t, readEventRow(t, overrideThird).enabled,
		"an override of a deleted series must go with it")
}

// TestDeleteEventOccurrence_CancelsAndDisablesItsOverride covers the two
// halves of "this occurrence is gone": the master stops producing it,
// and the row standing in for it stops being drawn. Either alone leaves
// the occurrence on the calendar.
func TestDeleteEventOccurrence_CancelsAndDisablesItsOverride(t *testing.T) {
	bootstrap(t)
	t.Parallel()

	tt := newTenant(t)
	calID := createCalendar(t, tt)

	first := time.Date(2028, 3, 6, 9, 0, 0, 0, time.UTC)
	masterID := weeklySeries(t, tt, calID, first)
	second := first.AddDate(0, 0, 7)
	third := first.AddDate(0, 0, 14)

	overrideSecond := seedOccurrenceOverride(t, tt, calID, masterID, second)
	overrideThird := seedOccurrenceOverride(t, tt, calID, masterID, third)

	query := fmt.Sprintf("scope=occurrence&occurrenceStart=%d", second.Unix())
	require.Equal(t, http.StatusOK, deleteEventScoped(t, tt, calID, masterID, query))

	master := readEventRow(t, masterID)
	assert.True(t, master.enabled, "cancelling one occurrence must leave the series standing")

	bounds := readSeriesBounds(t, masterID)
	assert.Contains(t, exceptionInstants(t, bounds.exceptions), second,
		"the cancelled occurrence must be recorded as an exception")

	assert.False(t, readEventRow(t, overrideSecond).enabled,
		"the override for the cancelled occurrence must go with it")
	assert.True(t, readEventRow(t, overrideThird).enabled,
		"an override for another occurrence must be left alone")
}

// TestDeleteEventFollowing_TruncatesAndDisablesLaterOverrides covers the
// split. The master keeps what it produced before the split point and
// stops there; the overrides at or after it describe occurrences it no
// longer produces, and an override before it still describes one it
// does.
func TestDeleteEventFollowing_TruncatesAndDisablesLaterOverrides(t *testing.T) {
	bootstrap(t)
	t.Parallel()

	tt := newTenant(t)
	calID := createCalendar(t, tt)

	first := time.Date(2028, 4, 3, 14, 0, 0, 0, time.UTC)
	masterID := weeklySeries(t, tt, calID, first)
	second := first.AddDate(0, 0, 7)
	third := first.AddDate(0, 0, 14)
	fourth := first.AddDate(0, 0, 21)

	overrideSecond := seedOccurrenceOverride(t, tt, calID, masterID, second)
	overrideThird := seedOccurrenceOverride(t, tt, calID, masterID, third)
	overrideFourth := seedOccurrenceOverride(t, tt, calID, masterID, fourth)

	query := fmt.Sprintf("scope=thisAndFollowing&occurrenceStart=%d", third.Unix())
	require.Equal(t, http.StatusOK, deleteEventScoped(t, tt, calID, masterID, query))

	master := readEventRow(t, masterID)
	assert.True(t, master.enabled, "a split must leave the earlier occurrences standing")

	bounds := readSeriesBounds(t, masterID)
	require.True(t, bounds.recurrenceEnd.Valid, "the master must carry a recurrence end after a split")
	assert.Equal(t,
		third.Add(-time.Millisecond).Format("2006-01-02T15:04:05.000000Z"),
		bounds.recurrenceEnd.String,
		"the series must stop just before the split occurrence")

	assert.True(t, readEventRow(t, overrideSecond).enabled,
		"an override before the split still names an occurrence the master produces")
	assert.False(t, readEventRow(t, overrideThird).enabled,
		"an override at the split names an occurrence the master no longer produces")
	assert.False(t, readEventRow(t, overrideFourth).enabled,
		"an override after the split names an occurrence the master no longer produces")
}

// TestDeleteEventOccurrence_RefusesWithoutOccurrenceStart covers the
// request that names a scope but no occurrence, which singles out
// nothing.
func TestDeleteEventOccurrence_RefusesWithoutOccurrenceStart(t *testing.T) {
	bootstrap(t)
	t.Parallel()

	tt := newTenant(t)
	calID := createCalendar(t, tt)

	masterID := weeklySeries(t, tt, calID, time.Date(2028, 5, 8, 11, 0, 0, 0, time.UTC))

	assert.Equal(t, http.StatusUnprocessableEntity,
		deleteEventScoped(t, tt, calID, masterID, "scope=occurrence"))
	assert.Equal(t, http.StatusUnprocessableEntity,
		deleteEventScoped(t, tt, calID, masterID, "scope=thisAndFollowing"))
	assert.True(t, readEventRow(t, masterID).enabled, "a refused delete must change nothing")
}

// TestDeleteEventOccurrence_RefusesOnNonRecurringEvent covers a scope
// naming an occurrence of a row that produces none.
func TestDeleteEventOccurrence_RefusesOnNonRecurringEvent(t *testing.T) {
	bootstrap(t)
	t.Parallel()

	tt := newTenant(t)
	calID := createCalendar(t, tt)

	start := time.Date(2028, 6, 5, 10, 0, 0, 0, time.UTC)
	evtID := createEventForSoftDelete(t, tt, calID, "One-off", start, start.Add(time.Hour))

	query := fmt.Sprintf("scope=occurrence&occurrenceStart=%d", start.Unix())
	assert.Equal(t, http.StatusUnprocessableEntity,
		deleteEventScoped(t, tt, calID, evtID, query))
	assert.True(t, readEventRow(t, evtID).enabled, "a refused delete must change nothing")
}

// TestDeleteEventOccurrence_RefusesOnOverrideRow covers a scope aimed at
// a row that is already an override. It produces no occurrences and has
// no series to truncate, and the projection guard trigger cannot say so:
// it inspects only the row being written and never follows the parent
// link.
func TestDeleteEventOccurrence_RefusesOnOverrideRow(t *testing.T) {
	bootstrap(t)
	t.Parallel()

	tt := newTenant(t)
	calID := createCalendar(t, tt)

	first := time.Date(2028, 7, 3, 8, 0, 0, 0, time.UTC)
	masterID := weeklySeries(t, tt, calID, first)
	second := first.AddDate(0, 0, 7)
	overrideID := seedOccurrenceOverride(t, tt, calID, masterID, second)

	query := fmt.Sprintf("scope=occurrence&occurrenceStart=%d", second.Unix())
	assert.Equal(t, http.StatusUnprocessableEntity,
		deleteEventScoped(t, tt, calID, overrideID, query))
	assert.True(t, readEventRow(t, overrideID).enabled, "a refused delete must change nothing")

	// The series scope is how an override row is deleted.
	require.Equal(t, http.StatusOK, deleteEventScoped(t, tt, calID, overrideID, ""))
	assert.False(t, readEventRow(t, overrideID).enabled)
}
