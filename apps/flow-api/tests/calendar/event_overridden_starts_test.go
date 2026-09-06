package calendar

import (
	"net/http"
	"net/url"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/libraz/nodate-flow/apps/flow-api/tests/helpers"
)

// Tests in this file pin what the two range endpoints tell the client
// about occurrences a separate override row already stands in for.
//
// The client-side expander suppresses a replaced occurrence only when the
// master carries overriddenStarts. Served without it, the master keeps
// emitting the occurrence its override replaced while the override row
// renders at its own time, so an edited occurrence appears twice in the
// grid — once under its new title and once under the old one.
//
// The handlers under test are calendars.ListCalendarEvents
// (GET /workspaces/{wsId}/calendar-events) and
// calendars.ListMyCalendarEvents (GET /me/calendar-events).

// rangeEventView is the slice of a range response these tests read.
// OverriddenStarts is a list of RFC 3339 instants, matching how
// recurrenceExceptions is spelled so the shared expander parses both
// through one reader.
type rangeEventView struct {
	ID               string         `json:"id"`
	Title            string         `json:"title"`
	StartAt          *int64         `json:"startAt"`
	RecurrenceRule   map[string]any `json:"recurrenceRule"`
	OverriddenStarts []string       `json:"overriddenStarts"`
}

// listWorkspaceCalendarEvents reads the per-workspace cross-calendar
// range endpoint over the given window.
func listWorkspaceCalendarEvents(t *testing.T, tt *helpers.CalendarTestTenant, token string, start, end time.Time) []rangeEventView {
	t.Helper()
	q := url.Values{}
	q.Set("start", start.Format(time.RFC3339))
	q.Set("end", end.Format(time.RFC3339))

	var resp struct {
		Events []rangeEventView `json:"events"`
	}
	helpers.DoJSON(t, http.MethodGet, tt.WsPath("calendar-events")+"?"+q.Encode(), token, nil, &resp)
	return resp.Events
}

// listMyCalendarEvents reads the cross-workspace range endpoint over the
// given window.
func listMyCalendarEvents(t *testing.T, tt *helpers.CalendarTestTenant, token string, start, end time.Time) []rangeEventView {
	t.Helper()
	var resp struct {
		Events []rangeEventView `json:"events"`
	}
	helpers.DoJSON(t, http.MethodGet,
		meCalendarEventsURL(tt.BaseURL, start.Format(time.RFC3339), end.Format(time.RFC3339)),
		token, nil, &resp)
	return resp.Events
}

func findRangeEvent(t *testing.T, events []rangeEventView, id string) rangeEventView {
	t.Helper()
	for _, e := range events {
		if e.ID == id {
			return e
		}
	}
	t.Fatalf("event %s not in range response (%d events)", id, len(events))
	return rangeEventView{}
}

// overrideOneOccurrence edits a single occurrence of a series and returns
// the public ID of the override row it creates.
func overrideOneOccurrence(t *testing.T, tt *helpers.CalendarTestTenant, token, calID, masterID, title string, occurrenceStart time.Time) string {
	t.Helper()
	var patched eventView
	helpers.DoJSON(t, http.MethodPatch, tt.WsPath("calendars", calID, "events", masterID), token, map[string]any{
		"scope":           "occurrence",
		"occurrenceStart": occurrenceStart.Unix(),
		"title":           title,
	}, &patched)
	require.NotEqual(t, masterID, patched.ID, "an occurrence edit must not rewrite the master")
	return patched.ID
}

// TestListCalendarEvents_MasterCarriesOverriddenStart is the direct
// regression guard for the duplicate: the master must name the start its
// override replaced, and it must name it in the shape the expander reads.
func TestListCalendarEvents_MasterCarriesOverriddenStart(t *testing.T) {
	bootstrap(t)
	t.Parallel()

	tt := newTenant(t)
	calID := createCalendar(t, tt)

	first := time.Date(2027, 6, 7, 10, 0, 0, 0, time.UTC)
	masterID := weeklySeries(t, tt, calID, first)
	third := first.AddDate(0, 0, 14)
	overrideID := overrideOneOccurrence(t, tt, tt.AccessToken, calID, masterID, "Stand-up (moved)", third)

	events := listWorkspaceCalendarEvents(t, tt, tt.AccessToken, first.AddDate(0, 0, -1), first.AddDate(0, 0, 28))

	master := findRangeEvent(t, events, masterID)
	require.NotNil(t, master.RecurrenceRule, "the master must still carry its rule")
	assert.Equal(t,
		[]string{third.UTC().Format(time.RFC3339)},
		master.OverriddenStarts,
		"the master must name the occurrence its override stands in for, as an RFC 3339 instant")

	// An override owns no rule of its own and stands in for nothing, so it
	// must not carry the field: an expander reading it there would subtract
	// a start from a row that emits no occurrences.
	override := findRangeEvent(t, events, overrideID)
	assert.Nil(t, override.RecurrenceRule, "an override owns no rule of its own")
	assert.Empty(t, override.OverriddenStarts, "only a recurring master carries overriddenStarts")
}

