package calendars_test

import (
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/libraz/nodate-flow/apps/flow-api/tests/helpers"
	"github.com/libraz/nodate-flow/packages/go-shared/testhelpers"
)

type restEvent struct {
	ID       string  `json:"id"`
	StartAt  *int64  `json:"startAt"`
	EndAt    *int64  `json:"endAt"`
	Location *string `json:"location"`
}

type restFixture struct {
	t     *testing.T
	owner *helpers.CalendarTestTenant
	calID string
	srv   *helpers.TestServer
}

func newRestFixture(t *testing.T) *restFixture {
	t.Helper()
	testhelpers.SkipUnlessIntegration(t)
	inst := helpers.StartShared(t)
	helpers.RegisterCleanupDB(inst.DB)
	srv := helpers.StartTestServer(t, inst.DB)
	owner := helpers.CreateCalendarTestTenant(t, srv)
	var cal struct {
		ID string `json:"id"`
	}
	helpers.DoJSON(t, http.MethodPost, owner.WsPath("calendars"), owner.AccessToken,
		map[string]any{"kind": "personal", "name": "Input calendar", "color": "#4285F4"}, &cal)
	return &restFixture{t: t, owner: owner, calID: cal.ID, srv: srv}
}

func (f *restFixture) eventsPath(rest ...string) string {
	return f.owner.WsPath(append([]string{"calendars", f.calID, "events"}, rest...)...)
}

func problemType(t *testing.T, raw []byte) string {
	t.Helper()
	var p struct {
		Type string `json:"type"`
	}
	require.NoError(t, json.Unmarshal(raw, &p), "decode problem body: %s", string(raw))
	return p.Type
}

func (f *restFixture) create(body map[string]any) (int, []byte) {
	f.t.Helper()
	return helpers.DoJSONStatus(f.t, http.MethodPost, f.eventsPath(), f.owner.AccessToken, body)
}

func TestRESTAllDayWritesFollowTheCanonicalForm(t *testing.T) {
	f := newRestFixture(t)
	day := time.Date(2027, 8, 5, 0, 0, 0, 0, time.UTC)

	t.Run("a one-day event stores end = start + 1 day", func(t *testing.T) {
		status, raw := f.create(map[string]any{
			"kind": "event", "title": "Holiday", "allDay": true, "timezone": "Asia/Tokyo",
			"startDate": "2027-08-05", "endDate": "2027-08-06",
		})
		require.Less(t, status, 300, string(raw))
		var evt restEvent
		require.NoError(t, json.Unmarshal(raw, &evt))
		require.NotNil(t, evt.StartAt)
		require.NotNil(t, evt.EndAt)
		assert.Equal(t, day.Unix(), *evt.StartAt)
		assert.Equal(t, day.AddDate(0, 0, 1).Unix(), *evt.EndAt)
	})

	t.Run("a milestone ends where it starts", func(t *testing.T) {
		status, raw := f.create(map[string]any{
			"kind": "milestone", "title": "Launch", "allDay": true, "timezone": "UTC",
			"startDate": "2027-08-05", "endDate": "2027-08-05",
		})
		require.Less(t, status, 300, string(raw))
		var evt restEvent
		require.NoError(t, json.Unmarshal(raw, &evt))
		assert.Equal(t, *evt.StartAt, *evt.EndAt)
	})

	t.Run("instants with allDay are the wrong shape", func(t *testing.T) {
		status, raw := f.create(map[string]any{
			"kind": "event", "title": "Holiday", "allDay": true, "timezone": "UTC",
			"startAt": day.Unix(), "endAt": day.AddDate(0, 0, 1).Unix(),
		})
		assert.Equal(t, http.StatusUnprocessableEntity, status, string(raw))
		assert.Equal(t, "CALENDAR.EVENT.ALL_DAY_SHAPE", problemType(t, raw))
	})

	t.Run("dates without allDay are the wrong shape", func(t *testing.T) {
		status, raw := f.create(map[string]any{
			"kind": "event", "title": "Timed", "timezone": "UTC",
			"startDate": "2027-08-05", "endDate": "2027-08-06",
		})
		assert.Equal(t, http.StatusUnprocessableEntity, status, string(raw))
		assert.Equal(t, "CALENDAR.EVENT.ALL_DAY_SHAPE", problemType(t, raw))
	})

	t.Run("an empty or reversed range is refused", func(t *testing.T) {
		for name, end := range map[string]string{"empty": "2027-08-05", "reversed": "2027-08-03"} {
			status, raw := f.create(map[string]any{
				"kind": "event", "title": name, "allDay": true, "timezone": "UTC",
				"startDate": "2027-08-05", "endDate": end,
			})
			assert.Equal(t, http.StatusUnprocessableEntity, status, "%s: %s", name, string(raw))
			assert.Equal(t, "CALENDAR.EVENT.DATE_RANGE_INVALID", problemType(t, raw), name)
		}
	})

	t.Run("a patch sends the one end that moves", func(t *testing.T) {
		status, raw := f.create(map[string]any{
			"kind": "event", "title": "Trip", "allDay": true, "timezone": "UTC",
			"startDate": "2027-08-05", "endDate": "2027-08-07",
		})
		require.Less(t, status, 300, string(raw))
		var evt restEvent
		require.NoError(t, json.Unmarshal(raw, &evt))

		status, raw = helpers.DoJSONStatus(t, http.MethodPatch, f.eventsPath(evt.ID), f.owner.AccessToken,
			map[string]any{"endDate": "2027-08-09"})
		require.Less(t, status, 300, string(raw))
		require.NoError(t, json.Unmarshal(raw, &evt))
		assert.Equal(t, day.Unix(), *evt.StartAt, "the start stays as stored")
		assert.Equal(t, day.AddDate(0, 0, 4).Unix(), *evt.EndAt)

		status, raw = helpers.DoJSONStatus(t, http.MethodPatch, f.eventsPath(evt.ID), f.owner.AccessToken,
			map[string]any{"endDate": "2027-08-05"})
		assert.Equal(t, http.StatusUnprocessableEntity, status, string(raw))
		assert.Equal(t, "CALENDAR.EVENT.DATE_RANGE_INVALID", problemType(t, raw))
	})

	t.Run("switching to all-day needs the dates", func(t *testing.T) {
		start := time.Date(2027, 8, 5, 22, 0, 0, 0, time.UTC)
		status, raw := f.create(map[string]any{
			"kind": "event", "title": "Timed", "timezone": "UTC",
			"startAt": start.Unix(), "endAt": start.Add(time.Hour).Unix(),
		})
		require.Less(t, status, 300, string(raw))
		var evt restEvent
		require.NoError(t, json.Unmarshal(raw, &evt))

		status, raw = helpers.DoJSONStatus(t, http.MethodPatch, f.eventsPath(evt.ID), f.owner.AccessToken,
			map[string]any{"allDay": true})
		assert.Equal(t, http.StatusUnprocessableEntity, status, string(raw))
		assert.Equal(t, "CALENDAR.EVENT.ALL_DAY_SHAPE", problemType(t, raw))

		status, raw = helpers.DoJSONStatus(t, http.MethodPatch, f.eventsPath(evt.ID), f.owner.AccessToken,
			map[string]any{"allDay": true, "startDate": "2027-08-05", "endDate": "2027-08-06"})
		require.Less(t, status, 300, string(raw))
		require.NoError(t, json.Unmarshal(raw, &evt))
		assert.Equal(t, day.Unix(), *evt.StartAt)
		assert.Equal(t, day.AddDate(0, 0, 1).Unix(), *evt.EndAt)
	})
}

