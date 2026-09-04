// Input validation on the calendar write paths: what a bad timezone or
// an inverted time range does to the caller.
//
// Every test here asserts the class of the answer, not only that the
// request failed. The defects these cover all produced a refusal of
// some kind — the event was never stored wrong — but the refusal was
// reported as the wrong kind of thing: a database write failure, or a
// success followed by an event that no client could draw. A test that
// only checked "not 2xx" would have passed against every one of them.
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

// problemType reads the RFC 9457 `type` member, which carries the
// machine-readable error code clients branch on.
func problemType(t *testing.T, raw []byte) string {
	t.Helper()
	var p struct {
		Type string `json:"type"`
	}
	require.NoError(t, json.Unmarshal(raw, &p), "decode problem body: %s", string(raw))
	return p.Type
}

// TestCreateEventRejectsUnresolvableTimezone covers the names callers
// actually send when they get this wrong. "JST" and "GMT+9" are not
// IANA identifiers, and time.LoadLocation resolves neither.
//
// Storing them succeeded before: the event came back from GET with the
// timezone intact, and then never appeared on a calendar, because every
// renderer resolves the zone first and an unresolvable one expands to
// no instances. A 201 for an event nobody can see is the worst
// available answer, so this asserts the refusal is a 422 and not a
// success.
func TestCreateEventRejectsUnresolvableTimezone(t *testing.T) {
	bootstrap(t)
	t.Parallel()

	tt := newTenant(t)
	calID := createCalendar(t, tt)
	start := time.Date(2026, 5, 4, 10, 0, 0, 0, time.UTC)

	for _, tz := range []string{"JST", "GMT+9", "Mars/Olympus", "Asia/Tokyo/Extra"} {
		status, raw := helpers.DoJSONStatus(t, http.MethodPost,
			tt.WsPath("calendars", calID, "events"), tt.AccessToken, map[string]any{
				"kind":     "event",
				"title":    "Bad zone " + tz,
				"startAt":  start.Unix(),
				"endAt":    start.Add(time.Hour).Unix(),
				"timezone": tz,
			})
		assert.Equalf(t, http.StatusUnprocessableEntity, status,
			"timezone %q must be refused as invalid input, got %d body=%s", tz, status, string(raw))
		assert.Equalf(t, "VALIDATION.BODY.FIELD_INVALID", problemType(t, raw),
			"timezone %q must be reported as a bad field", tz)
	}
}

// TestCreateEventAcceptsResolvableTimezone is the other half: the check
// has to let real zones through, including one whose offset is not a
// whole hour, so a validator that quietly narrowed to a hard-coded list
// would fail here.
func TestCreateEventAcceptsResolvableTimezone(t *testing.T) {
	bootstrap(t)
	t.Parallel()

	tt := newTenant(t)
	calID := createCalendar(t, tt)
	start := time.Date(2026, 5, 4, 10, 0, 0, 0, time.UTC)

	for _, tz := range []string{"Asia/Tokyo", "America/New_York", "Australia/Adelaide", "UTC"} {
		var resp struct {
			ID       string `json:"id"`
			Timezone string `json:"timezone"`
		}
		helpers.DoJSON(t, http.MethodPost,
			tt.WsPath("calendars", calID, "events"), tt.AccessToken, map[string]any{
				"kind":     "event",
				"title":    "Good zone " + tz,
				"startAt":  start.Unix(),
				"endAt":    start.Add(time.Hour).Unix(),
				"timezone": tz,
			}, &resp)
		require.NotEmpty(t, resp.ID)
		assert.Equal(t, tz, resp.Timezone)
	}
}