// TestListMyCalendarEvents_MasterCarriesOverriddenStart is the same
// guarantee on the cross-workspace endpoint, which is the one the unified
// flow-web calendar actually reads.
func TestListMyCalendarEvents_MasterCarriesOverriddenStart(t *testing.T) {
	bootstrap(t)
	t.Parallel()

	tt := newTenant(t)
	calID := createCalendar(t, tt)

	first := time.Date(2027, 7, 5, 10, 0, 0, 0, time.UTC)
	masterID := weeklySeries(t, tt, calID, first)
	third := first.AddDate(0, 0, 14)
	overrideID := overrideOneOccurrence(t, tt, tt.AccessToken, calID, masterID, "Stand-up (moved)", third)

	events := listMyCalendarEvents(t, tt, tt.AccessToken, first.AddDate(0, 0, -1), first.AddDate(0, 0, 28))

	master := findRangeEvent(t, events, masterID)
	require.NotNil(t, master.RecurrenceRule)
	assert.Equal(t,
		[]string{third.UTC().Format(time.RFC3339)},
		master.OverriddenStarts,
		"the master must name the occurrence its override stands in for, as an RFC 3339 instant")

	override := findRangeEvent(t, events, overrideID)
	assert.Empty(t, override.OverriddenStarts, "only a recurring master carries overriddenStarts")
}

// TestListCalendarEvents_UnoverriddenMasterCarriesNoStarts keeps the
// field off a series nothing stands in for, so a client cannot read an
// empty list as "some occurrence was replaced".
func TestListCalendarEvents_UnoverriddenMasterCarriesNoStarts(t *testing.T) {
	bootstrap(t)
	t.Parallel()

	tt := newTenant(t)
	calID := createCalendar(t, tt)

	first := time.Date(2027, 8, 2, 10, 0, 0, 0, time.UTC)
	masterID := weeklySeries(t, tt, calID, first)

	events := listWorkspaceCalendarEvents(t, tt, tt.AccessToken, first.AddDate(0, 0, -1), first.AddDate(0, 0, 28))

	master := findRangeEvent(t, events, masterID)
	require.NotNil(t, master.RecurrenceRule)
	assert.Empty(t, master.OverriddenStarts, "a series with no override names no replaced start")
}

// TestListCalendarEvents_OverrideOutsideRangeStillSuppressed covers the
// reason the underlying read carries no date filter. An occurrence moved
// out of the window is still replaced inside it, and a range-filtered
// read would let the master re-emit the occurrence that moved away —
// which is the duplicate the field exists to prevent.
func TestListCalendarEvents_OverrideOutsideRangeStillSuppressed(t *testing.T) {
	bootstrap(t)
	t.Parallel()

	tt := newTenant(t)
	calID := createCalendar(t, tt)

	first := time.Date(2027, 9, 6, 10, 0, 0, 0, time.UTC)
	masterID := weeklySeries(t, tt, calID, first)
	second := first.AddDate(0, 0, 7)
	movedTo := first.AddDate(0, 6, 0)

	var patched eventView
	helpers.DoJSON(t, http.MethodPatch, tt.WsPath("calendars", calID, "events", masterID), tt.AccessToken, map[string]any{
		"scope":           "occurrence",
		"occurrenceStart": second.Unix(),
		"startAt":         movedTo.Unix(),
		"endAt":           movedTo.Add(30 * time.Minute).Unix(),
	}, &patched)
	require.NotEqual(t, masterID, patched.ID)

	// A window that contains the replaced occurrence but not the override.
	events := listWorkspaceCalendarEvents(t, tt, tt.AccessToken, first.AddDate(0, 0, -1), first.AddDate(0, 0, 21))

	for _, e := range events {
		assert.NotEqual(t, patched.ID, e.ID, "the override sits outside this window")
	}
	master := findRangeEvent(t, events, masterID)
	assert.Equal(t,
		[]string{second.UTC().Format(time.RFC3339)},
		master.OverriddenStarts,
		"a replaced occurrence stays suppressed even when its replacement moved out of the window")
}

// TestListCalendarEvents_ConfidentialOverrideNotSubtractedForOthers pins
// the visibility scoping. The read is deliberately narrowed to the
// overrides the viewer may see: an override they cannot see is not served
// to them by any other read either, so subtracting its original start
// would suppress the master's occurrence while the replacement stayed
// hidden and the meeting would leave that viewer's calendar entirely.
// It keeps showing at its original time instead.
func TestListCalendarEvents_ConfidentialOverrideNotSubtractedForOthers(t *testing.T) {
	bootstrap(t)
	t.Parallel()

	owner := newTenant(t)
	calID := createCalendar(t, owner)
	calInternalID := helpers.ResolveCalendarInternalID(t, testDB, calID)
	member := helpers.CreateExtraCalendarMember(t, testSrv, owner, calInternalID, "editor")

	first := time.Date(2027, 10, 4, 10, 0, 0, 0, time.UTC)
	masterID := weeklySeries(t, owner, calID, first)
	third := first.AddDate(0, 0, 14)

	var patched eventView
	helpers.DoJSON(t, http.MethodPatch, owner.WsPath("calendars", calID, "events", masterID), owner.AccessToken, map[string]any{
		"scope":           "occurrence",
		"occurrenceStart": third.Unix(),
		"visibility":      "confidential",
	}, &patched)
	require.NotEqual(t, masterID, patched.ID)

	rangeStart, rangeEnd := first.AddDate(0, 0, -1), first.AddDate(0, 0, 28)

	ownerMaster := findRangeEvent(t, listWorkspaceCalendarEvents(t, owner, owner.AccessToken, rangeStart, rangeEnd), masterID)
	assert.Equal(t,
		[]string{third.UTC().Format(time.RFC3339)},
		ownerMaster.OverriddenStarts,
		"the owner sees the override, so the occurrence it replaces is suppressed for them")

	memberEvents := listWorkspaceCalendarEvents(t, member, member.AccessToken, rangeStart, rangeEnd)
	for _, e := range memberEvents {
		assert.NotEqual(t, patched.ID, e.ID, "a confidential override is not served to another member")
	}
	memberMaster := findRangeEvent(t, memberEvents, masterID)
	assert.Empty(t, memberMaster.OverriddenStarts,
		"an occurrence whose replacement the viewer cannot see keeps showing at its original time")
}
