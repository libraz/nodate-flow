package calendar

import (
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/nodate-flow/nodate-flow/apps/flow-api/tests/helpers"
)

// createCalendar is a test helper that creates a personal calendar and
// returns its public ID. The only user-facing calendar kind is
// "personal"; "system" calendars are provisioned automatically per
// workspace country.
func createCalendar(t *testing.T, tt *helpers.CalendarTestTenant) string {
	t.Helper()
	var cal struct {
		ID string `json:"id"`
	}
	helpers.DoJSON(t, http.MethodPost, tt.WsPath("calendars"), tt.AccessToken, map[string]any{
		"kind": "personal", "name": "Events Cal " + t.Name(), "color": "#4285F4",
	}, &cal)
	require.NotEmpty(t, cal.ID)
	return cal.ID
}

func TestCreateEvent(t *testing.T) {
	bootstrap(t)
	t.Parallel()

	tt := newTenant(t)
	calID := createCalendar(t, tt)

	start := time.Date(2026, 5, 1, 10, 0, 0, 0, time.UTC)
	end := time.Date(2026, 5, 1, 11, 0, 0, 0, time.UTC)

	body := map[string]any{
		"kind":     "event",
		"title":    "Sprint Planning",
		"startAt":  start.Unix(),
		"endAt":    end.Unix(),
		"timezone": "Asia/Tokyo",
	}
	var resp struct {
		ID       string `json:"id"`
		Kind     string `json:"kind"`
		Title    string `json:"title"`
		ShowAs   string `json:"showAs"`
		Timezone string `json:"timezone"`
	}
	helpers.DoJSON(t, http.MethodPost, tt.WsPath("calendars", calID, "events"), tt.AccessToken, body, &resp)

	require.NotEmpty(t, resp.ID)
	assert.Equal(t, "event", resp.Kind)
	assert.Equal(t, "Sprint Planning", resp.Title)
	assert.Equal(t, "busy", resp.ShowAs)
	assert.Equal(t, "Asia/Tokyo", resp.Timezone)
}

func TestCreateEventWithKinds(t *testing.T) {
	bootstrap(t)
	t.Parallel()

	tt := newTenant(t)
	calID := createCalendar(t, tt)

	tests := []struct {
		kind       string
		wantShowAs string
	}{
		{"event", "busy"},
		{"block", "busy"},
		{"free", "free"},
	}

	for _, tc := range tests {
		t.Run(tc.kind, func(t *testing.T) {
			body := map[string]any{
				"kind":     tc.kind,
				"title":    "Test " + tc.kind,
				"startAt":  time.Date(2026, 6, 1, 9, 0, 0, 0, time.UTC).Unix(),
				"endAt":    time.Date(2026, 6, 1, 10, 0, 0, 0, time.UTC).Unix(),
				"timezone": "UTC",
			}
			if tc.kind == "free" {
				body["showAs"] = "free"
			}

			var resp struct {
				Kind   string `json:"kind"`
				ShowAs string `json:"showAs"`
			}
			helpers.DoJSON(t, http.MethodPost, tt.WsPath("calendars", calID, "events"), tt.AccessToken, body, &resp)
			assert.Equal(t, tc.kind, resp.Kind)
			assert.Equal(t, tc.wantShowAs, resp.ShowAs)
		})
	}
}

func TestListEventsByRange(t *testing.T) {
	bootstrap(t)
	t.Parallel()

	tt := newTenant(t)
	calID := createCalendar(t, tt)

	dates := []time.Time{
		time.Date(2026, 5, 1, 10, 0, 0, 0, time.UTC),
		time.Date(2026, 5, 15, 10, 0, 0, 0, time.UTC),
		time.Date(2026, 6, 1, 10, 0, 0, 0, time.UTC),
	}
	for _, d := range dates {
		helpers.DoJSON(t, http.MethodPost, tt.WsPath("calendars", calID, "events"), tt.AccessToken, map[string]any{
			"kind":     "event",
			"title":    time.Month(d.Month()).String() + " event",
			"startAt":  d.Unix(),
			"endAt":    d.Add(1 * time.Hour).Unix(),
			"timezone": "UTC",
		}, nil)
	}

	rangeStart := time.Date(2026, 5, 1, 0, 0, 0, 0, time.UTC)
	rangeEnd := time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC)

	var resp struct {
		Events []struct {
			Title string `json:"title"`
		} `json:"events"`
	}
	url := tt.WsPath("calendars", calID, "events") +
		"?start=" + rangeStart.Format(time.RFC3339) +
		"&end=" + rangeEnd.Format(time.RFC3339)
	helpers.DoJSON(t, http.MethodGet, url, tt.AccessToken, nil, &resp)

	assert.Equal(t, 2, len(resp.Events), "should return exactly 2 events in May")
}

