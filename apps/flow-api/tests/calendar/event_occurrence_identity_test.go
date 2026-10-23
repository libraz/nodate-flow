package calendar

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/libraz/nodate-flow/apps/flow-api/tests/helpers"
	"github.com/libraz/nodate-flow/packages/go-shared/recurrence"
)

// Tests in this file pin how an occurrence is addressed: by an original
// start the series rule produces, refused otherwise on every scoped write,
// and kept cancelled or replaced when the series is split or the row
// standing in for it is deleted.

// zonedWeeklySeries creates a weekly master drawn in timezone and returns
// its public ID.
func zonedWeeklySeries(t *testing.T, tt *helpers.CalendarTestTenant, calID, timezone string, allDay bool, start, end time.Time) string {
	t.Helper()
	var created eventView
	helpers.DoJSON(t, http.MethodPost, tt.WsPath("calendars", calID, "events"), tt.AccessToken, map[string]any{
		"kind":           "event",
		"title":          "Weekly",
		"allDay":         allDay,
		"startAt":        start.Unix(),
		"endAt":          end.Unix(),
		"timezone":       timezone,
		"recurrenceRule": map[string]any{"freq": "weekly", "interval": 1},
	}, &created)
	require.NotEmpty(t, created.ID)
	return created.ID
}

// countOverrides counts the override rows naming a master, disabled ones
// included.
func countOverrides(t *testing.T, masterID string) int {
	t.Helper()
	var n int
	require.NoError(t, testDB.QueryRowContext(context.Background(),
		`SELECT COUNT(*) FROM calendar_events WHERE recurrence_parent_id = ?`,
		readEventRow(t, masterID).id).Scan(&n))
	return n
}

// requireScopedWritesRefuse sends every scoped write naming at and expects
// each to be refused as naming no occurrence, with the series and its
// overrides unchanged.
func requireScopedWritesRefuse(t *testing.T, tt *helpers.CalendarTestTenant, calID, masterID string, at time.Time) {
	t.Helper()
	bounds := readSeriesBounds(t, masterID)
	overrides := countOverrides(t, masterID)
	url := tt.WsPath("calendars", calID, "events", masterID)

	for _, scope := range []string{"occurrence", "thisAndFollowing"} {
		status, body := helpers.DoJSONStatus(t, http.MethodPatch, url, tt.AccessToken, map[string]any{
			"scope":           scope,
			"occurrenceStart": at.Unix(),
			"title":           "Should not land",
		})
		assert.Equal(t, http.StatusUnprocessableEntity, status, "PATCH %s at %s body=%s", scope, at, string(body))
		assert.Contains(t, string(body), "CALENDAR.EVENT.OCCURRENCE_START_NOT_IN_SERIES", "PATCH %s body=%s", scope, string(body))

		status, body = deleteEventScopedBody(t, tt, calID, masterID,
			fmt.Sprintf("scope=%s&occurrenceStart=%d", scope, at.Unix()))
		assert.Equal(t, http.StatusUnprocessableEntity, status, "DELETE %s at %s body=%s", scope, at, string(body))
		assert.Contains(t, string(body), "CALENDAR.EVENT.OCCURRENCE_START_NOT_IN_SERIES", "DELETE %s body=%s", scope, string(body))
	}

	assert.Equal(t, bounds, readSeriesBounds(t, masterID), "a refused write must leave the series columns alone")
	assert.Equal(t, overrides, countOverrides(t, masterID), "a refused write must add no override")
	assert.True(t, readEventRow(t, masterID).enabled)
}

func TestScopedWritesRefuseAnOccurrenceStartTheSeriesDoesNotProduce(t *testing.T) {
	bootstrap(t)
	t.Parallel()

	tt := newTenant(t)
	calID := createCalendar(t, tt)

	first := time.Date(2028, 9, 4, 10, 0, 0, 0, time.UTC)
	masterID := weeklySeries(t, tt, calID, first)

	t.Run("off the rule", func(t *testing.T) {
		requireScopedWritesRefuse(t, tt, calID, masterID, first.AddDate(0, 0, 3))
	})
	t.Run("before the start", func(t *testing.T) {
		requireScopedWritesRefuse(t, tt, calID, masterID, first.AddDate(0, 0, -7))
	})
	t.Run("after the recurrence end", func(t *testing.T) {
		requireScopedWritesRefuse(t, tt, calID, masterID, first.AddDate(0, 0, 7*60))
	})
	t.Run("already cancelled", func(t *testing.T) {
		cancelled := first.AddDate(0, 0, 7)
		require.Equal(t, http.StatusOK, deleteEventScoped(t, tt, calID, masterID,
			fmt.Sprintf("scope=occurrence&occurrenceStart=%d", cancelled.Unix())))
		requireScopedWritesRefuse(t, tt, calID, masterID, cancelled)
	})
}

