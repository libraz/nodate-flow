// REST and MCP answering the same question the same way.
//
// The tests here cover three places where they did not: who may edit an
// event, whose day the working hours belong to, and what
// visibility='default' means. Each is a case where the agent surface
// reached its own conclusion about the same rows, and in each the
// divergence was invisible from either side alone — the web app worked,
// so the rule looked correct.
package e2e

import (
	"encoding/json"
	"fmt"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/libraz/nodate-flow/apps/flow-api/tests/helpers"
)

// mcpTool calls one MCP tool and returns the raw tool payload.
func mcpTool(t *testing.T, tt *helpers.TestTenant, name string, args map[string]any) string {
	t.Helper()
	token := mintMCPToken(t, tt.AccessToken, tt.WorkspacePublicID,
		"parity-"+randomHex(4), []string{"read:workspace", "write:workspace"})
	status, body := mcpCallRaw(t, token, "tools/call", map[string]any{
		"name":      name,
		"arguments": args,
	})
	require.Equal(t, http.StatusOK, status, "MCP transport must accept the call; body=%s", string(body))
	return mcpToolResultText(t, body)
}

// mcpToolError calls a tool expecting the tool itself to refuse, and
// returns the stable error code.
func mcpToolErrorCode(t *testing.T, tt *helpers.TestTenant, name string, args map[string]any) string {
	t.Helper()
	token := mintMCPToken(t, tt.AccessToken, tt.WorkspacePublicID,
		"parity-"+randomHex(4), []string{"read:workspace", "write:workspace"})
	_, body := mcpCallRaw(t, token, "tools/call", map[string]any{
		"name":      name,
		"arguments": args,
	})
	return mcpErrorCode(t, body)
}

// TestMCPEventEditRuleMatchesREST is M-19. A calendar manager who is
// neither the event's owner nor an attendee may move other people's
// events — that is what the manager role is for on a shared calendar.
//
// The old MCP rule asked calendars.owner_user_id instead of
// calendar_members.role, and a shared calendar leaves owner_user_id NULL
// on purpose (naming an owner makes the FK cascade take everyone's
// history with that user). So on exactly the calendars that have
// managers, no manager qualified: the same edit succeeded in the web app
// and failed through an agent.
func TestMCPEventEditRuleMatchesREST(t *testing.T) {
	bootstrap(t)
	t.Parallel()

	host := newTenant(t)
	manager := inviteAndJoinWorkspace(t, host)

	calID := createCalendarMut(t, host, "Shared Team Calendar")
	addCalendarMemberWithRole(t, host, calID, manager.Email, "manager")

	// Make it a shared calendar in the sense the schema means: nobody
	// owns it. This is the shape the old rule could not handle.
	_, err := testDB.Exec(
		`UPDATE calendars SET owner_user_id = NULL WHERE public_id = UUID_TO_BIN(?, 0)`, calID)
	require.NoError(t, err)

	evtID := createEventMut(t, host, calID, "Host's event")

	// REST: the manager may rename it.
	status, body := doJSONStatus(t, http.MethodPatch,
		testServerURL+"/workspaces/"+manager.WorkspacePublicID+
			"/calendars/"+calID+"/events/"+evtID,
		manager.AccessToken, map[string]any{"title": "Renamed over REST"})
	require.Equal(t, http.StatusOK, status,
		"a calendar manager must be able to edit an event over REST; body=%s", string(body))

	// MCP: the same person, the same event, the same answer.
	out := mcpTool(t, manager, "update_calendar_event", map[string]any{
		"eventId": evtID,
		"title":   "Renamed over MCP",
	})
	assert.NotContains(t, out, "PERMISSION",
		"a calendar manager must be able to edit the same event through MCP: %s", out)

	var title string
	require.NoError(t, testDB.QueryRow(
		`SELECT title FROM calendar_events WHERE public_id = UUID_TO_BIN(?, 0)`, evtID).Scan(&title))
	assert.Equal(t, "Renamed over MCP", title,
		"the MCP edit must have landed, not merely returned without error")
}