func TestRESTNotificationOffsetIsBounded(t *testing.T) {
	f := newRestFixture(t)
	start := time.Date(2027, 8, 5, 10, 0, 0, 0, time.UTC)
	body := func(offset int) map[string]any {
		return map[string]any{
			"kind": "event", "title": "Reminded", "timezone": "UTC",
			"startAt": start.Unix(), "endAt": start.Add(time.Hour).Unix(),
			"notificationOffset": offset,
		}
	}

	for _, bad := range []int{-1, 10081} {
		status, raw := f.create(body(bad))
		assert.Equal(t, http.StatusUnprocessableEntity, status, "%d: %s", bad, string(raw))
		assert.Equal(t, "CALENDAR.EVENT.NOTIFICATION_OFFSET_OUT_OF_RANGE", problemType(t, raw), "%d", bad)
	}
	status, raw := f.create(body(10080))
	require.Less(t, status, 300, string(raw))
	var evt restEvent
	require.NoError(t, json.Unmarshal(raw, &evt))

	status, raw = helpers.DoJSONStatus(t, http.MethodPatch, f.eventsPath(evt.ID), f.owner.AccessToken,
		map[string]any{"notificationOffset": 10081})
	assert.Equal(t, http.StatusUnprocessableEntity, status, string(raw))
	assert.Equal(t, "CALENDAR.EVENT.NOTIFICATION_OFFSET_OUT_OF_RANGE", problemType(t, raw))
}