// TestPatchEventRejectsUnresolvableTimezone closes the second door.
// Create and patch are separate writes, and a check on one of them
// leaves the other able to turn a working event into an invisible one.
func TestPatchEventRejectsUnresolvableTimezone(t *testing.T) {
	bootstrap(t)
	t.Parallel()

	tt := newTenant(t)
	calID := createCalendar(t, tt)
	start := time.Date(2026, 5, 4, 10, 0, 0, 0, time.UTC)

	var created struct {
		ID string `json:"id"`
	}
	helpers.DoJSON(t, http.MethodPost,
		tt.WsPath("calendars", calID, "events"), tt.AccessToken, map[string]any{
			"kind":     "event",
			"title":    "Patch zone",
			"startAt":  start.Unix(),
			"endAt":    start.Add(time.Hour).Unix(),
			"timezone": "UTC",
		}, &created)
	require.NotEmpty(t, created.ID)

	status, raw := helpers.DoJSONStatus(t, http.MethodPatch,
		tt.WsPath("calendars", calID, "events", created.ID), tt.AccessToken, map[string]any{
			"timezone": "JST",
		})
	assert.Equal(t, http.StatusUnprocessableEntity, status, "body=%s", string(raw))
	assert.Equal(t, "VALIDATION.BODY.FIELD_INVALID", problemType(t, raw))

	// The stored value must be the one that was there before the
	// refused patch, not a half-applied write.
	var after struct {
		Timezone string `json:"timezone"`
	}
	helpers.DoJSON(t, http.MethodGet,
		tt.WsPath("calendars", calID, "events", created.ID), tt.AccessToken, nil, &after)
	assert.Equal(t, "UTC", after.Timezone)
}

// problemFieldLocations reads the RFC 9457 `errors` member, which names
// the request member each validation failure is about. A refusal that
// comes back without one is a refusal the caller cannot act on.
func problemFieldLocations(t *testing.T, raw []byte) []string {
	t.Helper()
	var p struct {
		Errors []struct {
			Location string `json:"location"`
		} `json:"errors"`
	}
	require.NoError(t, json.Unmarshal(raw, &p), "decode problem body: %s", string(raw))
	out := make([]string, 0, len(p.Errors))
	for _, e := range p.Errors {
		out = append(out, e.Location)
	}
	return out
}

// TestPatchEventRejectsUnknownEnumValues pins where an unrecognised enum
// value is refused, not merely that it is.
//
// The column is an ENUM and the connection runs in strict mode, so an
// unknown value never reached storage. What it did reach was the write:
// the refusal arrived as a failed INSERT reported as
// CALENDAR.EVENT.STORE_WRITE_INTERRUPTED, a 500 that tells the caller
// their input was fine and the server is broken, and it left the API
// contract silent about which values the field takes. Asserting the class
// is the whole test — a check for "not 2xx" passes on the database doing
// the refusing.
//
// visibility is the one with a consequence beyond the status code: the
// readers downstream branch on it, and a value none of them classifies
// decides how far the event is published.
func TestPatchEventRejectsUnknownEnumValues(t *testing.T) {
	bootstrap(t)
	t.Parallel()

	tt := newTenant(t)
	calID := createCalendar(t, tt)
	start := time.Date(2026, 5, 11, 10, 0, 0, 0, time.UTC)

	var created struct {
		ID string `json:"id"`
	}
	helpers.DoJSON(t, http.MethodPost,
		tt.WsPath("calendars", calID, "events"), tt.AccessToken, map[string]any{
			"kind":       "event",
			"visibility": "private",
			"showAs":     "busy",
			"title":      "Patch enum",
			"startAt":    start.Unix(),
			"endAt":      start.Add(time.Hour).Unix(),
			"timezone":   "UTC",
		}, &created)
	require.NotEmpty(t, created.ID)

	for _, c := range []struct {
		field string
		value string
	}{
		{"visibility", "internal"},
		{"kind", "appointment"},
		{"showAs", "away"},
	} {
		t.Run(c.field, func(t *testing.T) {
			status, raw := helpers.DoJSONStatus(t, http.MethodPatch,
				tt.WsPath("calendars", calID, "events", created.ID), tt.AccessToken, map[string]any{
					c.field: c.value,
				})
			assert.Equalf(t, http.StatusUnprocessableEntity, status,
				"%s=%q is bad input, not a storage failure: got %d body=%s",
				c.field, c.value, status, string(raw))
			assert.Containsf(t, problemFieldLocations(t, raw), "body."+c.field,
				"the refusal has to name the field the caller sent: body=%s", string(raw))
		})
	}

	// The stored row must be the one that was there before the refused
	// patches, which is what says none of them reached the write.
	var after struct {
		Kind       string `json:"kind"`
		Visibility string `json:"visibility"`
		ShowAs     string `json:"showAs"`
	}
	helpers.DoJSON(t, http.MethodGet,
		tt.WsPath("calendars", calID, "events", created.ID), tt.AccessToken, nil, &after)
	assert.Equal(t, "event", after.Kind)
	assert.Equal(t, "private", after.Visibility)
	assert.Equal(t, "busy", after.ShowAs)
}