func TestPatchEvent(t *testing.T) {
	bootstrap(t)
	t.Parallel()

	tt := newTenant(t)
	calID := createCalendar(t, tt)

	var created struct {
		ID string `json:"id"`
	}
	helpers.DoJSON(t, http.MethodPost, tt.WsPath("calendars", calID, "events"), tt.AccessToken, map[string]any{
		"kind":     "event",
		"title":    "Original Title",
		"startAt":  time.Date(2026, 7, 1, 10, 0, 0, 0, time.UTC).Unix(),
		"endAt":    time.Date(2026, 7, 1, 11, 0, 0, 0, time.UTC).Unix(),
		"timezone": "UTC",
	}, &created)

	var patched struct {
		Title string `json:"title"`
	}
	helpers.DoJSON(t, http.MethodPatch, tt.WsPath("calendars", calID, "events", created.ID), tt.AccessToken, map[string]any{
		"title": "Updated Title",
	}, &patched)

	assert.Equal(t, "Updated Title", patched.Title)
}

func TestDeleteEvent(t *testing.T) {
	bootstrap(t)
	t.Parallel()

	tt := newTenant(t)
	calID := createCalendar(t, tt)

	var created struct {
		ID string `json:"id"`
	}
	helpers.DoJSON(t, http.MethodPost, tt.WsPath("calendars", calID, "events"), tt.AccessToken, map[string]any{
		"kind":     "event",
		"title":    "Delete Me",
		"startAt":  time.Date(2026, 8, 1, 10, 0, 0, 0, time.UTC).Unix(),
		"endAt":    time.Date(2026, 8, 1, 11, 0, 0, 0, time.UTC).Unix(),
		"timezone": "UTC",
	}, &created)

	var deleted struct {
		Deleted bool `json:"deleted"`
	}
	helpers.DoJSON(t, http.MethodDelete, tt.WsPath("calendars", calID, "events", created.ID), tt.AccessToken, nil, &deleted)
	assert.True(t, deleted.Deleted)

	status, _ := helpers.DoJSONStatus(t, http.MethodGet, tt.WsPath("calendars", calID, "events", created.ID), tt.AccessToken, nil)
	assert.Equal(t, http.StatusNotFound, status)
}

func TestEventPermission_NonOwnerCannotEditOthersEvent(t *testing.T) {
	bootstrap(t)
	t.Parallel()

	owner := newTenant(t)
	calID := createCalendar(t, owner)

	calInternalID := helpers.ResolveCalendarInternalID(t, testDB, calID)
	member := helpers.CreateExtraCalendarMember(t, testSrv, owner.WorkspaceID, owner.WorkspacePublicID, calInternalID, "")

	var evt struct {
		ID string `json:"id"`
	}
	helpers.DoJSON(t, http.MethodPost, owner.WsPath("calendars", calID, "events"), owner.AccessToken, map[string]any{
		"kind":     "event",
		"title":    "Owner Event",
		"startAt":  time.Date(2026, 9, 1, 10, 0, 0, 0, time.UTC).Unix(),
		"endAt":    time.Date(2026, 9, 1, 11, 0, 0, 0, time.UTC).Unix(),
		"timezone": "UTC",
	}, &evt)

	status, body := helpers.DoJSONStatus(t, http.MethodPatch, member.WsPath("calendars", calID, "events", evt.ID), member.AccessToken, map[string]any{
		"title": "Hostile Update",
	})
	assert.Equal(t, http.StatusForbidden, status)
	assert.Contains(t, string(body), "CALENDAR.EVENT.EDIT_PERMISSION_REQUIRED")
}