// A 09:00 New York series is at 14:00Z before the DST change and 13:00Z
// after it. The instant a fixed seven-day step from the anchor gives is
// not an occurrence; the wall-clock one is.
func TestScopedWritesFollowTheSeriesWallClockAcrossDST(t *testing.T) {
	bootstrap(t)
	t.Parallel()

	tt := newTenant(t)
	calID := createCalendar(t, tt)

	ny, err := time.LoadLocation("America/New_York")
	require.NoError(t, err)
	first := time.Date(2027, 3, 1, 9, 0, 0, 0, ny)
	masterID := zonedWeeklySeries(t, tt, calID, "America/New_York", false, first, first.Add(time.Hour))

	requireScopedWritesRefuse(t, tt, calID, masterID, first.Add(14*24*time.Hour))

	afterDST := time.Date(2027, 3, 15, 9, 0, 0, 0, ny)
	var patched eventView
	helpers.DoJSON(t, http.MethodPatch, tt.WsPath("calendars", calID, "events", masterID), tt.AccessToken, map[string]any{
		"scope":           "occurrence",
		"occurrenceStart": afterDST.Unix(),
		"title":           "After DST",
	}, &patched)
	require.NotNil(t, patched.StartAt)
	assert.Equal(t, afterDST.Unix(), *patched.StartAt)
}

// An all-day series is addressed by UTC midnights whatever its zone: the
// instant New York's wall clock drifts to after DST is not an occurrence.
func TestScopedWritesAddressAllDayOccurrencesByUTCMidnight(t *testing.T) {
	bootstrap(t)
	t.Parallel()

	tt := newTenant(t)
	calID := createCalendar(t, tt)

	first := time.Date(2027, 3, 1, 0, 0, 0, 0, time.UTC)
	masterID := zonedWeeklySeries(t, tt, calID, "America/New_York", true, first, first.AddDate(0, 0, 1))

	requireScopedWritesRefuse(t, tt, calID, masterID, time.Date(2027, 3, 14, 23, 0, 0, 0, time.UTC))

	afterDST := time.Date(2027, 3, 15, 0, 0, 0, 0, time.UTC)
	require.Equal(t, http.StatusOK, deleteEventScoped(t, tt, calID, masterID,
		fmt.Sprintf("scope=occurrence&occurrenceStart=%d", afterDST.Unix())))
	assert.Contains(t, exceptionInstants(t, readSeriesBounds(t, masterID).exceptions), afterDST)
}

// Deleting the row a moved occurrence became removes the occurrence: the
// master records its original start as cancelled in the same delete, so
// nothing is drawn at the slot it was moved away from.
func TestDeleteMovedOccurrenceLeavesNothingAtTheOriginalSlot(t *testing.T) {
	bootstrap(t)
	t.Parallel()

	tt := newTenant(t)
	calID := createCalendar(t, tt)

	first := time.Date(2028, 10, 2, 10, 0, 0, 0, time.UTC)
	masterID := weeklySeries(t, tt, calID, first)
	second := first.AddDate(0, 0, 7)
	moved := second.Add(3 * time.Hour)

	var override eventView
	helpers.DoJSON(t, http.MethodPatch, tt.WsPath("calendars", calID, "events", masterID), tt.AccessToken, map[string]any{
		"scope":           "occurrence",
		"occurrenceStart": second.Unix(),
		"startAt":         moved.Unix(),
		"endAt":           moved.Add(30 * time.Minute).Unix(),
	}, &override)

	require.Equal(t, http.StatusOK, deleteEventScoped(t, tt, calID, override.ID, ""))
	assert.False(t, readEventRow(t, override.ID).enabled)
	assert.Contains(t, exceptionInstants(t, readSeriesBounds(t, masterID).exceptions), second,
		"the occurrence the deleted override replaced must stay cancelled on the master")

	listed := listOccurrences(t, tt, first.AddDate(0, 0, -1), first.AddDate(0, 0, 21))
	assert.NotContains(t, listed[masterID], second.Unix(), "the master must not produce the original slot again")
	assert.NotContains(t, listed, override.ID)
}

