// Disclosure suite for the task side of task_event_links.
//
// GET /tasks/{id}/linked-events renders each link with the event's title,
// its times and the name of the calendar it lives on. Reaching the task
// is what the route checks, and a workspace holds calendars whose member
// lists do not coincide, so without a membership rule of its own this
// list hands every reader of a task the contents of calendars they cannot
// open.
//
// There is no existence oracle here — the caller named a task they may
// already read — so the answer is not a refusal. A link whose calendar
// the caller cannot reach is returned with its own fields intact and the
// event's withheld, marked eventHidden so a withheld title cannot be read
// as an event that has none.
package e2e

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// linkedEventsList is the decoded task-side list response, carrying the
// fields that belong to the calendar as well as the link's own.
type linkedEventsList struct {
	Total int64 `json:"total"`
	Links []struct {
		ID           string `json:"id"`
		Relation     string `json:"relation"`
		EventID      string `json:"eventId"`
		EventTitle   string `json:"eventTitle"`
		EventStartAt *int64 `json:"eventStartAt"`
		EventEndAt   *int64 `json:"eventEndAt"`
		CalendarID   string `json:"calendarId"`
		CalendarName string `json:"calendarName"`
		EventHidden  bool   `json:"eventHidden"`
	} `json:"links"`
}

// byEvent indexes the decoded links by the event id they point at, which
// survives the redaction and is therefore what a caller keys on.
func (l linkedEventsList) byEvent(eventID string) (int, bool) {
	for i, link := range l.Links {
		if link.EventID == eventID {
			return i, true
		}
	}
	return 0, false
}

// TestLinkedEventsWithholdsUnreachableCalendars puts one task on two
// calendars and gives the reader a grant on exactly one of them.
//
// Both links are the task's, so both are answered and total counts both:
// a list that dropped the unreachable one would tell the reader their
// task has fewer links than it has, and leave total disagreeing with the
// rows beneath it. What the reachable half proves is that the withheld
// half is withheld rather than the route having stopped rendering events
// at all.
func TestLinkedEventsWithholdsUnreachableCalendars(t *testing.T) {
	bootstrap(t)
	t.Parallel()

	const reachableEventTitle = "Pricing committee"
	const hiddenEventTitle = "Layoff sequencing"
	const hiddenCalendarName = "Exec Only Cal"

	host := newTenant(t)
	reader := seedProjectRoleMember(t, host, "editor")

	reachableCal := createCalendarMut(t, host, "Team Planning Cal")
	hiddenCal := createCalendarMut(t, host, hiddenCalendarName)
	reachableEvt := createEventMut(t, host, reachableCal, reachableEventTitle)
	hiddenEvt := createEventMut(t, host, hiddenCal, hiddenEventTitle)

	taskID := createTaskWithVisibility(t, host, "Assemble the Q3 numbers", "public")
	createLinkVia(t, host, taskID, reachableEvt)
	createLinkVia(t, host, taskID, hiddenEvt)

	// One grant, on one of the two calendars. The reader's standing on
	// the task is untouched by this and stays sufficient throughout.
	addCalendarMemberWithRole(t, host, reachableCal, reader.Email, "viewer")

	// The structural assertions and the raw-text ones below run against
	// one response, so a body that carried a title the decoded view did
	// not surface cannot slip between two requests.
	listURL := testServerURL + "/tasks/" + taskID + "/linked-events"
	status, body := doJSONStatus(t, http.MethodGet, listURL, reader.AccessToken, nil)
	require.Equal(t, http.StatusOK, status, "linked-events must render; body=%s", string(body))

	var seen linkedEventsList
	require.NoError(t, json.Unmarshal(body, &seen), "decode linked-events body=%s", string(body))
	require.Len(t, seen.Links, 2, "both of the task's links must be answered")
	assert.Equal(t, int64(2), seen.Total,
		"total must count every link on the task, including the withheld one")

	reachableIdx, ok := seen.byEvent(reachableEvt)
	require.True(t, ok, "the reachable calendar's link must be in the list")
	reachable := seen.Links[reachableIdx]
	assert.False(t, reachable.EventHidden, "a calendar the reader may open is not hidden")
	assert.Equal(t, reachableEventTitle, reachable.EventTitle)
	assert.Equal(t, "Team Planning Cal", reachable.CalendarName)
	assert.NotNil(t, reachable.EventStartAt, "a visible event must carry its start time")

	hiddenIdx, ok := seen.byEvent(hiddenEvt)
	require.True(t, ok, "the unreachable calendar's link must still be listed")
	hidden := seen.Links[hiddenIdx]
	assert.True(t, hidden.EventHidden,
		"a link the reader cannot see through must say so rather than look empty")
	assert.Empty(t, hidden.EventTitle, "the withheld event's title must not be rendered")
	assert.Empty(t, hidden.CalendarName, "the withheld calendar's name must not be rendered")
	assert.Nil(t, hidden.EventStartAt, "the withheld event's start time must not be rendered")
	assert.Nil(t, hidden.EventEndAt, "the withheld event's end time must not be rendered")

	// The link itself is the task's own data and stays addressable: it
	// keeps its id and its relation, which is what lets the reader detach
	// their task from something they cannot see.
	assert.NotEmpty(t, hidden.ID, "a withheld link keeps its own id")
	assert.Equal(t, "contributes_to", hidden.Relation, "a withheld link keeps its relation")

	// The host holds both calendars, so the withheld strings are
	// demonstrably renderable by this route — which is what makes their
	// absence above a statement about the reader's grant rather than
	// about a route that renders nothing.
	_, hostBody := doJSONStatus(t, http.MethodGet, listURL, host.AccessToken, nil)
	assert.Contains(t, string(hostBody), hiddenEventTitle,
		"the calendar's owner must still see the event's title")
	assert.Contains(t, string(hostBody), hiddenCalendarName,
		"the calendar's owner must still see the calendar's name")

	assert.NotContains(t, string(body), hiddenEventTitle,
		"an event on a calendar the reader cannot open must not reach the wire")
	assert.NotContains(t, string(body), hiddenCalendarName,
		"the name of a calendar the reader cannot open must not reach the wire")
}