// TestPatchEventAcceptsEveryEnumValue is the other half: a constraint
// implemented by refusing everything would pass the test above, and would
// take the four visibilities the product is built on with it.
func TestPatchEventAcceptsEveryEnumValue(t *testing.T) {
	bootstrap(t)
	t.Parallel()

	tt := newTenant(t)
	calID := createCalendar(t, tt)
	start := time.Date(2026, 5, 12, 10, 0, 0, 0, time.UTC)

	var created struct {
		ID string `json:"id"`
	}
	helpers.DoJSON(t, http.MethodPost,
		tt.WsPath("calendars", calID, "events"), tt.AccessToken, map[string]any{
			"kind":     "event",
			"title":    "Patch enum accepted",
			"startAt":  start.Unix(),
			"endAt":    start.Add(time.Hour).Unix(),
			"timezone": "UTC",
		}, &created)
	require.NotEmpty(t, created.ID)

	for _, c := range []struct {
		field  string
		values []string
	}{
		{"visibility", []string{"default", "public", "private", "confidential"}},
		{"kind", []string{"event", "block", "free", "milestone"}},
		{"showAs", []string{"busy", "free", "tentative", "oof"}},
	} {
		for _, value := range c.values {
			var patched map[string]any
			helpers.DoJSON(t, http.MethodPatch,
				tt.WsPath("calendars", calID, "events", created.ID), tt.AccessToken, map[string]any{
					c.field: value,
				}, &patched)
			assert.Equalf(t, value, patched[c.field],
				"%s=%q is a value of the column and must round-trip", c.field, value)
		}
	}
}

// TestCreateEventRejectsEndBeforeStart pins the status class as much as
// the refusal. The ordering is a database CHECK, so an unchecked
// request was already refused — as a 500 saying the event could not be
// saved, which tells the caller their input was fine and the server is
// broken.
//
// The all-day case is the one that shipped: the browser dialog skips
// its own check when all-day is on, so an inverted all-day range was
// the request that reached the constraint.
func TestCreateEventRejectsEndBeforeStart(t *testing.T) {
	bootstrap(t)
	t.Parallel()

	tt := newTenant(t)
	calID := createCalendar(t, tt)
	start := time.Date(2026, 5, 6, 10, 0, 0, 0, time.UTC)

	cases := []struct {
		name   string
		allDay bool
		end    time.Time
	}{
		{"timed", false, start.Add(-time.Hour)},
		{"all day, days inverted", true, start.AddDate(0, 0, -2)},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			status, raw := helpers.DoJSONStatus(t, http.MethodPost,
				tt.WsPath("calendars", calID, "events"), tt.AccessToken, map[string]any{
					"kind":     "event",
					"title":    "Inverted " + c.name,
					"allDay":   c.allDay,
					"startAt":  start.Unix(),
					"endAt":    c.end.Unix(),
					"timezone": "UTC",
				})
			assert.Equal(t, http.StatusUnprocessableEntity, status,
				"an inverted range is bad input, not a server fault: got %d body=%s", status, string(raw))
			assert.Equal(t, "CALENDAR.EVENT.END_BEFORE_START", problemType(t, raw),
				"the answer has to name which fields are wrong")
		})
	}
}