// TestMCPEventEditStillRefusesViewer pins the other side of the same
// rule: sharing the predicate must not widen it. A viewer is refused on
// both surfaces.
func TestMCPEventEditStillRefusesViewer(t *testing.T) {
	bootstrap(t)
	t.Parallel()

	host := newTenant(t)
	viewer := inviteAndJoinWorkspace(t, host)

	calID := createCalendarMut(t, host, "Read Only For Them")
	addCalendarMemberWithRole(t, host, calID, viewer.Email, "viewer")
	_, err := testDB.Exec(
		`UPDATE calendars SET owner_user_id = NULL WHERE public_id = UUID_TO_BIN(?, 0)`, calID)
	require.NoError(t, err)

	evtID := createEventMut(t, host, calID, "Not theirs to move")

	status, _ := doJSONStatus(t, http.MethodPatch,
		testServerURL+"/workspaces/"+viewer.WorkspacePublicID+
			"/calendars/"+calID+"/events/"+evtID,
		viewer.AccessToken, map[string]any{"title": "Viewer rename"})
	assert.Equal(t, http.StatusForbidden, status, "REST must refuse a viewer")

	code := mcpToolErrorCode(t, viewer, "update_calendar_event", map[string]any{
		"eventId": evtID,
		"title":   "Viewer rename via MCP",
	})
	assert.NotEmpty(t, code, "MCP must refuse a viewer too")

	var title string
	require.NoError(t, testDB.QueryRow(
		`SELECT title FROM calendar_events WHERE public_id = UUID_TO_BIN(?, 0)`, evtID).Scan(&title))
	assert.Equal(t, "Not theirs to move", title, "no rename may have landed")
}

// TestMCPFreeSlotsUsesUserTimezone is H-25. The working day belongs to
// the person whose day it is.
//
// Fixed at UTC, "09:00–18:00" named 18:00–03:00 for a Tokyo user: their
// real working day fell outside the query window, so every meeting in it
// was invisible, the day was reported wholly free, and the agent booked
// the middle of the night.
func TestMCPFreeSlotsUsesUserTimezone(t *testing.T) {
	bootstrap(t)
	t.Parallel()

	tt := newTenant(t)
	_, err := testDB.Exec(
		`UPDATE users SET timezone = 'Asia/Tokyo' WHERE public_id = UUID_TO_BIN(?, 0)`,
		tt.UserPublicID)
	require.NoError(t, err)

	calID := createCalendarMut(t, tt, "Tokyo Calendar")

	// A meeting at 10:00–11:00 JST, which is 01:00–02:00 UTC. Under the
	// UTC window it sat before the window opened and did not count.
	const day = "2027-05-11"
	start := time.Date(2027, 5, 11, 1, 0, 0, 0, time.UTC)
	var evt struct {
		ID string `json:"id"`
	}
	doJSON(t, http.MethodPost,
		testServerURL+"/workspaces/"+tt.WorkspacePublicID+"/calendars/"+calID+"/events",
		tt.AccessToken, map[string]any{
			"kind":     "event",
			"title":    "Morning standup",
			"startAt":  start.Unix(),
			"endAt":    start.Add(time.Hour).Unix(),
			"timezone": "Asia/Tokyo",
		}, &evt)
	require.NotEmpty(t, evt.ID)

	out := mcpTool(t, tt, "list_free_slots", map[string]any{
		"date":            day,
		"durationMinutes": 30,
	})

	var parsed struct {
		Slots []struct {
			StartAt int64 `json:"startAt"`
			EndAt   int64 `json:"endAt"`
		} `json:"slots"`
	}
	require.NoError(t, json.Unmarshal([]byte(out), &parsed), "decode tool output: %s", out)
	require.NotEmpty(t, parsed.Slots, "expected free slots on an otherwise empty day: %s", out)

	jst, err := time.LoadLocation("Asia/Tokyo")
	require.NoError(t, err)

	// Compare instants, not clock hours. Under the old UTC window the
	// slots ran 18:00 JST to 03:00 JST, whose Hour() values (18, 3) both
	// sit inside a naive 9..18 range check — an assertion written that
	// way passes against the bug it is supposed to catch.
	windowOpen := time.Date(2027, 5, 11, 9, 0, 0, 0, jst)
	windowClose := time.Date(2027, 5, 11, 18, 0, 0, 0, jst)

	for _, sl := range parsed.Slots {
		st := time.Unix(sl.StartAt, 0)
		en := time.Unix(sl.EndAt, 0)
		assert.Falsef(t, st.Before(windowOpen),
			"slot starts at %s, before the caller's working day opens at %s",
			st.In(jst).Format(time.RFC3339), windowOpen.Format(time.RFC3339))
		assert.Falsef(t, en.After(windowClose),
			"slot ends at %s, after the caller's working day closes at %s",
			en.In(jst).Format(time.RFC3339), windowClose.Format(time.RFC3339))
	}

	// The meeting has to be inside the window the tool searched, or the
	// overlap check below is vacuous: a window that never reaches 10:00
	// JST trivially proposes nothing that collides with it.
	busyStart := start
	busyEnd := start.Add(time.Hour)
	require.Falsef(t, busyStart.Before(windowOpen) || busyEnd.After(windowClose),
		"the seeded meeting (%s..%s) must lie inside the searched window (%s..%s)",
		busyStart.In(jst).Format(time.RFC3339), busyEnd.In(jst).Format(time.RFC3339),
		windowOpen.Format(time.RFC3339), windowClose.Format(time.RFC3339))

	for _, sl := range parsed.Slots {
		overlaps := sl.StartAt < busyEnd.Unix() && sl.EndAt > busyStart.Unix()
		assert.Falsef(t, overlaps,
			"a slot (%s..%s) overlaps a meeting that is on the calendar",
			time.Unix(sl.StartAt, 0).In(jst).Format(time.RFC3339),
			time.Unix(sl.EndAt, 0).In(jst).Format(time.RFC3339))
	}
}