// TestLinkedEventsOpenUpOnceTheGrantArrives is the other direction of the
// same rule: the withholding follows the membership, so granting one
// turns the hidden half into an ordinary row.
//
// Without this leg a route that marked every link hidden would satisfy
// the suite above, and the list would be useless to everyone rather than
// closed to the right people.
func TestLinkedEventsOpenUpOnceTheGrantArrives(t *testing.T) {
	bootstrap(t)
	t.Parallel()

	const eventTitle = "Succession shortlist"

	host := newTenant(t)
	reader := seedProjectRoleMember(t, host, "editor")

	calID := createCalendarMut(t, host, "Grant Arrival Cal")
	evtID := createEventMut(t, host, calID, eventTitle)
	taskID := createTaskWithVisibility(t, host, "Prepare the briefing pack", "public")
	createLinkVia(t, host, taskID, evtID)

	listURL := testServerURL + "/tasks/" + taskID + "/linked-events"

	var before linkedEventsList
	doJSON(t, http.MethodGet, listURL, reader.AccessToken, nil, &before)
	require.Len(t, before.Links, 1)
	assert.True(t, before.Links[0].EventHidden, "no grant on the calendar means the event is withheld")
	assert.Empty(t, before.Links[0].EventTitle)

	addCalendarMemberWithRole(t, host, calID, reader.Email, "viewer")

	var after linkedEventsList
	doJSON(t, http.MethodGet, listURL, reader.AccessToken, nil, &after)
	require.Len(t, after.Links, 1)
	assert.False(t, after.Links[0].EventHidden, "a member of the calendar sees its event")
	assert.Equal(t, eventTitle, after.Links[0].EventTitle,
		"the same link renders the event once the grant exists")
	assert.Equal(t, before.Links[0].ID, after.Links[0].ID,
		"the grant changes what the link carries, not which link it is")
}