func TestRESTAllDayToggleRefusedWhileOccurrencesAreNamed(t *testing.T) {
	f := newRestFixture(t)
	first := time.Date(2027, 3, 1, 10, 0, 0, 0, time.UTC)
	series := func(title string) restEvent {
		status, raw := f.create(map[string]any{
			"kind": "event", "title": title, "timezone": "UTC",
			"startAt": first.Unix(), "endAt": first.Add(time.Hour).Unix(),
			"recurrenceRule": map[string]any{"freq": "weekly", "interval": 1},
		})
		require.Less(t, status, 300, string(raw))
		var evt restEvent
		require.NoError(t, json.Unmarshal(raw, &evt))
		return evt
	}
	toggle := func(id string) (int, []byte) {
		return helpers.DoJSONStatus(t, http.MethodPatch, f.eventsPath(id), f.owner.AccessToken,
			map[string]any{"allDay": true, "startDate": "2027-03-01", "endDate": "2027-03-02"})
	}

	t.Run("an exception", func(t *testing.T) {
		evt := series("With exception")
		helpers.DoJSON(t, http.MethodPatch, f.eventsPath(evt.ID), f.owner.AccessToken,
			map[string]any{"recurrenceExceptions": []string{first.AddDate(0, 0, 7).Format(time.RFC3339)}}, nil)
		status, raw := toggle(evt.ID)
		assert.Equal(t, http.StatusUnprocessableEntity, status, string(raw))
		assert.Equal(t, "CALENDAR.EVENT.ALL_DAY_TOGGLE_WITH_EXCEPTIONS", problemType(t, raw))
	})

	t.Run("an override row", func(t *testing.T) {
		evt := series("With override")
		helpers.DoJSON(t, http.MethodPatch, f.eventsPath(evt.ID), f.owner.AccessToken, map[string]any{
			"scope": "occurrence", "occurrenceStart": first.AddDate(0, 0, 14).Unix(), "title": "Moved",
		}, nil)
		status, raw := toggle(evt.ID)
		assert.Equal(t, http.StatusUnprocessableEntity, status, string(raw))
		assert.Equal(t, "CALENDAR.EVENT.ALL_DAY_TOGGLE_WITH_EXCEPTIONS", problemType(t, raw))
	})

	t.Run("a clean series switches", func(t *testing.T) {
		evt := series("Clean")
		status, raw := toggle(evt.ID)
		assert.Less(t, status, 300, string(raw))
	})
}

// A guest who is attendee of a series but was taken off one occurrence is
// not an attendee of that occurrence: the override's own rows replace the
// series'. A creator filing an event under another owner is a party to it.
func TestRESTAttendanceFollowsTheReplaceRule(t *testing.T) {
	f := newRestFixture(t)
	calInternalID := helpers.ResolveCalendarInternalID(t, f.srv.DB, f.calID)
	guest := helpers.CreateExtraCalendarMember(t, f.srv, f.owner, calInternalID, "editor")
	manager := helpers.CreateExtraCalendarMember(t, f.srv, f.owner, calInternalID, "manager")

	readLocation := func(who *helpers.CalendarTestTenant, id string) *string {
		var evt restEvent
		helpers.DoJSON(t, http.MethodGet, f.eventsPath(id), who.AccessToken, nil, &evt)
		return evt.Location
	}

	t.Run("an override with its own rows replaces the series", func(t *testing.T) {
		first := time.Date(2027, 3, 1, 10, 0, 0, 0, time.UTC)
		status, raw := f.create(map[string]any{
			"kind": "event", "title": "Private series", "visibility": "private", "location": "Room 5F",
			"timezone": "UTC", "startAt": first.Unix(), "endAt": first.Add(time.Hour).Unix(),
			"recurrenceRule": map[string]any{"freq": "weekly", "interval": 1},
		})
		require.Less(t, status, 300, string(raw))
		var series restEvent
		require.NoError(t, json.Unmarshal(raw, &series))
		helpers.DoJSON(t, http.MethodPost, f.eventsPath(series.ID, "attendees"), f.owner.AccessToken,
			map[string]any{"userIds": []string{guest.UserPublicID.String()}}, nil)

		var override restEvent
		helpers.DoJSON(t, http.MethodPatch, f.eventsPath(series.ID), f.owner.AccessToken, map[string]any{
			"scope": "occurrence", "occurrenceStart": first.AddDate(0, 0, 7).Unix(), "title": "Moved",
		}, &override)

		require.NotNil(t, readLocation(guest, override.ID), "with no rows of its own the override reads the series' list")

		helpers.DoJSON(t, http.MethodPost, f.eventsPath(override.ID, "attendees"), f.owner.AccessToken,
			map[string]any{"userIds": []string{manager.UserPublicID.String()}}, nil)
		helpers.DoJSON(t, http.MethodDelete, f.eventsPath(override.ID, "attendees", guest.UserPublicID.String()),
			f.owner.AccessToken, nil, nil)

		assert.Nil(t, readLocation(guest, override.ID), "the guest is off this occurrence's own list")
		assert.NotNil(t, readLocation(guest, series.ID), "and still on the series")
	})

	t.Run("the creator is a party beside the owner", func(t *testing.T) {
		start := time.Date(2027, 4, 1, 10, 0, 0, 0, time.UTC)
		var evt restEvent
		helpers.DoJSON(t, http.MethodPost, f.eventsPath(), manager.AccessToken, map[string]any{
			"kind": "event", "title": "Filed for the owner", "visibility": "private", "location": "Room 9",
			"timezone": "UTC", "startAt": start.Unix(), "endAt": start.Add(time.Hour).Unix(),
			"ownerUserId": f.owner.UserPublicID.String(),
		}, &evt)
		require.NotNil(t, readLocation(manager, evt.ID), "the creator reads what they filed")
		assert.Nil(t, readLocation(guest, evt.ID), "another member does not")
	})
}