// TestDefaultVisibilityResolvesAgainstCalendarSetting is M-49.
//
// visibility='default' is the column's own default, so most events carry
// it, and it resolved to nothing: the details of an ordinary event were
// readable by anyone who could reach the calendar and were printed in
// full on an unauthenticated share page. It now resolves against
// calendars.default_event_visibility.
func TestDefaultVisibilityResolvesAgainstCalendarSetting(t *testing.T) {
	bootstrap(t)
	t.Parallel()

	host := newTenant(t)
	member := inviteAndJoinWorkspace(t, host)

	calID := createCalendarMut(t, host, "Private By Default")
	addCalendarMemberWithRole(t, host, calID, member.Email, "editor")
	_, err := testDB.Exec(
		`UPDATE calendars SET default_event_visibility = 'private' WHERE public_id = UUID_TO_BIN(?, 0)`,
		calID)
	require.NoError(t, err)

	const secretLocation = "Boardroom 12F"
	start := time.Date(2027, 6, 2, 10, 0, 0, 0, time.UTC)
	var evt struct {
		ID string `json:"id"`
	}
	doJSON(t, http.MethodPost,
		testServerURL+"/workspaces/"+host.WorkspacePublicID+"/calendars/"+calID+"/events",
		host.AccessToken, map[string]any{
			"kind":       "event",
			"title":      "Default visibility event",
			"visibility": "default",
			"location":   secretLocation,
			"memo":       "default visibility memo",
			"startAt":    start.Unix(),
			"endAt":      start.Add(time.Hour).Unix(),
			"timezone":   "UTC",
		}, &evt)
	require.NotEmpty(t, evt.ID)

	// A co-member sees the block but not its contents, exactly as they
	// would for an explicitly private event.
	status, body := doJSONStatus(t, http.MethodGet,
		testServerURL+"/workspaces/"+member.WorkspacePublicID+
			"/calendars/"+calID+"/events/"+evt.ID,
		member.AccessToken, nil)
	require.Equal(t, http.StatusOK, status, "the block stays visible; body=%s", string(body))
	var detail struct {
		Title    string  `json:"title"`
		Location *string `json:"location"`
		Memo     *string `json:"memo"`
	}
	require.NoError(t, json.Unmarshal(body, &detail))
	assert.Equal(t, "Default visibility event", detail.Title)
	assert.Nil(t, detail.Location,
		"a calendar that defaults to private must withhold the location of a default-visibility event")
	assert.Nil(t, detail.Memo, "and the memo")

	// The owner still reads everything.
	status, body = doJSONStatus(t, http.MethodGet,
		testServerURL+"/workspaces/"+host.WorkspacePublicID+
			"/calendars/"+calID+"/events/"+evt.ID,
		host.AccessToken, nil)
	require.Equal(t, http.StatusOK, status)
	assert.Contains(t, string(body), secretLocation, "the owner reads their own event in full")

	// The unauthenticated share page is where guessing wrong is
	// irreversible, so it gets its own assertion.
	var share struct {
		ID    string `json:"id"`
		Token string `json:"token"`
	}
	doJSON(t, http.MethodPost,
		testServerURL+"/workspaces/"+host.WorkspacePublicID+"/public-shares",
		host.AccessToken, map[string]any{"title": "Default vis share"}, &share)
	var attach struct {
		Attached int `json:"attached"`
	}
	doJSON(t, http.MethodPost,
		testServerURL+"/workspaces/"+host.WorkspacePublicID+"/public-shares/"+share.ID+"/events",
		host.AccessToken, map[string]any{"eventIds": []string{evt.ID}}, &attach)
	require.Equal(t, 1, attach.Attached)

	status, rendered := doJSONStatus(t, http.MethodGet,
		testServerURL+"/share/cal/"+share.Token, "", nil)
	require.Equal(t, http.StatusOK, status, "render; body=%s", string(rendered))
	assert.NotContains(t, string(rendered), secretLocation,
		"a default-visibility event on a private-by-default calendar must render time-only")
	assert.NotContains(t, string(rendered), "Default visibility event",
		"and its title is descriptive content under the time-only contract")
}