// TestPatchEventRejectsEndBeforeStart covers the same inversion arriving
// through the update path.
func TestPatchEventRejectsEndBeforeStart(t *testing.T) {
	bootstrap(t)
	t.Parallel()

	tt := newTenant(t)
	calID := createCalendar(t, tt)
	start := time.Date(2026, 5, 6, 10, 0, 0, 0, time.UTC)

	var created struct {
		ID string `json:"id"`
	}
	helpers.DoJSON(t, http.MethodPost,
		tt.WsPath("calendars", calID, "events"), tt.AccessToken, map[string]any{
			"kind":     "event",
			"title":    "Patch inversion",
			"startAt":  start.Unix(),
			"endAt":    start.Add(time.Hour).Unix(),
			"timezone": "UTC",
		}, &created)

	status, raw := helpers.DoJSONStatus(t, http.MethodPatch,
		tt.WsPath("calendars", calID, "events", created.ID), tt.AccessToken, map[string]any{
			"startAt": start.Unix(),
			"endAt":   start.Add(-time.Hour).Unix(),
		})
	assert.Equal(t, http.StatusUnprocessableEntity, status, "body=%s", string(raw))
	assert.Equal(t, "CALENDAR.EVENT.END_BEFORE_START", problemType(t, raw))
}

// TestCreateEventAcceptsZeroLengthRange keeps the chronology check from
// swallowing milestones, which are written as an event whose end equals
// its start. The database CHECK allows equality; so must the handler.
func TestCreateEventAcceptsZeroLengthRange(t *testing.T) {
	bootstrap(t)
	t.Parallel()

	tt := newTenant(t)
	calID := createCalendar(t, tt)
	at := time.Date(2026, 5, 7, 0, 0, 0, 0, time.UTC)

	var resp struct {
		ID      string `json:"id"`
		StartAt int64  `json:"startAt"`
		EndAt   int64  `json:"endAt"`
	}
	helpers.DoJSON(t, http.MethodPost,
		tt.WsPath("calendars", calID, "events"), tt.AccessToken, map[string]any{
			"kind":     "milestone",
			"title":    "Ship it",
			"startAt":  at.Unix(),
			"endAt":    at.Unix(),
			"timezone": "UTC",
		}, &resp)
	require.NotEmpty(t, resp.ID)
	assert.Equal(t, resp.StartAt, resp.EndAt)
}

// TestPublicShareRejectsUnresolvableTimezone covers the same input on
// the share endpoints, where the check was already being run and its
// failure was reported as CALENDAR.CALENDAR.STORE_WRITE_INTERRUPTED —
// a 500 telling the caller the server could not save, for a request the
// server had decided not to save.
func TestPublicShareRejectsUnresolvableTimezone(t *testing.T) {
	bootstrap(t)
	t.Parallel()

	tt := newTenant(t)

	status, raw := helpers.DoJSONStatus(t, http.MethodPost,
		tt.WsPath("public-shares"), tt.AccessToken, map[string]any{
			"title":    "Bad zone share",
			"timezone": "JST",
		})
	assert.Equal(t, http.StatusUnprocessableEntity, status,
		"a rejected timezone is bad input, not a storage failure: got %d body=%s", status, string(raw))
	assert.Equal(t, "VALIDATION.BODY.FIELD_INVALID", problemType(t, raw))

	var share struct {
		ID string `json:"id"`
	}
	helpers.DoJSON(t, http.MethodPost, tt.WsPath("public-shares"), tt.AccessToken, map[string]any{
		"title":    "Good zone share",
		"timezone": "Europe/Berlin",
	}, &share)
	require.NotEmpty(t, share.ID)

	status, raw = helpers.DoJSONStatus(t, http.MethodPatch,
		tt.WsPath("public-shares", share.ID), tt.AccessToken, map[string]any{
			"timezone": "GMT+9",
		})
	assert.Equal(t, http.StatusUnprocessableEntity, status, "body=%s", string(raw))
	assert.Equal(t, "VALIDATION.BODY.FIELD_INVALID", problemType(t, raw))
}

