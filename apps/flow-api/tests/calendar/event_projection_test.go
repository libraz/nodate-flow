// What the read paths project: whether every list surface returns the
// same fields for the same row, in the same wire shape, and whether the
// range predicate admits an event that has no duration.
package calendar

import (
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/libraz/nodate-flow/apps/flow-api/tests/helpers"
)

// TestFlexibilityIsProjectedOnEveryEventSurface reads one event through
// each list endpoint and requires the same flexibility on all of them.
//
// The cross-calendar feed declared the field and never assigned it, so
// it answered "" while the per-calendar list, the cross-workspace feed
// and the single-event read all answered "negotiable". A UI reading the
// feed showed every commitment as unmovable — an empty string is not
// obviously a missing value, so nothing looked broken.
//
// The event is found by its own id rather than by position, because the
// workspace-scoped feeds carry whatever else the tenant has.
func TestFlexibilityIsProjectedOnEveryEventSurface(t *testing.T) {
	bootstrap(t)
	t.Parallel()

	tt := newTenant(t)
	calID := createCalendar(t, tt)

	start := time.Date(2026, 9, 3, 10, 0, 0, 0, time.UTC)
	var created struct {
		ID          string `json:"id"`
		Flexibility string `json:"flexibility"`
	}
	helpers.DoJSON(t, http.MethodPost,
		tt.WsPath("calendars", calID, "events"), tt.AccessToken, map[string]any{
			"kind":        "event",
			"title":       "Movable review",
			"startAt":     start.Unix(),
			"endAt":       start.Add(time.Hour).Unix(),
			"timezone":    "UTC",
			"flexibility": "negotiable",
		}, &created)
	require.NotEmpty(t, created.ID)
	require.Equal(t, "negotiable", created.Flexibility, "create response must echo the stored value")

	rangeStart := "2026-09-01"
	rangeEnd := "2026-09-30"

	surfaces := map[string]string{
		"per-calendar list": withRange(tt.WsPath("calendars", calID, "events"), rangeStart, rangeEnd),
		"cross-calendar feed": withRange(
			tt.WsPath("calendar-events"), rangeStart, rangeEnd),
		"cross-workspace feed": withRange(
			tt.BaseURL+"/me/calendar-events", rangeStart, rangeEnd),
	}
	for name, url := range surfaces {
		t.Run(name, func(t *testing.T) {
			var resp struct {
				Events []struct {
					ID          string `json:"id"`
					Flexibility string `json:"flexibility"`
				} `json:"events"`
			}
			helpers.DoJSON(t, http.MethodGet, url, tt.AccessToken, nil, &resp)

			found := false
			for _, e := range resp.Events {
				if e.ID != created.ID {
					continue
				}
				found = true
				assert.Equal(t, "negotiable", e.Flexibility,
					"%s must project the stored flexibility, not the zero value", name)
			}
			require.True(t, found, "%s did not return the event under test", name)
		})
	}

	var got struct {
		Flexibility string `json:"flexibility"`
	}
	helpers.DoJSON(t, http.MethodGet,
		tt.WsPath("calendars", calID, "events", created.ID), tt.AccessToken, nil, &got)
	assert.Equal(t, "negotiable", got.Flexibility)
}

// TestZeroLengthEventIsVisibleAtTheRangeStart pins the boundary the
// month grid actually queries.
//
// A milestone is stored with end_at equal to start_at. The overlap
// predicate was start_at < range_end AND end_at > range_start, so a
// milestone sitting exactly on the first instant of the range failed the
// second half by an equality — and the grid's range start is local
// midnight, which is precisely where an all-day milestone lands. The
// result was one month view, the one containing it, where the milestone
// did not appear.
//
// Each case names the instant it queries from rather than a window
// around it, because an off-by-one is only visible at the boundary: a
// range starting a second earlier passes against the broken predicate
// too.
func TestZeroLengthEventIsVisibleAtTheRangeStart(t *testing.T) {
	bootstrap(t)
	t.Parallel()

	tt := newTenant(t)
	calID := createCalendar(t, tt)

	at := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
	var milestone struct {
		ID string `json:"id"`
	}
	helpers.DoJSON(t, http.MethodPost,
		tt.WsPath("calendars", calID, "events"), tt.AccessToken, map[string]any{
			"kind":     "milestone",
			"title":    "Boundary milestone",
			"startAt":  at.Unix(),
			"endAt":    at.Unix(),
			"timezone": "UTC",
		}, &milestone)
	require.NotEmpty(t, milestone.ID)

	rangeStart := at.Format(time.RFC3339)
	rangeEnd := at.AddDate(0, 1, 0).Format(time.RFC3339)

	surfaces := map[string]string{
		"per-calendar list":    withRange(tt.WsPath("calendars", calID, "events"), rangeStart, rangeEnd),
		"cross-calendar feed":  withRange(tt.WsPath("calendar-events"), rangeStart, rangeEnd),
		"cross-workspace feed": withRange(tt.BaseURL+"/me/calendar-events", rangeStart, rangeEnd),
	}
	for name, url := range surfaces {
		t.Run(name, func(t *testing.T) {
			assert.Truef(t, listContainsEvent(t, url, tt.AccessToken, milestone.ID),
				"%s dropped a zero-length event lying on the first instant of the range", name)
		})
	}

	// The other side of the boundary still has to hold: an event that
	// ends exactly where the range starts belongs to the window before
	// it, so a fix that simply relaxed the comparison to >= would pull a
	// finished meeting into the next month.
	before := at.Add(-time.Hour)
	var earlier struct {
		ID string `json:"id"`
	}
	helpers.DoJSON(t, http.MethodPost,
		tt.WsPath("calendars", calID, "events"), tt.AccessToken, map[string]any{
			"kind":     "event",
			"title":    "Ends at the boundary",
			"startAt":  before.Unix(),
			"endAt":    at.Unix(),
			"timezone": "UTC",
		}, &earlier)
	require.NotEmpty(t, earlier.ID)

	assert.False(t,
		listContainsEvent(t, withRange(tt.WsPath("calendars", calID, "events"), rangeStart, rangeEnd),
			tt.AccessToken, earlier.ID),
		"an event ending exactly at the range start is not in this range")
}