// TestDefaultVisibilityStaysPublicWhenCalendarSaysSo guards the other
// direction: the setting defaults to public, so an existing calendar
// that never chose keeps behaving as it did. A fix that quietly made
// every default-visibility event private would pass the test above and
// break every shared calendar in the product.
func TestDefaultVisibilityStaysPublicWhenCalendarSaysSo(t *testing.T) {
	bootstrap(t)
	t.Parallel()

	host := newTenant(t)
	member := inviteAndJoinWorkspace(t, host)

	calID := createCalendarMut(t, host, "Ordinary Team Calendar")
	addCalendarMemberWithRole(t, host, calID, member.Email, "editor")

	var setting string
	require.NoError(t, testDB.QueryRow(
		`SELECT default_event_visibility FROM calendars WHERE public_id = UUID_TO_BIN(?, 0)`,
		calID).Scan(&setting))
	require.Equal(t, "public", setting, "a new calendar must default to public")

	const location = "Room 3A"
	start := time.Date(2027, 6, 9, 10, 0, 0, 0, time.UTC)
	var evt struct {
		ID string `json:"id"`
	}
	doJSON(t, http.MethodPost,
		testServerURL+"/workspaces/"+host.WorkspacePublicID+"/calendars/"+calID+"/events",
		host.AccessToken, map[string]any{
			"kind":       "event",
			"title":      "Team sync",
			"visibility": "default",
			"location":   location,
			"startAt":    start.Unix(),
			"endAt":      start.Add(time.Hour).Unix(),
			"timezone":   "UTC",
		}, &evt)
	require.NotEmpty(t, evt.ID)

	status, body := doJSONStatus(t, http.MethodGet,
		fmt.Sprintf("%s/workspaces/%s/calendars/%s/events/%s",
			testServerURL, member.WorkspacePublicID, calID, evt.ID),
		member.AccessToken, nil)
	require.Equal(t, http.StatusOK, status, "body=%s", string(body))
	assert.Contains(t, string(body), location,
		"on a public-by-default calendar a co-member still reads the details")
}

// TestMCPEventEditRequiresCalendarMembership covers the gap the static
// guard surfaced: the MCP event tools resolved an event by its own id
// and went straight to the edit rule, so somebody removed from a
// calendar kept editing the events on it that they happened to own.
// REST refuses them at the membership gate, and now so does MCP.
func TestMCPEventEditRequiresCalendarMembership(t *testing.T) {
	bootstrap(t)
	t.Parallel()

	host := newTenant(t)
	member := inviteAndJoinWorkspace(t, host)

	calID := createCalendarMut(t, host, "Departures Calendar")
	addCalendarMemberWithRole(t, host, calID, member.Email, "editor")

	// The member creates their own event, so ownership alone would carry
	// them past an edit rule that never asked about membership.
	start := time.Date(2027, 7, 6, 10, 0, 0, 0, time.UTC)
	var evt struct {
		ID string `json:"id"`
	}
	doJSON(t, http.MethodPost,
		testServerURL+"/workspaces/"+member.WorkspacePublicID+"/calendars/"+calID+"/events",
		member.AccessToken, map[string]any{
			"kind":     "event",
			"title":    "Their own event",
			"startAt":  start.Unix(),
			"endAt":    start.Add(time.Hour).Unix(),
			"timezone": "UTC",
		}, &evt)
	require.NotEmpty(t, evt.ID)

	// Remove them from the calendar. They remain a workspace member.
	doJSON(t, http.MethodDelete,
		testServerURL+"/workspaces/"+host.WorkspacePublicID+
			"/calendars/"+calID+"/members/"+member.UserPublicID,
		host.AccessToken, nil, nil)

	status, body := doJSONStatus(t, http.MethodPatch,
		testServerURL+"/workspaces/"+member.WorkspacePublicID+
			"/calendars/"+calID+"/events/"+evt.ID,
		member.AccessToken, map[string]any{"title": "Edited after removal"})
	requireDenied(t, status, body, http.StatusForbidden, "CALENDAR.CALENDAR.ACCESS_DENIED",
		"REST edit from someone removed from the calendar")

	code := mcpToolErrorCode(t, member, "update_calendar_event", map[string]any{
		"eventId": evt.ID,
		"title":   "Edited after removal via MCP",
	})
	assert.NotEmpty(t, code, "MCP must refuse the same edit")

	var title string
	require.NoError(t, testDB.QueryRow(
		`SELECT title FROM calendar_events WHERE public_id = UUID_TO_BIN(?, 0)`, evt.ID).Scan(&title))
	assert.Equal(t, "Their own event", title, "no rename may have landed")
}

