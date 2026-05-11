package calendar

import (
	"net/http"
	"net/url"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/nodate-flow/nodate-flow/apps/flow-api/tests/helpers"
)

// Tests in this file pin the input contract for GET /me/calendar-events.
//
// History: an over-eager `pattern:"^20\\d{2}-(0[1-9]|1[0-2])-(0[1-9]|1\\d|2[0-8])$"`
// regex on the start/end query fields rejected day 29-31 and rejected
// RFC 3339 datetimes despite the doc string promising both forms. The
// regression silently 422'd the calendar grid for any month with >28
// days. These tests fail fast at the handler layer if the regex (or any
// equivalent over-narrow validator) is reintroduced, so the next
// mistake doesn't ride to a multi-minute Playwright run.
//
// The handler under test is calendars.ListMyCalendarEvents
// (apps/flow-api/internal/http/handlers/calendars/me.go) which delegates
// parsing to parseFlexibleTime in events.go.

// seedEventForMe creates a personal calendar (which auto-subscribes the
// owner) and posts a single event into it. Returns the event start as
// the "expected to be visible" anchor for range assertions.
func seedEventForMe(t *testing.T, tt *helpers.CalendarTestTenant, start time.Time) string {
	t.Helper()

	calID := createCalendar(t, tt)

	body := map[string]any{
		"kind":     "event",
		"title":    "Me Range " + t.Name(),
		"startAt":  start.Unix(),
		"endAt":    start.Add(time.Hour).Unix(),
		"timezone": "UTC",
	}
	var resp struct {
		ID string `json:"id"`
	}
	helpers.DoJSON(t, http.MethodPost, tt.WsPath("calendars", calID, "events"), tt.AccessToken, body, &resp)
	require.NotEmpty(t, resp.ID, "seed event create returned empty id")
	return resp.ID
}

// meCalendarEventsURL builds the cross-workspace /me/calendar-events
// URL with start/end query params already escaped. We escape the values
// so RFC 3339 strings (which contain ':' and '+') survive the round
// trip without coincidentally tripping a different validator.
func meCalendarEventsURL(baseURL, start, end string) string {
	q := url.Values{}
	q.Set("start", start)
	q.Set("end", end)
	return baseURL + "/me/calendar-events?" + q.Encode()
}

// TestListMyCalendarEvents_RFC3339Range exercises the exact shape the
// flow-web client sends today: `start.toISOString()` from the calendar
// route. A regression here means real users see no events.
func TestListMyCalendarEvents_RFC3339Range(t *testing.T) {
	bootstrap(t)
	t.Parallel()

	tt := newTenant(t)
	eventStart := time.Date(2026, 4, 27, 10, 0, 0, 0, time.UTC)
	eventID := seedEventForMe(t, tt, eventStart)

	rangeStart := "2026-04-27T00:00:00.000Z"
	rangeEnd := "2026-05-04T00:00:00.000Z"

	var resp struct {
		Events []struct {
			ID      string `json:"id"`
			Title   string `json:"title"`
			StartAt *int64 `json:"startAt"`
		} `json:"events"`
	}
	helpers.DoJSON(t, http.MethodGet,
		meCalendarEventsURL(tt.BaseURL, rangeStart, rangeEnd),
		tt.AccessToken, nil, &resp)

	found := false
	for _, e := range resp.Events {
		if e.ID == eventID {
			found = true
			require.NotNil(t, e.StartAt, "seeded event missing startAt")
			assert.Equal(t, eventStart.Unix(), *e.StartAt)
			break
		}
	}
	assert.True(t, found, "seeded event must surface in /me/calendar-events for RFC 3339 range; got %d events", len(resp.Events))
}

// TestListMyCalendarEvents_YYYYMMDDRange covers the second documented
// input form. parseFlexibleTime tries RFC 3339 first then YYYY-MM-DD.
func TestListMyCalendarEvents_YYYYMMDDRange(t *testing.T) {
	bootstrap(t)
	t.Parallel()

	tt := newTenant(t)
	eventStart := time.Date(2026, 4, 27, 10, 0, 0, 0, time.UTC)
	eventID := seedEventForMe(t, tt, eventStart)

	var resp struct {
		Events []struct {
			ID string `json:"id"`
		} `json:"events"`
	}
	helpers.DoJSON(t, http.MethodGet,
		meCalendarEventsURL(tt.BaseURL, "2026-04-26", "2026-05-04"),
		tt.AccessToken, nil, &resp)

	found := false
	for _, e := range resp.Events {
		if e.ID == eventID {
			found = true
			break
		}
	}
	assert.True(t, found, "seeded event must surface for YYYY-MM-DD range; got %d events", len(resp.Events))
}

// TestListMyCalendarEvents_MonthEndDayBoundary is the direct regression
// guard for the rejected-regex bug. Days 29-31 must round-trip through
// the input validator. The regex used to be
// `^20\d{2}-(0[1-9]|1[0-2])-(0[1-9]|1\d|2[0-8])$` which rejected `31`.
func TestListMyCalendarEvents_MonthEndDayBoundary(t *testing.T) {
	bootstrap(t)
	t.Parallel()

	tt := newTenant(t)

	// No event seed needed: the test pins the validator, not the data.
	// An empty list with status 200 is a passing response; what we are
	// asserting is "the request was accepted at all".
	cases := []struct {
		name       string
		start, end string
	}{
		{"day_31_end", "2026-05-01", "2026-05-31"}, // the regex's headline reject
		{"day_30_end", "2026-04-01", "2026-04-30"},
		{"day_29_start", "2026-04-29", "2026-05-02"}, // start at day 29 must also pass
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			status, body := helpers.DoJSONStatus(t, http.MethodGet,
				meCalendarEventsURL(tt.BaseURL, tc.start, tc.end),
				tt.AccessToken, nil)
			assert.Equal(t, http.StatusOK, status,
				"day boundary %s..%s must be accepted; body=%s", tc.start, tc.end, string(body))
		})
	}
}

// TestListMyCalendarEvents_UnparseableStartReturnsApiError pins the
// negative path: junk input must surface as
// CALENDAR.EVENT.DATE_RANGE_UNPARSEABLE (status 422), not as a generic
// 500 or a leaked sentinel string.
func TestListMyCalendarEvents_UnparseableStartReturnsApiError(t *testing.T) {
	bootstrap(t)
	t.Parallel()

	tt := newTenant(t)

	status, body := helpers.DoJSONStatus(t, http.MethodGet,
		meCalendarEventsURL(tt.BaseURL, "garbage", "2026-05-01"),
		tt.AccessToken, nil)

	assert.Equal(t, http.StatusUnprocessableEntity, status, "body=%s", string(body))
	assert.Contains(t, string(body), "CALENDAR.EVENT.DATE_RANGE_UNPARSEABLE",
		"unparseable start must surface CALENDAR.EVENT.DATE_RANGE_UNPARSEABLE; body=%s", string(body))
}