func TestEventPermission_NonOwnerCannotSetOtherOwner(t *testing.T) {
	bootstrap(t)
	t.Parallel()

	owner := newTenant(t)
	calID := createCalendar(t, owner)

	calInternalID := helpers.ResolveCalendarInternalID(t, testDB, calID)
	member := helpers.CreateExtraCalendarMember(t, testSrv, owner.WorkspaceID, owner.WorkspacePublicID, calInternalID, "")

	status, body := helpers.DoJSONStatus(t, http.MethodPost, member.WsPath("calendars", calID, "events"), member.AccessToken, map[string]any{
		"kind":        "event",
		"title":       "Forged Delegation",
		"startAt":     time.Date(2026, 12, 1, 10, 0, 0, 0, time.UTC).Unix(),
		"endAt":       time.Date(2026, 12, 1, 11, 0, 0, 0, time.UTC).Unix(),
		"timezone":    "UTC",
		"ownerUserId": owner.UserPublicID.String(),
	})
	assert.Equal(t, http.StatusForbidden, status)
	assert.Contains(t, string(body), "CALENDAR.EVENT.EDIT_PERMISSION_REQUIRED")
}

func TestEventPermission_NonOwnerCannotDeleteOthersEvent(t *testing.T) {
	bootstrap(t)
	t.Parallel()

	owner := newTenant(t)
	calID := createCalendar(t, owner)

	calInternalID := helpers.ResolveCalendarInternalID(t, testDB, calID)
	member := helpers.CreateExtraCalendarMember(t, testSrv, owner.WorkspaceID, owner.WorkspacePublicID, calInternalID, "")

	var evt struct {
		ID string `json:"id"`
	}
	helpers.DoJSON(t, http.MethodPost, owner.WsPath("calendars", calID, "events"), owner.AccessToken, map[string]any{
		"kind":     "event",
		"title":    "Do Not Delete",
		"startAt":  time.Date(2026, 10, 1, 10, 0, 0, 0, time.UTC).Unix(),
		"endAt":    time.Date(2026, 10, 1, 11, 0, 0, 0, time.UTC).Unix(),
		"timezone": "UTC",
	}, &evt)

	status, body := helpers.DoJSONStatus(t, http.MethodDelete, member.WsPath("calendars", calID, "events", evt.ID), member.AccessToken, nil)
	assert.Equal(t, http.StatusForbidden, status)
	assert.Contains(t, string(body), "CALENDAR.EVENT.EDIT_PERMISSION_REQUIRED")
}

func TestEventPermission_AttendeeWithCanEditCanEdit(t *testing.T) {
	bootstrap(t)
	t.Parallel()

	owner := newTenant(t)
	calID := createCalendar(t, owner)

	calInternalID := helpers.ResolveCalendarInternalID(t, testDB, calID)
	member := helpers.CreateExtraCalendarMember(t, testSrv, owner.WorkspaceID, owner.WorkspacePublicID, calInternalID, "")

	var evt struct {
		ID string `json:"id"`
	}
	helpers.DoJSON(t, http.MethodPost, owner.WsPath("calendars", calID, "events"), owner.AccessToken, map[string]any{
		"kind":     "event",
		"title":    "Team Meeting",
		"startAt":  time.Date(2026, 11, 1, 10, 0, 0, 0, time.UTC).Unix(),
		"endAt":    time.Date(2026, 11, 1, 11, 0, 0, 0, time.UTC).Unix(),
		"timezone": "UTC",
	}, &evt)

	helpers.DoJSON(t, http.MethodPost, owner.WsPath("calendars", calID, "events", evt.ID, "attendees"), owner.AccessToken, map[string]any{
		"userIds": []string{member.UserPublicID.String()},
	}, nil)

	helpers.DoJSON(t, http.MethodPatch, owner.WsPath("calendars", calID, "events", evt.ID, "attendees", member.UserPublicID.String(), "can-edit"), owner.AccessToken, map[string]any{
		"canEdit": true,
	}, nil)

	var patched struct {
		Title string `json:"title"`
	}
	helpers.DoJSON(t, http.MethodPatch, member.WsPath("calendars", calID, "events", evt.ID), member.AccessToken, map[string]any{
		"title": "Attendee Updated",
	}, &patched)
	assert.Equal(t, "Attendee Updated", patched.Title)
}