// TestMCPListsRecurringOccurrences is H-19 on the read side. A weekly
// series has to appear in the agent's answer as the meetings it produces
// in the window, not as one row plus a rule the model would have to
// expand, and not — as it did — as nothing at all.
func TestMCPListsRecurringOccurrences(t *testing.T) {
	bootstrap(t)
	t.Parallel()

	tt := newTenant(t)
	calID := createCalendarMut(t, tt, "Standup Calendar")

	// Mondays at 09:00 UTC from 2027-03-01.
	start := time.Date(2027, 3, 1, 9, 0, 0, 0, time.UTC)
	var evt struct {
		ID string `json:"id"`
	}
	doJSON(t, http.MethodPost,
		testServerURL+"/workspaces/"+tt.WorkspacePublicID+"/calendars/"+calID+"/events",
		tt.AccessToken, map[string]any{
			"kind":           "event",
			"title":          "Weekly standup",
			"startAt":        start.Unix(),
			"endAt":          start.Add(time.Hour).Unix(),
			"timezone":       "UTC",
			"recurrenceRule": json.RawMessage(`{"freq":"weekly","interval":1,"byDay":["MO"]}`),
			"recurrenceEnd":  time.Date(2027, 6, 1, 0, 0, 0, 0, time.UTC).Unix(),
		}, &evt)
	require.NotEmpty(t, evt.ID)

	out := mcpTool(t, tt, "list_calendar_events", map[string]any{
		"startDate": "2027-03-01",
		"endDate":   "2027-03-29",
	})

	var parsed struct {
		Events []struct {
			Title   string `json:"title"`
			StartAt int64  `json:"startAt"`
		} `json:"events"`
	}
	require.NoError(t, json.Unmarshal([]byte(out), &parsed), "decode: %s", out)

	var got []string
	for _, e := range parsed.Events {
		if e.Title == "Weekly standup" {
			got = append(got, time.Unix(e.StartAt, 0).UTC().Format("2006-01-02T15:04:05Z"))
		}
	}
	assert.Equal(t, []string{
		"2027-03-01T09:00:00Z",
		"2027-03-08T09:00:00Z",
		"2027-03-15T09:00:00Z",
		"2027-03-22T09:00:00Z",
	}, got, "the series must be reported as its occurrences in the window: %s", out)
}

// TestMCPFreeSlotsTreatsRecurringMeetingsAsBusy is the harm H-19 named.
// The free-slot search built its busy map from the non-recurring query,
// so a standing meeting's hour was offered as available and an agent
// booking into it double-booked the person.
func TestMCPFreeSlotsTreatsRecurringMeetingsAsBusy(t *testing.T) {
	bootstrap(t)
	t.Parallel()

	tt := newTenant(t)
	calID := createCalendarMut(t, tt, "Busy Calendar")

	// Mondays 10:00–11:00 UTC, anchored three weeks before the day the
	// search asks about, so only an expanded occurrence can be found.
	anchor := time.Date(2027, 3, 1, 10, 0, 0, 0, time.UTC)
	var evt struct {
		ID string `json:"id"`
	}
	doJSON(t, http.MethodPost,
		testServerURL+"/workspaces/"+tt.WorkspacePublicID+"/calendars/"+calID+"/events",
		tt.AccessToken, map[string]any{
			"kind":           "event",
			"title":          "Weekly one to one",
			"startAt":        anchor.Unix(),
			"endAt":          anchor.Add(time.Hour).Unix(),
			"timezone":       "UTC",
			"recurrenceRule": json.RawMessage(`{"freq":"weekly","interval":1,"byDay":["MO"]}`),
			"recurrenceEnd":  time.Date(2027, 6, 1, 0, 0, 0, 0, time.UTC).Unix(),
		}, &evt)
	require.NotEmpty(t, evt.ID)

	// 2027-03-22 is a Monday, three occurrences after the anchor.
	out := mcpTool(t, tt, "list_free_slots", map[string]any{
		"date":            "2027-03-22",
		"durationMinutes": 30,
	})
	var parsed struct {
		Slots []struct {
			StartAt int64 `json:"startAt"`
			EndAt   int64 `json:"endAt"`
		} `json:"slots"`
	}
	require.NoError(t, json.Unmarshal([]byte(out), &parsed), "decode: %s", out)
	require.NotEmpty(t, parsed.Slots, "the day is not fully booked: %s", out)

	busyStart := time.Date(2027, 3, 22, 10, 0, 0, 0, time.UTC).Unix()
	busyEnd := time.Date(2027, 3, 22, 11, 0, 0, 0, time.UTC).Unix()
	for _, sl := range parsed.Slots {
		overlaps := sl.StartAt < busyEnd && sl.EndAt > busyStart
		assert.Falsef(t, overlaps,
			"slot %s..%s overlaps an occurrence of a weekly meeting",
			time.Unix(sl.StartAt, 0).UTC().Format(time.RFC3339),
			time.Unix(sl.EndAt, 0).UTC().Format(time.RFC3339))
	}
}

