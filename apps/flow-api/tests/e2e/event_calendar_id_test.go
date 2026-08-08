package e2e

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"
)

// calendarEventRowsFor returns, for the given calendar, how many rows in
// the event log carry its internal id and how many carry NULL, counted
// only over the event types the caller lists.
//
// Everything is scoped to one calendar and one workspace on purpose: the
// suite runs in parallel against a shared database, so an assertion on
// instance-wide counts would depend on what other tests happen to be
// doing.
func calendarEventRowsFor(t *testing.T, wsPublicID, calPublicID string, types []string) (withCalendar, withoutCalendar int) {
	t.Helper()
	require.NotNil(t, testDB, "this test needs the shared test database handle")

	inList := "("
	args := []any{wsPublicID, calPublicID}
	for i, ty := range types {
		if i > 0 {
			inList += ","
		}
		inList += "?"
		args = append(args, ty)
	}
	inList += ")"

	const prefix = `
		SELECT
		  SUM(e.calendar_id IS NOT NULL AND e.calendar_id = c.id) AS with_cal,
		  SUM(e.calendar_id IS NULL)                              AS without_cal
		FROM events e
		INNER JOIN workspaces w ON w.id = e.workspace_id
		INNER JOIN calendars  c ON c.workspace_id = w.id
		WHERE w.public_id = UUID_TO_BIN(?)
		  AND c.public_id = UUID_TO_BIN(?)
		  AND e.type IN `

	var with, without *int64
	require.NoError(t, testDB.QueryRow(prefix+inList, args...).Scan(&with, &without))
	if with != nil {
		withCalendar = int(*with)
	}
	if without != nil {
		withoutCalendar = int(*without)
	}
	return withCalendar, withoutCalendar
}

// TestCalendarEventsRecordTheirCalendar pins that events emitted from
// inside a calendar carry events.calendar_id.
//
// The column, its index and its foreign key were all in the schema while
// nothing ever wrote it, so `SELECT ... WHERE calendar_id = ?` returned
// nothing for every calendar and a per-calendar activity feed could not
// be built at all. Nothing failed: the writes succeeded, the rows
// existed, and only the one column that says which calendar they belong
// to was NULL.
//
// The assertion is on the calendar this test creates and on the event
// types it provokes, so it stays valid alongside whatever else is
// running in the shared database.
func TestCalendarEventsRecordTheirCalendar(t *testing.T) {
	bootstrap(t)
	t.Parallel()

	owner := newTenant(t)
	calID := createCalendarMut(t, owner, "Activity feed source")
	evtID := createEventMut(t, owner, calID, "Recorded against its calendar")

	// A second mutation on the same calendar, through a different
	// handler file, so this is not just the create path.
	doJSON(t, http.MethodPatch,
		testServerURL+"/workspaces/"+owner.WorkspacePublicID+"/calendars/"+calID+"/events/"+evtID,
		owner.AccessToken, map[string]any{"title": "Renamed"}, nil)

	// A memo, which is emitted by yet another handler.
	var memo struct {
		ID string `json:"id"`
	}
	doJSON(t, http.MethodPost,
		testServerURL+"/workspaces/"+owner.WorkspacePublicID+"/calendars/"+calID+"/memos",
		owner.AccessToken, map[string]any{"title": "Memo", "body": "note"}, &memo)
	require.NotEmpty(t, memo.ID)

	kinds := []string{
		"calendar.created",
		"calendar.event.created",
		"calendar.event.updated",
		"calendar.memo.created",
	}
	with, without := calendarEventRowsFor(t, owner.WorkspacePublicID, calID, kinds)

	require.Positivef(t, with,
		"no event row for this calendar carries calendar_id; the column, its index "+
			"and its foreign key exist but nothing writes them, so a per-calendar "+
			"activity feed reads an empty log")
	require.Zerof(t, without,
		"%d calendar-originated event rows still left calendar_id NULL; every "+
			"emitter in the calendar handlers has to bind it or the feed is "+
			"silently partial", without)
	require.GreaterOrEqualf(t, with, len(kinds),
		"expected at least one row per provoked event kind (%v), got %d", kinds, with)
}

// TestPublicShareEventsAreNotAttributedToACalendar is the other half of
// the contract. A public share aggregates events drawn from several
// calendars, so its own lifecycle belongs to the workspace; attributing
// those rows to whichever calendar happened to be in scope would put
// them in a feed they do not belong to.
//
// It is asserted rather than left implicit because the parameter that
// carries the calendar id is required, which means "no calendar" is
// something a caller writes down, and a future caller could write down
// the wrong thing just as easily.
func TestPublicShareEventsAreNotAttributedToACalendar(t *testing.T) {
	bootstrap(t)
	t.Parallel()

	owner := newTenant(t)
	calID := createCalendarMut(t, owner, "Share source")

	var share struct {
		ID string `json:"id"`
	}
	doJSON(t, http.MethodPost,
		testServerURL+"/workspaces/"+owner.WorkspacePublicID+"/public-shares",
		owner.AccessToken, map[string]any{
			"title":    "Shared board",
			"timezone": "UTC",
		}, &share)
	require.NotEmpty(t, share.ID)

	with, _ := calendarEventRowsFor(t, owner.WorkspacePublicID, calID,
		[]string{"public_share.created"})
	require.Zerof(t, with,
		"a public_share.created row was attributed to a calendar; the share spans "+
			"several calendars, so pinning it to one puts it in a feed it does not "+
			"belong to")
}