func TestPrivateEventVisibility_ScrubsFieldsForNonOwner(t *testing.T) {
	bootstrap(t)
	t.Parallel()

	owner := newTenant(t)
	calID := createCalendar(t, owner)

	calInternalID := helpers.ResolveCalendarInternalID(t, testDB, calID)
	member := helpers.CreateExtraCalendarMember(t, testSrv, owner.WorkspaceID, owner.WorkspacePublicID, calInternalID, "")

	start := time.Date(2027, 1, 15, 9, 0, 0, 0, time.UTC)
	end := time.Date(2027, 1, 15, 10, 0, 0, 0, time.UTC)

	var created struct {
		ID string `json:"id"`
	}
	helpers.DoJSON(t, http.MethodPost, owner.WsPath("calendars", calID, "events"), owner.AccessToken, map[string]any{
		"kind":       "event",
		"visibility": "private",
		"title":      "Private Meeting",
		"startAt":    start.Unix(),
		"endAt":      end.Unix(),
		"timezone":   "UTC",
		"location":   "HQ Room 3",
		"memo":       "Budget review notes",
		"url":        "https://internal.example/meet",
	}, &created)
	require.NotEmpty(t, created.ID)

	type eventView struct {
		Title    string  `json:"title"`
		StartAt  *int64  `json:"startAt"`
		EndAt    *int64  `json:"endAt"`
		Location *string `json:"location"`
		Memo     *string `json:"memo"`
		URL      *string `json:"url"`
	}

	var memberView eventView
	helpers.DoJSON(t, http.MethodGet, member.WsPath("calendars", calID, "events", created.ID), member.AccessToken, nil, &memberView)

	assert.Equal(t, "Private Meeting", memberView.Title)
	require.NotNil(t, memberView.StartAt)
	require.NotNil(t, memberView.EndAt)
	assert.Equal(t, start.Unix(), *memberView.StartAt)
	assert.Equal(t, end.Unix(), *memberView.EndAt)
	assert.Nil(t, memberView.Location, "location must be scrubbed for non-owner on private event")
	assert.Nil(t, memberView.Memo, "memo must be scrubbed for non-owner on private event")
	assert.Nil(t, memberView.URL, "url must be scrubbed for non-owner on private event")

	var ownerView eventView
	helpers.DoJSON(t, http.MethodGet, owner.WsPath("calendars", calID, "events", created.ID), owner.AccessToken, nil, &ownerView)

	assert.Equal(t, "Private Meeting", ownerView.Title)
	require.NotNil(t, ownerView.Location)
	require.NotNil(t, ownerView.Memo)
	require.NotNil(t, ownerView.URL)
	assert.Equal(t, "HQ Room 3", *ownerView.Location)
	assert.Equal(t, "Budget review notes", *ownerView.Memo)
	assert.Equal(t, "https://internal.example/meet", *ownerView.URL)
}

// TestListCrossCalendarEvents_BadDateRangeReturnsApiError exercises the
// parseFlexibleTime sentinel path to confirm the workspace-level
// /calendar-events endpoint translates a malformed range parameter into
// the CALENDAR.EVENT.DATE_RANGE_UNPARSEABLE apierror code rather than
// leaking the raw fmt.Errorf message. Audit H5 regression guard.
func TestListCrossCalendarEvents_BadDateRangeReturnsApiError(t *testing.T) {
	bootstrap(t)
	t.Parallel()

	tt := newTenant(t)

	cases := []struct {
		name  string
		query string
	}{
		{"bad_start", "?start=not-a-date&end=2026-05-01T00:00:00Z"},
		{"bad_end", "?start=2026-05-01T00:00:00Z&end=tomorrow"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			url := tt.WsPath("calendar-events") + tc.query
			status, body := helpers.DoJSONStatus(t, http.MethodGet, url, tt.AccessToken, nil)
			assert.Equal(t, http.StatusUnprocessableEntity, status, "body=%s", string(body))
			assert.Contains(t, string(body), "CALENDAR.EVENT.DATE_RANGE_UNPARSEABLE", "body=%s", string(body))
		})
	}
}
