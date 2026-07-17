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

// Tests in this file pin the private-event field-leak guard: a private
// event's free-text fields (location, and on the per-calendar shape also
// memo/url) must never surface to a workspace co-member who is not the
// owner, across every list/feed read path. Event-level visibility is the
// real ACL; workspace membership only gates editing. The shared scrub
// helper (scrubPrivateEvent in
// apps/flow-api/internal/http/handlers/calendars/mapper.go) is the single
// source of truth; these tests exercise all three list endpoints that
// funnel through it.

// withRange appends start/end query params, escaping the values so
// RFC 3339 strings survive the round trip untouched.
func withRange(base, start, end string) string {
	q := url.Values{}
	q.Set("start", start)
	q.Set("end", end)
	return base + "?" + q.Encode()
}

// TestPrivateEventFieldLeak_HiddenFromCoMemberAcrossListEndpoints seeds a
// private event owned by the calendar owner, then lists it as a second
// workspace member subscribed to the same calendar. The event must be
// visible (not filtered out) but its location must be scrubbed on all
// three list paths; the per-calendar shape additionally scrubs memo/url.
// The owner's own read still carries every field.
func TestPrivateEventFieldLeak_HiddenFromCoMemberAcrossListEndpoints(t *testing.T) {
	bootstrap(t)
	t.Parallel()

	owner := newTenant(t)
	calID := createCalendar(t, owner)

	calInternalID := helpers.ResolveCalendarInternalID(t, testDB, calID)
	member := helpers.CreateExtraCalendarMember(t, testSrv, owner.WorkspaceID, owner.WorkspacePublicID, calInternalID, "")

	eventStart := time.Date(2026, 8, 3, 10, 0, 0, 0, time.UTC)
	const (
		secretLocation = "Room 5F, Building A"
		secretMemo     = "1:1 salary review notes"
		secretURL      = "https://example.test/private-doc"
	)

	var evt struct {
		ID string `json:"id"`
	}
	helpers.DoJSON(t, http.MethodPost, owner.WsPath("calendars", calID, "events"), owner.AccessToken, map[string]any{
		"kind":       "event",
		"visibility": "private",
		"title":      "Private 1:1",
		"startAt":    eventStart.Unix(),
		"endAt":      eventStart.Add(time.Hour).Unix(),
		"timezone":   "UTC",
		"location":   secretLocation,
		"memo":       secretMemo,
		"url":        secretURL,
	}, &evt)
	require.NotEmpty(t, evt.ID)

	rangeStart := "2026-08-01T00:00:00.000Z"
	rangeEnd := "2026-08-08T00:00:00.000Z"

	// eventDTO covers the union of fields the three response shapes
	// project. Cross-calendar / cross-workspace shapes omit memo/url, so
	// those pointers simply stay nil there.
	type eventDTO struct {
		ID         string  `json:"id"`
		Visibility string  `json:"visibility"`
		Location   *string `json:"location"`
		Memo       *string `json:"memo"`
		URL        *string `json:"url"`
	}
	findEvent := func(t *testing.T, events []eventDTO) eventDTO {
		t.Helper()
		for _, e := range events {
			if e.ID == evt.ID {
				return e
			}
		}
		t.Fatalf("seeded private event %s not present in list response", evt.ID)
		return eventDTO{}
	}

	// Owner sees every field on the per-calendar list (sanity: the fields
	// were actually stored, so a null below is real scrubbing not an
	// empty column).
	t.Run("owner sees full fields", func(t *testing.T) {
		var resp struct {
			Events []eventDTO `json:"events"`
		}
		helpers.DoJSON(t, http.MethodGet,
			withRange(owner.WsPath("calendars", calID, "events"), rangeStart, rangeEnd),
			owner.AccessToken, nil, &resp)
		got := findEvent(t, resp.Events)
		require.NotNil(t, got.Location)
		assert.Equal(t, secretLocation, *got.Location)
		require.NotNil(t, got.Memo)
		assert.Equal(t, secretMemo, *got.Memo)
		require.NotNil(t, got.URL)
		assert.Equal(t, secretURL, *got.URL)
	})

	// Per-calendar list: GET /workspaces/{wsId}/calendars/{calId}/events
	t.Run("co-member per-calendar list scrubs location/memo/url", func(t *testing.T) {
		var resp struct {
			Events []eventDTO `json:"events"`
		}
		helpers.DoJSON(t, http.MethodGet,
			withRange(member.WsPath("calendars", calID, "events"), rangeStart, rangeEnd),
			member.AccessToken, nil, &resp)
		got := findEvent(t, resp.Events)
		assert.Equal(t, "private", got.Visibility)
		assert.Nil(t, got.Location, "location must be scrubbed for co-member on per-calendar list")
		assert.Nil(t, got.Memo, "memo must be scrubbed for co-member on per-calendar list")
		assert.Nil(t, got.URL, "url must be scrubbed for co-member on per-calendar list")
	})

	// Cross-calendar feed: GET /workspaces/{wsId}/calendar-events
	t.Run("co-member cross-calendar feed scrubs location", func(t *testing.T) {
		var resp struct {
			Events []eventDTO `json:"events"`
		}
		helpers.DoJSON(t, http.MethodGet,
			withRange(member.WsPath("calendar-events"), rangeStart, rangeEnd),
			member.AccessToken, nil, &resp)
		got := findEvent(t, resp.Events)
		assert.Nil(t, got.Location, "location must be scrubbed for co-member on cross-calendar feed")
	})

	// Cross-workspace feed: GET /me/calendar-events
	t.Run("co-member cross-workspace feed scrubs location", func(t *testing.T) {
		var resp struct {
			Events []eventDTO `json:"events"`
		}
		helpers.DoJSON(t, http.MethodGet,
			withRange(member.BaseURL+"/me/calendar-events", rangeStart, rangeEnd),
			member.AccessToken, nil, &resp)
		got := findEvent(t, resp.Events)
		assert.Nil(t, got.Location, "location must be scrubbed for co-member on cross-workspace feed")
	})
}
