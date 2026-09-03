// Calendar event visibility across every read surface.
//
// calendar_events.visibility draws two different lines and the product
// needs both. `confidential` hides the row: nobody but the owner may
// know the event exists. `private` hides only what is written on it:
// the time still reads as taken, because a shared calendar whose blocks
// vanish stops being a shared calendar, but the room, the notes and the
// call link belong to the owner and the people invited.
//
// The tests below check each surface separately rather than trusting one
// of them to stand for the rest. That is the shape of the bug they
// replace: the redaction lived in one mapper in the REST handler
// package, so the MCP tools — reading the same rows through the same
// query — had no redaction at all, and no list query filtered anything.
package e2e

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/libraz/nodate-flow/apps/flow-api/tests/helpers"
)

const (
	visRangeStart = "2027-11-01"
	visRangeEnd   = "2027-12-01"
)

// createEventWithVisibility creates an event carrying the free-text
// fields the visibility rules govern, and returns its public id.
func createEventWithVisibility(
	t *testing.T,
	tt *helpers.TestTenant,
	calID, title, visibility string,
	start time.Time,
) string {
	t.Helper()
	var resp struct {
		ID string `json:"id"`
	}
	doJSON(t, http.MethodPost,
		testServerURL+"/workspaces/"+tt.WorkspacePublicID+"/calendars/"+calID+"/events",
		tt.AccessToken, map[string]any{
			"kind":       "event",
			"title":      title,
			"visibility": visibility,
			"location":   title + " room",
			"memo":       title + " memo",
			"url":        "https://example.test/" + strings.ToLower(visibility),
			"startAt":    start.Unix(),
			"endAt":      start.Add(time.Hour).Unix(),
			"timezone":   "UTC",
		}, &resp)
	require.NotEmpty(t, resp.ID)
	return resp.ID
}

// eventReadSurfaces returns the raw body of every authenticated read
// path that lists or renders calendar events for one actor, keyed by a
// name the failure message can use. Raw bodies rather than decoded
// structs, because the assertion is about a string never appearing.
func eventReadSurfaces(t *testing.T, tt *helpers.TestTenant, calID string) map[string]string {
	t.Helper()
	out := map[string]string{}

	for name, url := range map[string]string{
		"per-calendar list": testServerURL + "/workspaces/" + tt.WorkspacePublicID +
			"/calendars/" + calID + "/events?start=" + visRangeStart + "&end=" + visRangeEnd,
		"cross-calendar list": testServerURL + "/workspaces/" + tt.WorkspacePublicID +
			"/calendar-events?start=" + visRangeStart + "&end=" + visRangeEnd,
		"my events feed": testServerURL + "/me/calendar-events?start=" + visRangeStart +
			"&end=" + visRangeEnd,
	} {
		status, body := doJSONStatus(t, http.MethodGet, url, tt.AccessToken, nil)
		require.Equal(t, http.StatusOK, status, "%s must render; body=%s", name, string(body))
		out[name] = string(body)
	}
	return out
}

// mcpListCalendarEvents drives the MCP list_calendar_events tool and
// returns the raw tool payload.
func mcpListCalendarEvents(t *testing.T, tt *helpers.TestTenant, name string) string {
	t.Helper()
	token := mintMCPToken(t, tt.AccessToken, tt.WorkspacePublicID, name, []string{"read:workspace"})
	status, body := mcpCallRaw(t, token, "tools/call", map[string]any{
		"name": "list_calendar_events",
		"arguments": map[string]any{
			"startDate": visRangeStart,
			"endDate":   visRangeEnd,
		},
	})
	require.Equal(t, http.StatusOK, status, "MCP call must succeed; body=%s", string(body))
	return mcpToolResultText(t, body)
}