// TestSmartCreateRejectsUnresolvableTimezone completes the set. This
// path reported the same input as unparseable text, which sends the
// caller to rewrite a sentence that was never the problem.
func TestSmartCreateRejectsUnresolvableTimezone(t *testing.T) {
	bootstrap(t)
	t.Parallel()

	tt := newTenant(t)
	calID := createCalendar(t, tt)

	status, raw := helpers.DoJSONStatus(t, http.MethodPost,
		tt.WsPath("calendars", calID, "events", "smart-create"), tt.AccessToken, map[string]any{
			"text":     "明日15時から打ち合わせ",
			"timezone": "JST",
		})
	assert.Equal(t, http.StatusUnprocessableEntity, status, "body=%s", string(raw))
	assert.Equal(t, "VALIDATION.BODY.FIELD_INVALID", problemType(t, raw),
		"a bad timezone must not be reported as unparseable text")
}

// TestSmartCreateRefusesTextItCannotRead is the fabrication check. The
// parser only knows Japanese date and time expressions. Falling back
// to today at 09:00 for anything else returns that as a proposal, so an
// English caller gets a confident answer for an appointment nobody
// described.
//
// The assertion is that no proposal comes back at all. Checking the
// returned time against the request would not do: there is no correct
// time to compare against, which is the whole point.
func TestSmartCreateRefusesTextItCannotRead(t *testing.T) {
	bootstrap(t)
	t.Parallel()

	tt := newTenant(t)
	calID := createCalendar(t, tt)

	for _, text := range []string{
		"lunch with Sam tomorrow 3pm",
		"Mittagessen mit Sam morgen",
		"just some words",
	} {
		status, raw := helpers.DoJSONStatus(t, http.MethodPost,
			tt.WsPath("calendars", calID, "events", "smart-create"), tt.AccessToken, map[string]any{
				"text": text,
			})
		assert.Equalf(t, http.StatusUnprocessableEntity, status,
			"%q carries no date or time this parser reads; inventing one is the defect: got %d body=%s",
			text, status, string(raw))
		assert.Equalf(t, "CALENDAR.SMART_CREATE.TEXT_UNPARSEABLE", problemType(t, raw),
			"%q must be reported as unreadable text", text)
	}
}

// TestSmartCreateStillReadsJapanese guards the other direction: the
// refusal must not have been implemented by refusing everything.
func TestSmartCreateStillReadsJapanese(t *testing.T) {
	bootstrap(t)
	t.Parallel()

	tt := newTenant(t)
	calID := createCalendar(t, tt)

	var resp struct {
		Proposal struct {
			Title   string `json:"title"`
			StartAt int64  `json:"startAt"`
			EndAt   int64  `json:"endAt"`
		} `json:"proposal"`
	}
	helpers.DoJSON(t, http.MethodPost,
		tt.WsPath("calendars", calID, "events", "smart-create"), tt.AccessToken, map[string]any{
			"text":     "明日15時から16時まで定例会議",
			"timezone": "Asia/Tokyo",
		}, &resp)

	assert.Equal(t, "定例会議", resp.Proposal.Title)
	require.NotZero(t, resp.Proposal.StartAt)
	assert.Equal(t, int64(3600), resp.Proposal.EndAt-resp.Proposal.StartAt)

	tokyo, err := time.LoadLocation("Asia/Tokyo")
	require.NoError(t, err)
	assert.Equal(t, 15, time.Unix(resp.Proposal.StartAt, 0).In(tokyo).Hour())
}