// listContainsEvent reports whether a list endpoint returned the given
// event id. Asserting on presence of one id rather than on a count is
// what keeps these tests correct while the suite runs in parallel
// against a shared database.
func listContainsEvent(t *testing.T, url, bearer, eventID string) bool {
	t.Helper()
	var resp struct {
		Events []struct {
			ID string `json:"id"`
		} `json:"events"`
	}
	helpers.DoJSON(t, http.MethodGet, url, bearer, nil, &resp)
	for _, e := range resp.Events {
		if e.ID == eventID {
			return true
		}
	}
	return false
}

// TestRecurrenceRuleHasOneWireShape reads the same recurring event
// through the authenticated API and through the unauthenticated public
// share page, and requires the two to agree on what recurrenceRule is.
//
// They did not. The authenticated surfaces returned the decoded object;
// the share render returned the stored JSON as a string holding its own
// encoding. Two shapes for one field produced two client-side parsers,
// which then drifted, so the same series expanded differently depending
// on which page you were on.
//
// The assertion decodes both into the same Go value: a string and an
// object do not compare equal that way, so the test fails on the shape
// and not merely on the bytes.
func TestRecurrenceRuleHasOneWireShape(t *testing.T) {
	bootstrap(t)
	t.Parallel()

	tt := newTenant(t)
	calID := createCalendar(t, tt)

	start := time.Date(2026, 11, 2, 9, 0, 0, 0, time.UTC)
	var created struct {
		ID string `json:"id"`
	}
	helpers.DoJSON(t, http.MethodPost,
		tt.WsPath("calendars", calID, "events"), tt.AccessToken, map[string]any{
			"kind":     "event",
			"title":    "Weekly sync",
			"startAt":  start.Unix(),
			"endAt":    start.Add(time.Hour).Unix(),
			"timezone": "UTC",
			"recurrenceRule": map[string]any{
				"freq":     "weekly",
				"interval": 2,
				"byDay":    []string{"MO"},
			},
		}, &created)
	require.NotEmpty(t, created.ID)

	var share struct {
		ID    string `json:"id"`
		Token string `json:"token"`
	}
	helpers.DoJSON(t, http.MethodPost, tt.WsPath("public-shares"), tt.AccessToken, map[string]any{
		"title": "Sync schedule",
	}, &share)

	var attach struct {
		Attached int `json:"attached"`
	}
	helpers.DoJSON(t, http.MethodPost, tt.WsPath("public-shares", share.ID, "events"),
		tt.AccessToken, map[string]any{"eventIds": []string{created.ID}}, &attach)
	require.Equal(t, 1, attach.Attached)

	var authed struct {
		RecurrenceRule json.RawMessage `json:"recurrenceRule"`
	}
	helpers.DoJSON(t, http.MethodGet,
		tt.WsPath("calendars", calID, "events", created.ID), tt.AccessToken, nil, &authed)
	require.NotEmpty(t, authed.RecurrenceRule, "the authenticated read must carry the rule")

	var rendered struct {
		Events []struct {
			ID             string          `json:"id"`
			RecurrenceRule json.RawMessage `json:"recurrenceRule"`
		} `json:"events"`
	}
	helpers.DoJSON(t, http.MethodGet, tt.BaseURL+"/share/cal/"+share.Token, "", nil, &rendered)

	var public json.RawMessage
	for _, e := range rendered.Events {
		if e.ID == created.ID {
			public = e.RecurrenceRule
		}
	}
	require.NotEmpty(t, public, "the share render must carry the rule for the attached event")

	// Both must decode as an object with the same members. A string
	// carrying the JSON fails here, which is the shape that shipped.
	var fromAuthed, fromPublic map[string]any
	require.NoErrorf(t, json.Unmarshal(authed.RecurrenceRule, &fromAuthed),
		"authenticated recurrenceRule is not an object: %s", string(authed.RecurrenceRule))
	require.NoErrorf(t, json.Unmarshal(public, &fromPublic),
		"public recurrenceRule is not an object: %s", string(public))
	assert.Equal(t, fromAuthed, fromPublic,
		"the same event must describe its recurrence identically on both faces")
	assert.Equal(t, "weekly", fromPublic["freq"])
}