// TestPatchClearsNullableEventFields is the backend half of H-26.
//
// A PATCH that only carries values cannot express removal: the field the
// caller omitted and the field they want emptied arrive identically, so
// every nullable column was write-once. The visible form was a dialog
// offering "no repeat", reporting success, and leaving the daily standup
// recurring — with no way short of deleting the series.
func TestPatchClearsNullableEventFields(t *testing.T) {
	bootstrap(t)
	t.Parallel()

	tt := newTenant(t)
	calID := createCalendarMut(t, tt, "Clearable Calendar")

	start := time.Date(2027, 4, 5, 9, 0, 0, 0, time.UTC)
	var evt struct {
		ID string `json:"id"`
	}
	doJSON(t, http.MethodPost,
		testServerURL+"/workspaces/"+tt.WorkspacePublicID+"/calendars/"+calID+"/events",
		tt.AccessToken, map[string]any{
			"kind":           "event",
			"title":          "Daily standup",
			"location":       "Room 1",
			"memo":           "bring the board",
			"url":            "https://example.test/standup",
			"startAt":        start.Unix(),
			"endAt":          start.Add(30 * time.Minute).Unix(),
			"timezone":       "UTC",
			"recurrenceRule": json.RawMessage(`{"freq":"daily","interval":1}`),
			"recurrenceEnd":  time.Date(2027, 9, 1, 0, 0, 0, 0, time.UTC).Unix(),
		}, &evt)
	require.NotEmpty(t, evt.ID)

	eventURL := testServerURL + "/workspaces/" + tt.WorkspacePublicID +
		"/calendars/" + calID + "/events/" + evt.ID

	// Sanity: the fields are set, so clearing them proves something.
	status, body := doJSONStatus(t, http.MethodGet, eventURL, tt.AccessToken, nil)
	require.Equal(t, http.StatusOK, status, "body=%s", string(body))
	require.Contains(t, string(body), "Room 1")

	status, body = doJSONStatus(t, http.MethodPatch, eventURL, tt.AccessToken, map[string]any{
		"clear": []string{"location", "memo", "url", "recurrenceRule"},
	})
	require.Equal(t, http.StatusOK, status, "clear must succeed; body=%s", string(body))

	var after struct {
		Location       *string          `json:"location"`
		Memo           *string          `json:"memo"`
		URL            *string          `json:"url"`
		RecurrenceRule *json.RawMessage `json:"recurrenceRule"`
		RecurrenceEnd  *int64           `json:"recurrenceEnd"`
		Title          string           `json:"title"`
	}
	status, body = doJSONStatus(t, http.MethodGet, eventURL, tt.AccessToken, nil)
	require.Equal(t, http.StatusOK, status, "body=%s", string(body))
	require.NoError(t, json.Unmarshal(body, &after))

	assert.Nil(t, after.Location, "location must be gone")
	assert.Nil(t, after.Memo, "memo must be gone")
	assert.Nil(t, after.URL, "url must be gone")
	assert.Equal(t, "Daily standup", after.Title, "clearing must not touch what was not named")

	// The row itself, because a mapper that omits an empty field would
	// make the assertions above pass on a column that still holds a rule.
	var rule, seriesEnd any
	require.NoError(t, testDB.QueryRow(
		`SELECT recurrence_rule, recurrence_end FROM calendar_events
		  WHERE public_id = UUID_TO_BIN(?, 0)`, evt.ID).Scan(&rule, &seriesEnd))
	assert.Nil(t, rule, "recurrence_rule must be NULL in the row")
	assert.Nil(t, seriesEnd,
		"clearing the rule must clear recurrence_end with it: it only describes a series")

	// The meeting has actually stopped recurring, which is the thing the
	// user was trying to do.
	out := mcpTool(t, tt, "list_calendar_events", map[string]any{
		"startDate": "2027-04-05",
		"endDate":   "2027-04-12",
	})
	var parsed struct {
		Events []struct {
			Title string `json:"title"`
		} `json:"events"`
	}
	require.NoError(t, json.Unmarshal([]byte(out), &parsed), "decode: %s", out)
	count := 0
	for _, e := range parsed.Events {
		if e.Title == "Daily standup" {
			count++
		}
	}
	assert.Equal(t, 1, count,
		"after clearing the rule the event must occur once, not daily: %s", out)
}