// TestConfidentialEventHiddenFromCalendarCoMembers is the row-level half
// of the confidential-event rule. A confidential event must not reach a
// co-member on any read
// surface, including MCP, and the detail route must answer as though the
// id did not exist rather than refusing — a 403 would confirm the event
// is there, which is the fact the setting exists to hide.
func TestConfidentialEventHiddenFromCalendarCoMembers(t *testing.T) {
	bootstrap(t)
	t.Parallel()

	owner := newTenant(t)
	member := inviteAndJoinWorkspace(t, owner)
	calID := createCalendarMut(t, owner, "Exec Calendar")
	addCalendarMemberWithRole(t, owner, calID, member.Email, "editor")

	start := time.Date(2027, 11, 3, 10, 0, 0, 0, time.UTC)
	const secretTitle = "Board compensation review" //#nosec G101 -- an event title the test asserts is not disclosed, not a credential
	evtID := createEventWithVisibility(t, owner, calID, secretTitle, "confidential", start)

	// Also create an ordinary event so a surface that returns nothing at
	// all cannot pass by accident.
	const openTitle = "Weekly sync"
	createEventWithVisibility(t, owner, calID, openTitle, "default", start.Add(48*time.Hour))

	for name, body := range eventReadSurfaces(t, member, calID) {
		assert.NotContains(t, body, secretTitle,
			"%s must not carry a confidential event owned by someone else", name)
		assert.Contains(t, body, openTitle,
			"%s must still carry ordinary events, or this test proves nothing", name)
	}

	// MCP reads the same rows through the same query. It has to be
	// checked separately because it maps them with its own code — the
	// REST redaction never applied to it.
	mcpBody := mcpListCalendarEvents(t, member, "vis-member")
	assert.NotContains(t, mcpBody, secretTitle,
		"the MCP calendar tool must not carry a confidential event owned by someone else")
	assert.Contains(t, mcpBody, openTitle,
		"the MCP calendar tool must still carry ordinary events")

	// The detail route answers as an unknown id.
	status, body := doJSONStatus(t, http.MethodGet,
		testServerURL+"/workspaces/"+member.WorkspacePublicID+"/calendars/"+calID+"/events/"+evtID,
		member.AccessToken, nil)
	assert.Equal(t, http.StatusNotFound, status,
		"a confidential event must read as not found to a co-member; body=%s", string(body))
	assert.NotContains(t, string(body), secretTitle)

	// The owner still sees their own event everywhere.
	for name, ownerBody := range eventReadSurfaces(t, owner, calID) {
		assert.Contains(t, ownerBody, secretTitle,
			"%s must still show the owner their own confidential event", name)
	}
	ownerMCP := mcpListCalendarEvents(t, owner, "vis-owner")
	assert.Contains(t, ownerMCP, secretTitle,
		"the MCP calendar tool must show the owner their own confidential event")

	status, body = doJSONStatus(t, http.MethodGet,
		testServerURL+"/workspaces/"+owner.WorkspacePublicID+"/calendars/"+calID+"/events/"+evtID,
		owner.AccessToken, nil)
	require.Equal(t, http.StatusOK, status, "owner detail; body=%s", string(body))
	assert.Contains(t, string(body), secretTitle)
}

// TestPrivateEventIsTimeVisibleAndDetailScoped is the field-level half.
// A private event stays on the calendar as a block a co-member can see
// and plan around, but its room, notes and link are readable only by the
// owner and by the people actually invited — the second of which is what
// an owner-only rule got wrong, withholding the meeting room from the
// meeting's attendees.
func TestPrivateEventIsTimeVisibleAndDetailScoped(t *testing.T) {
	bootstrap(t)
	t.Parallel()

	owner := newTenant(t)
	member := inviteAndJoinWorkspace(t, owner)
	calID := createCalendarMut(t, owner, "Shared Calendar")
	addCalendarMemberWithRole(t, owner, calID, member.Email, "editor")

	start := time.Date(2027, 11, 5, 14, 0, 0, 0, time.UTC)
	const privTitle = "One to one"
	evtID := createEventWithVisibility(t, owner, calID, privTitle, "private", start)
	secretLocation := privTitle + " room"
	secretMemo := privTitle + " memo"

	memberDetailURL := testServerURL + "/workspaces/" + member.WorkspacePublicID +
		"/calendars/" + calID + "/events/" + evtID

	// Before the invitation: the block is visible, its contents are not.
	for name, body := range eventReadSurfaces(t, member, calID) {
		assert.Contains(t, body, privTitle,
			"%s must keep a private event visible as a time block", name)
		assert.NotContains(t, body, secretLocation,
			"%s must not carry a private event's location to a non-attendee", name)
	}

	status, body := doJSONStatus(t, http.MethodGet, memberDetailURL, member.AccessToken, nil)
	require.Equal(t, http.StatusOK, status,
		"a private event's detail must resolve for a co-member; body=%s", string(body))
	var beforeInvite struct {
		Title    string  `json:"title"`
		Location *string `json:"location"`
		Memo     *string `json:"memo"`
		URL      *string `json:"url"`
	}
	require.NoError(t, json.Unmarshal(body, &beforeInvite))
	assert.Equal(t, privTitle, beforeInvite.Title)
	assert.Nil(t, beforeInvite.Location, "location must be withheld from a non-attendee")
	assert.Nil(t, beforeInvite.Memo, "memo must be withheld from a non-attendee")
	assert.Nil(t, beforeInvite.URL, "url must be withheld from a non-attendee")

	// Inviting them is what makes the details theirs to read.
	addAttendeeMut(t, owner, calID, evtID, member.UserPublicID)

	status, body = doJSONStatus(t, http.MethodGet, memberDetailURL, member.AccessToken, nil)
	require.Equal(t, http.StatusOK, status, "attendee detail; body=%s", string(body))
	var afterInvite struct {
		Location *string `json:"location"`
		Memo     *string `json:"memo"`
		URL      *string `json:"url"`
	}
	require.NoError(t, json.Unmarshal(body, &afterInvite))
	require.NotNil(t, afterInvite.Location, "an attendee must be able to read the room")
	assert.Equal(t, secretLocation, *afterInvite.Location)
	require.NotNil(t, afterInvite.Memo, "an attendee must be able to read the notes")
	assert.Equal(t, secretMemo, *afterInvite.Memo)
	assert.NotNil(t, afterInvite.URL, "an attendee must be able to read the call link")

	// The list paths agree with the detail path.
	surfaces := eventReadSurfaces(t, member, calID)
	assert.Contains(t, surfaces["per-calendar list"], secretLocation,
		"the per-calendar list must give an attendee the same room the detail does")

	// The owner reads everything throughout.
	status, body = doJSONStatus(t, http.MethodGet,
		testServerURL+"/workspaces/"+owner.WorkspacePublicID+"/calendars/"+calID+"/events/"+evtID,
		owner.AccessToken, nil)
	require.Equal(t, http.StatusOK, status, "owner detail; body=%s", string(body))
	assert.Contains(t, string(body), secretLocation)
	assert.Contains(t, string(body), secretMemo)
}