// A this-and-following edit that moves the start an hour keeps a later
// cancelled occurrence cancelled and a later moved one drawn once, by its
// override, on the continuing series.
func TestSplitWithAMovedStartKeepsLaterCancellationsAndOverrides(t *testing.T) {
	bootstrap(t)
	t.Parallel()

	tt := newTenant(t)
	calID := createCalendar(t, tt)

	first := time.Date(2028, 11, 6, 10, 0, 0, 0, time.UTC)
	masterID := weeklySeries(t, tt, calID, first)
	split := first.AddDate(0, 0, 14)
	cancelled := first.AddDate(0, 0, 21)
	replaced := first.AddDate(0, 0, 28)
	movedTo := replaced.Add(5 * time.Hour)

	require.Equal(t, http.StatusOK, deleteEventScoped(t, tt, calID, masterID,
		fmt.Sprintf("scope=occurrence&occurrenceStart=%d", cancelled.Unix())))
	var override eventView
	helpers.DoJSON(t, http.MethodPatch, tt.WsPath("calendars", calID, "events", masterID), tt.AccessToken, map[string]any{
		"scope":           "occurrence",
		"occurrenceStart": replaced.Unix(),
		"startAt":         movedTo.Unix(),
		"endAt":           movedTo.Add(30 * time.Minute).Unix(),
	}, &override)

	var continuing eventView
	helpers.DoJSON(t, http.MethodPatch, tt.WsPath("calendars", calID, "events", masterID), tt.AccessToken, map[string]any{
		"scope":           "thisAndFollowing",
		"occurrenceStart": split.Unix(),
		"startAt":         split.Add(time.Hour).Unix(),
		"endAt":           split.Add(90 * time.Minute).Unix(),
	}, &continuing)

	listed := listOccurrences(t, tt, first.AddDate(0, 0, -1), first.AddDate(0, 0, 42))
	series := listed[continuing.ID]
	assert.Contains(t, series, split.Add(time.Hour).Unix(), "the continuing series starts at the moved time")
	assert.NotContains(t, series, cancelled.Unix())
	assert.NotContains(t, series, cancelled.Add(time.Hour).Unix(), "the cancelled occurrence must stay cancelled")
	assert.NotContains(t, series, replaced.Add(time.Hour).Unix(), "the moved occurrence is drawn by its override alone")
	assert.NotContains(t, series, replaced.Unix())
	assert.Equal(t, []int64{movedTo.Unix()}, listed[override.ID], "the override is drawn once, at its own time")
}

// listOccurrences reads the workspace range endpoint and expands it the
// way the client does, keyed by event ID: a master's occurrences after its
// exceptions and overridden starts are subtracted, and every other row's
// own start.
func listOccurrences(t *testing.T, tt *helpers.CalendarTestTenant, start, end time.Time) map[string][]int64 {
	t.Helper()
	var resp struct {
		Events []struct {
			ID                   string          `json:"id"`
			AllDay               bool            `json:"allDay"`
			StartAt              *int64          `json:"startAt"`
			EndAt                *int64          `json:"endAt"`
			Timezone             string          `json:"timezone"`
			RecurrenceRule       json.RawMessage `json:"recurrenceRule"`
			RecurrenceEnd        *int64          `json:"recurrenceEnd"`
			RecurrenceExceptions json.RawMessage `json:"recurrenceExceptions"`
			OverriddenStarts     []string        `json:"overriddenStarts"`
		} `json:"events"`
	}
	q := fmt.Sprintf("?start=%s&end=%s", start.Format(time.RFC3339), end.Format(time.RFC3339))
	helpers.DoJSON(t, http.MethodGet, tt.WsPath("calendar-events")+q, tt.AccessToken, nil, &resp)

	out := map[string][]int64{}
	for _, e := range resp.Events {
		if e.StartAt == nil || e.EndAt == nil {
			continue
		}
		rule, err := recurrence.ParseRule(e.RecurrenceRule)
		require.NoError(t, err)
		if rule == nil {
			out[e.ID] = append(out[e.ID], *e.StartAt)
			continue
		}
		evt := recurrence.Event{
			StartAt:          time.Unix(*e.StartAt, 0).UTC(),
			EndAt:            time.Unix(*e.EndAt, 0).UTC(),
			Timezone:         e.Timezone,
			AllDay:           e.AllDay,
			Rule:             rule,
			Exceptions:       recurrence.ParseExceptions(e.RecurrenceExceptions),
			OverriddenStarts: e.OverriddenStarts,
		}
		if e.RecurrenceEnd != nil {
			until := time.Unix(*e.RecurrenceEnd, 0).UTC()
			evt.RecurrenceEnd = &until
		}
		for _, occ := range recurrence.Expand(evt, start, end) {
			out[e.ID] = append(out[e.ID], occ.StartAt.Unix())
		}
	}
	return out
}