// TestPatchClearRejectsUnknownField pins the refusal. Ignoring a name it
// does not recognise would answer a removal request with success and
// leave the value in place, which is the failure this mechanism exists
// to remove.
func TestPatchClearRejectsUnknownField(t *testing.T) {
	bootstrap(t)
	t.Parallel()

	tt := newTenant(t)
	calID := createCalendarMut(t, tt, "Reject Calendar")
	evtID := createEventMut(t, tt, calID, "Unchanged")

	status, body := doJSONStatus(t, http.MethodPatch,
		testServerURL+"/workspaces/"+tt.WorkspacePublicID+
			"/calendars/"+calID+"/events/"+evtID,
		tt.AccessToken, map[string]any{"clear": []string{"titel"}})
	assert.GreaterOrEqual(t, status, 400,
		"an unrecognised clear target must be refused; body=%s", string(body))
}

// TestRecurrenceEndStopsExpansion is H-26's other half at the API
// boundary: setting recurrenceEnd has to actually end the series.
func TestRecurrenceEndStopsExpansion(t *testing.T) {
	bootstrap(t)
	t.Parallel()

	tt := newTenant(t)
	calID := createCalendarMut(t, tt, "Ending Calendar")

	start := time.Date(2027, 4, 5, 9, 0, 0, 0, time.UTC)
	var evt struct {
		ID string `json:"id"`
	}
	doJSON(t, http.MethodPost,
		testServerURL+"/workspaces/"+tt.WorkspacePublicID+"/calendars/"+calID+"/events",
		tt.AccessToken, map[string]any{
			"kind":           "event",
			"title":          "Ends on Wednesday",
			"startAt":        start.Unix(),
			"endAt":          start.Add(time.Hour).Unix(),
			"timezone":       "UTC",
			"recurrenceRule": json.RawMessage(`{"freq":"daily","interval":1}`),
			"recurrenceEnd":  time.Date(2027, 4, 7, 23, 59, 59, 0, time.UTC).Unix(),
		}, &evt)
	require.NotEmpty(t, evt.ID)

	out := mcpTool(t, tt, "list_calendar_events", map[string]any{
		"startDate": "2027-04-05",
		"endDate":   "2027-04-15",
	})
	var parsed struct {
		Events []struct {
			Title   string `json:"title"`
			StartAt int64  `json:"startAt"`
		} `json:"events"`
	}
	require.NoError(t, json.Unmarshal([]byte(out), &parsed), "decode: %s", out)

	var got []string
	for _, e := range parsed.Events {
		if e.Title == "Ends on Wednesday" {
			got = append(got, time.Unix(e.StartAt, 0).UTC().Format("2006-01-02"))
		}
	}
	assert.Equal(t, []string{"2027-04-05", "2027-04-06", "2027-04-07"}, got,
		"the series must stop at recurrenceEnd rather than run on: %s", out)
}