// TestDeletedCalendarLeavesPublicSharePage asserts the deleted-calendar
// rule where it matters: the unauthenticated page. Checking that the
// link row is gone would not catch the failure, whose whole shape is a
// row still being served by a query nobody filtered.
func TestDeletedCalendarLeavesPublicSharePage(t *testing.T) {
	bootstrap(t)
	t.Parallel()

	tt := newTenant(t)
	keptCalID := createCalendarMut(t, tt, "Kept Calendar")
	doomedCalID := createCalendarMut(t, tt, "Doomed Calendar")

	const keptTitle = "Survives the deletion"
	const doomedTitle = "Offsite venue walkthrough"
	keptEvtID := createEventMut(t, tt, keptCalID, keptTitle)
	doomedEvtID := createEventMut(t, tt, doomedCalID, doomedTitle)

	var share struct {
		ID    string `json:"id"`
		Token string `json:"token"`
	}
	doJSON(t, http.MethodPost,
		testServerURL+"/workspaces/"+tt.WorkspacePublicID+"/public-shares",
		tt.AccessToken, map[string]any{"title": "Team schedule"}, &share)
	require.NotEmpty(t, share.Token)

	var attach struct {
		Attached int `json:"attached"`
	}
	doJSON(t, http.MethodPost,
		testServerURL+"/workspaces/"+tt.WorkspacePublicID+"/public-shares/"+share.ID+"/events",
		tt.AccessToken, map[string]any{"eventIds": []string{keptEvtID, doomedEvtID}}, &attach)
	require.Equal(t, 2, attach.Attached)

	status, rendered := doJSONStatus(t, http.MethodGet,
		testServerURL+"/share/cal/"+share.Token, "", nil)
	require.Equal(t, http.StatusOK, status, "render; body=%s", string(rendered))
	require.Contains(t, string(rendered), doomedTitle,
		"the event must be published before the calendar is deleted, or the test proves nothing")

	// Delete the calendar the event lives on.
	status, body := doJSONStatus(t, http.MethodDelete,
		testServerURL+"/workspaces/"+tt.WorkspacePublicID+"/calendars/"+doomedCalID,
		tt.AccessToken, nil)
	require.Equal(t, http.StatusOK, status, "delete calendar; body=%s", string(body))

	// The public page is the surface that matters.
	status, rendered = doJSONStatus(t, http.MethodGet,
		testServerURL+"/share/cal/"+share.Token, "", nil)
	require.Equal(t, http.StatusOK, status, "render after delete; body=%s", string(rendered))
	assert.NotContains(t, string(rendered), doomedTitle,
		"an event whose calendar was deleted must stop rendering on the public share page")
	assert.Contains(t, string(rendered), keptTitle,
		"events on surviving calendars must keep rendering")

	// And the link row is withdrawn, so the state the editor shows and
	// the state the page serves agree. Before the fix the editor hid the
	// row while the page kept serving it, which left no way to unpublish.
	var liveLinks int
	require.NoError(t, testDB.QueryRow(
		`SELECT COUNT(*) FROM calendar_public_share_events l
		   JOIN calendar_events e ON e.id = l.event_id
		  WHERE e.public_id = UUID_TO_BIN(?, 0) AND l.enabled = TRUE`,
		doomedEvtID).Scan(&liveLinks))
	assert.Zero(t, liveLinks, "deleting a calendar must withdraw its events from every share")

	var editor struct {
		Events []struct {
			EventID string `json:"eventId"`
		} `json:"events"`
	}
	doJSON(t, http.MethodGet,
		testServerURL+"/workspaces/"+tt.WorkspacePublicID+"/public-shares/"+share.ID,
		tt.AccessToken, nil, &editor)
	ids := map[string]bool{}
	for _, e := range editor.Events {
		ids[e.EventID] = true
	}
	assert.False(t, ids[doomedEvtID], "the editor must not list the withdrawn event")
	assert.True(t, ids[keptEvtID], "the editor must still list the surviving event")
}