// TestAllDayEventIsStoredAsACanonicalDate is the backend half of H-23.
//
// "All day on 5 August" is a date, and a date is the same square on the
// calendar for everyone. The column pair is DATETIME, so the date has to
// be encoded as an instant — and which instant it is has to be one
// thing. It was two: the browser sent local midnight and MCP sent UTC
// midnight, so a Tokyo user's holiday arrived as 2026-08-04T15:00Z and
// showed as the 4th in Europe while its author had called it the 5th.
func TestAllDayEventIsStoredAsACanonicalDate(t *testing.T) {
	bootstrap(t)
	t.Parallel()

	tt := newTenant(t)
	calID := createCalendarMut(t, tt, "All Day Calendar")

	jst, err := time.LoadLocation("Asia/Tokyo")
	require.NoError(t, err)

	// What the browser dialog sends today: local midnight on 5 August,
	// which is 2027-08-04T15:00Z.
	localMidnight := time.Date(2027, 8, 5, 0, 0, 0, 0, jst)
	require.Equal(t, "2027-08-04", localMidnight.UTC().Format("2006-01-02"),
		"the fixture must actually straddle the date line, or it proves nothing")

	var evt struct {
		ID      string `json:"id"`
		StartAt *int64 `json:"startAt"`
		EndAt   *int64 `json:"endAt"`
	}
	doJSON(t, http.MethodPost,
		testServerURL+"/workspaces/"+tt.WorkspacePublicID+"/calendars/"+calID+"/events",
		tt.AccessToken, map[string]any{
			"kind":     "event",
			"title":    "Company holiday",
			"allDay":   true,
			"startAt":  localMidnight.Unix(),
			"endAt":    localMidnight.Add(24 * time.Hour).Unix(),
			"timezone": "Asia/Tokyo",
		}, &evt)
	require.NotEmpty(t, evt.ID)
	require.NotNil(t, evt.StartAt)

	stored := time.Unix(*evt.StartAt, 0).UTC()
	assert.Equal(t, 0, stored.Hour(), "an all-day event must be stored at midnight UTC")
	assert.Equal(t, 0, stored.Minute())
	assert.Equal(t, "2027-08-04", stored.Format("2006-01-02"),
		"the date is the UTC day the request's instant falls on")

	out := mcpTool(t, tt, "list_calendar_events", map[string]any{
		"startDate": "2027-08-01",
		"endDate":   "2027-08-10",
	})
	var parsed struct {
		Events []struct {
			Title     string `json:"title"`
			AllDay    bool   `json:"allDay"`
			StartDate string `json:"startDate"`
		} `json:"events"`
	}
	require.NoError(t, json.Unmarshal([]byte(out), &parsed), "decode: %s", out)
	found := false
	for _, e := range parsed.Events {
		if e.Title != "Company holiday" {
			continue
		}
		found = true
		assert.True(t, e.AllDay)
		assert.Equal(t, stored.Format("2006-01-02"), e.StartDate,
			"MCP must report the same date the row encodes: %s", out)
	}
	assert.True(t, found, "the all-day event must appear in the MCP listing: %s", out)
}

// TestAllDayNormalisationSurvivesAPatch covers the case where the flag
// and the times move separately: flipping allDay on later has to pin the
// instants the row already had, or it keeps wall-clock times that read
// as a different day for half the workspace.
func TestAllDayNormalisationSurvivesAPatch(t *testing.T) {
	bootstrap(t)
	t.Parallel()

	tt := newTenant(t)
	calID := createCalendarMut(t, tt, "Patch All Day Calendar")

	start := time.Date(2027, 8, 5, 22, 0, 0, 0, time.UTC)
	var evt struct {
		ID string `json:"id"`
	}
	doJSON(t, http.MethodPost,
		testServerURL+"/workspaces/"+tt.WorkspacePublicID+"/calendars/"+calID+"/events",
		tt.AccessToken, map[string]any{
			"kind":     "event",
			"title":    "Becomes all day",
			"startAt":  start.Unix(),
			"endAt":    start.Add(time.Hour).Unix(),
			"timezone": "UTC",
		}, &evt)
	require.NotEmpty(t, evt.ID)

	eventURL := testServerURL + "/workspaces/" + tt.WorkspacePublicID +
		"/calendars/" + calID + "/events/" + evt.ID
	status, body := doJSONStatus(t, http.MethodPatch, eventURL, tt.AccessToken,
		map[string]any{"allDay": true})
	require.Equal(t, http.StatusOK, status, "body=%s", string(body))

	var after struct {
		AllDay  bool   `json:"allDay"`
		StartAt *int64 `json:"startAt"`
	}
	require.NoError(t, json.Unmarshal(body, &after))
	require.True(t, after.AllDay)
	require.NotNil(t, after.StartAt)

	stored := time.Unix(*after.StartAt, 0).UTC()
	assert.Equal(t, 0, stored.Hour(),
		"flipping allDay must pin the existing instants to midnight UTC, not leave 22:00")
	assert.Equal(t, "2027-08-05", stored.Format("2006-01-02"),
		"and it must stay on the day the row was already on")
}