// TestPublicShareRenderIgnoresLinksOnDeletedCalendars covers the other
// half, and it needs its own test because the two halves mask
// each other: with the delete path withdrawing the links, the render
// query never meets one belonging to a deleted calendar, so removing
// its join changes nothing observable.
//
// The state this reproduces is a live link row whose calendar is gone —
// which is what any calendar disabled by something other than the
// delete handler leaves behind. Disabling the calendar directly is the
// only way to produce it, which is the point: the query has to be
// correct on its own.
func TestPublicShareRenderIgnoresLinksOnDeletedCalendars(t *testing.T) {
	bootstrap(t)
	t.Parallel()

	tt := newTenant(t)
	calID := createCalendarMut(t, tt, "Legacy Calendar")
	const strandedTitle = "Stranded publication"
	evtID := createEventMut(t, tt, calID, strandedTitle)

	// A second event on a calendar that stays enabled. Without it, the
	// render assertion below cannot tell "the stranded event was
	// filtered" from "the render route answers nobody" — both look like
	// a page missing strandedTitle.
	liveCalID := createCalendarMut(t, tt, "Live Calendar")
	const liveTitle = "Live publication"
	liveEvtID := createEventMut(t, tt, liveCalID, liveTitle)

	var share struct {
		ID    string `json:"id"`
		Token string `json:"token"`
	}
	doJSON(t, http.MethodPost,
		testServerURL+"/workspaces/"+tt.WorkspacePublicID+"/public-shares",
		tt.AccessToken, map[string]any{"title": "Legacy share"}, &share)

	var attach struct {
		Attached int `json:"attached"`
	}
	doJSON(t, http.MethodPost,
		testServerURL+"/workspaces/"+tt.WorkspacePublicID+"/public-shares/"+share.ID+"/events",
		tt.AccessToken, map[string]any{"eventIds": []string{evtID, liveEvtID}}, &attach)
	require.Equal(t, 2, attach.Attached)

	// Disable the calendar without going through the delete handler, so
	// the link row stays enabled — the shape of every row stranded
	// before the cascade was added.
	_, err := testDB.Exec(
		`UPDATE calendars SET enabled = FALSE WHERE public_id = UUID_TO_BIN(?, 0)`, calID)
	require.NoError(t, err)

	var liveLinks int
	require.NoError(t, testDB.QueryRow(
		`SELECT COUNT(*) FROM calendar_public_share_events l
		   JOIN calendar_events e ON e.id = l.event_id
		  WHERE e.public_id = UUID_TO_BIN(?, 0) AND l.enabled = TRUE`,
		evtID).Scan(&liveLinks))
	require.Equal(t, 1, liveLinks,
		"the link must still be live, or this test is not reproducing the stranded state")

	status, rendered := doJSONStatus(t, http.MethodGet,
		testServerURL+"/share/cal/"+share.Token, "", nil)
	require.Equal(t, http.StatusOK, status, "render; body=%s", string(rendered))
	require.NotEmpty(t, rendered, "the render must answer with the page, not an empty body")
	assert.NotContains(t, string(rendered), strandedTitle,
		"a live link whose calendar is disabled must not render on the public page")

	// The other attached event's calendar was never disabled, so it must
	// still render — proving the page renders attached events at all,
	// rather than coming back empty regardless of which calendar the
	// event is on.
	assert.Contains(t, string(rendered), liveTitle,
		"an event on a calendar that stayed enabled must still render")
}
