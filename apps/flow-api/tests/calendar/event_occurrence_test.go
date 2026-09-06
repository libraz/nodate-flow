package calendar

import (
	"context"
	"database/sql"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/libraz/nodate-flow/apps/flow-api/tests/helpers"
)

// eventView is the slice of the patch / get response these tests read.
type eventView struct {
	ID             string         `json:"id"`
	Title          string         `json:"title"`
	StartAt        *int64         `json:"startAt"`
	EndAt          *int64         `json:"endAt"`
	Location       *string        `json:"location"`
	RecurrenceRule map[string]any `json:"recurrenceRule"`
	RecurrenceEnd  *int64         `json:"recurrenceEnd"`
}

// eventRow is the part of a stored calendar_events row that says where it
// sits in a series. recurrence_parent_id and recurrence_original_start
// reach no API response, so a test that cares whether an override was
// written, revived or reparented has to read them from the table.
//
// recurrence_original_start is formatted in SQL rather than scanned as a
// time so the assertion does not depend on the driver's parseTime setting
// or on the session timezone.
type eventRow struct {
	id            uint32
	parentID      sql.NullInt64
	originalStart sql.NullString
	enabled       bool
}

func readEventRow(t *testing.T, publicID string) eventRow {
	t.Helper()
	var row eventRow
	err := testDB.QueryRowContext(
		context.Background(),
		`SELECT id,
		        recurrence_parent_id,
		        DATE_FORMAT(recurrence_original_start, '%Y-%m-%dT%H:%i:%sZ'),
		        enabled
		   FROM calendar_events
		  WHERE public_id = UUID_TO_BIN(?, 0)
		  LIMIT 1`,
		publicID,
	).Scan(&row.id, &row.parentID, &row.originalStart, &row.enabled)
	require.NoError(t, err, "read calendar_events row for %s", publicID)
	return row
}

// weeklySeries creates a recurring master and returns its public ID along
// with the start of its first occurrence.
func weeklySeries(t *testing.T, tt *helpers.CalendarTestTenant, calID string, first time.Time) string {
	t.Helper()
	var created eventView
	helpers.DoJSON(t, http.MethodPost, tt.WsPath("calendars", calID, "events"), tt.AccessToken, map[string]any{
		"kind":           "event",
		"title":          "Weekly Stand-up",
		"startAt":        first.Unix(),
		"endAt":          first.Add(30 * time.Minute).Unix(),
		"timezone":       "UTC",
		"recurrenceRule": map[string]any{"freq": "weekly", "interval": 1},
		"recurrenceEnd":  first.AddDate(1, 0, 0).Unix(),
	}, &created)
	require.NotEmpty(t, created.ID)
	return created.ID
}

func TestPatchEventOccurrence_WritesOverride(t *testing.T) {
	bootstrap(t)
	t.Parallel()

	tt := newTenant(t)
	calID := createCalendar(t, tt)

	first := time.Date(2027, 3, 1, 10, 0, 0, 0, time.UTC)
	masterID := weeklySeries(t, tt, calID, first)
	third := first.AddDate(0, 0, 14)

	var patched eventView
	helpers.DoJSON(t, http.MethodPatch, tt.WsPath("calendars", calID, "events", masterID), tt.AccessToken, map[string]any{
		"scope":           "occurrence",
		"occurrenceStart": third.Unix(),
		"title":           "Stand-up (room change)",
	}, &patched)

	require.NotEqual(t, masterID, patched.ID, "an occurrence edit must not rewrite the master")
	assert.Equal(t, "Stand-up (room change)", patched.Title)
	assert.Nil(t, patched.RecurrenceRule, "an override owns no rule of its own")
	require.NotNil(t, patched.StartAt)
	require.NotNil(t, patched.EndAt)
	assert.Equal(t, third.Unix(), *patched.StartAt, "the override keeps the occurrence's slot when no time is sent")
	assert.Equal(t, third.Add(30*time.Minute).Unix(), *patched.EndAt, "the override inherits the master's duration")

	master := readEventRow(t, masterID)
	override := readEventRow(t, patched.ID)
	require.True(t, override.parentID.Valid)
	assert.Equal(t, int64(master.id), override.parentID.Int64)
	assert.Equal(t, third.Format("2006-01-02T15:04:05Z"), override.originalStart.String)
	assert.True(t, override.enabled)

	// The master keeps its rule and its window.
	var reread eventView
	helpers.DoJSON(t, http.MethodGet, tt.WsPath("calendars", calID, "events", masterID), tt.AccessToken, nil, &reread)
	assert.Equal(t, "Weekly Stand-up", reread.Title)
	assert.NotNil(t, reread.RecurrenceRule)
}

func TestPatchEventOccurrence_RevivesRevertedOverride(t *testing.T) {
	bootstrap(t)
	t.Parallel()

	tt := newTenant(t)
	calID := createCalendar(t, tt)

	first := time.Date(2027, 4, 5, 9, 0, 0, 0, time.UTC)
	masterID := weeklySeries(t, tt, calID, first)
	second := first.AddDate(0, 0, 7)

	var firstEdit eventView
	helpers.DoJSON(t, http.MethodPatch, tt.WsPath("calendars", calID, "events", masterID), tt.AccessToken, map[string]any{
		"scope":           "occurrence",
		"occurrenceStart": second.Unix(),
		"title":           "Moved once",
	}, &firstEdit)
	require.NotEmpty(t, firstEdit.ID)

	// Reverting the occurrence to the series soft-deletes the override.
	// uniq_calendar_events_recurrence_override counts the disabled row, so
	// the next edit of the same occurrence has to revive it.
	var deleted struct {
		Deleted bool `json:"deleted"`
	}
	helpers.DoJSON(t, http.MethodDelete, tt.WsPath("calendars", calID, "events", firstEdit.ID), tt.AccessToken, nil, &deleted)
	require.True(t, deleted.Deleted)
	require.False(t, readEventRow(t, firstEdit.ID).enabled)

	var secondEdit eventView
	helpers.DoJSON(t, http.MethodPatch, tt.WsPath("calendars", calID, "events", masterID), tt.AccessToken, map[string]any{
		"scope":           "occurrence",
		"occurrenceStart": second.Unix(),
		"title":           "Moved again",
	}, &secondEdit)

	assert.Equal(t, firstEdit.ID, secondEdit.ID, "the second edit must revive the existing override, not insert a second one")
	assert.Equal(t, "Moved again", secondEdit.Title)
	assert.True(t, readEventRow(t, secondEdit.ID).enabled)
}

// TestPatchEventOccurrence_LaterEditKeepsTheOverridesOwnValues pins what a
// partial edit of an already overridden occurrence falls back to. The
// override row is the occurrence, so a member the caller leaves out keeps
// the override's value; falling back to the series would undo the move the
// override was created to record.
func TestPatchEventOccurrence_LaterEditKeepsTheOverridesOwnValues(t *testing.T) {
	bootstrap(t)
	t.Parallel()

	tt := newTenant(t)
	calID := createCalendar(t, tt)

	first := time.Date(2028, 2, 7, 10, 0, 0, 0, time.UTC)
	masterID := weeklySeries(t, tt, calID, first)
	second := first.AddDate(0, 0, 7)
	moved := second.Add(3 * time.Hour)

	var movedEdit eventView
	helpers.DoJSON(t, http.MethodPatch, tt.WsPath("calendars", calID, "events", masterID), tt.AccessToken, map[string]any{
		"scope":           "occurrence",
		"occurrenceStart": second.Unix(),
		"startAt":         moved.Unix(),
		"endAt":           moved.Add(45 * time.Minute).Unix(),
		"location":        "Room 12",
	}, &movedEdit)
	require.NotEmpty(t, movedEdit.ID)
	require.NotNil(t, movedEdit.StartAt)
	require.Equal(t, moved.Unix(), *movedEdit.StartAt)

	var renamed eventView
	helpers.DoJSON(t, http.MethodPatch, tt.WsPath("calendars", calID, "events", masterID), tt.AccessToken, map[string]any{
		"scope":           "occurrence",
		"occurrenceStart": second.Unix(),
		"title":           "Stand-up (agenda review)",
	}, &renamed)

	assert.Equal(t, movedEdit.ID, renamed.ID, "the second edit writes the same override row")
	assert.Equal(t, "Stand-up (agenda review)", renamed.Title)
	require.NotNil(t, renamed.StartAt)
	require.NotNil(t, renamed.EndAt)
	assert.Equal(t, moved.Unix(), *renamed.StartAt, "a title-only edit must not move the occurrence back to the series' time")
	assert.Equal(t, moved.Add(45*time.Minute).Unix(), *renamed.EndAt, "the override keeps its own end")
	require.NotNil(t, renamed.Location, "a member the second edit did not send keeps the override's value")
	assert.Equal(t, "Room 12", *renamed.Location)

	// The occurrence a later edit did not name is untouched, so the series
	// still produces it in its own slot.
	var third eventView
	helpers.DoJSON(t, http.MethodPatch, tt.WsPath("calendars", calID, "events", masterID), tt.AccessToken, map[string]any{
		"scope":           "occurrence",
		"occurrenceStart": first.AddDate(0, 0, 14).Unix(),
		"title":           "Untouched slot",
	}, &third)
	require.NotNil(t, third.StartAt)
	assert.Equal(t, first.AddDate(0, 0, 14).Unix(), *third.StartAt)
	assert.Nil(t, third.Location, "a first override still falls back to the series")
}

func TestPatchEventOccurrence_RefusesWithoutOccurrenceStart(t *testing.T) {
	bootstrap(t)
	t.Parallel()

	tt := newTenant(t)
	calID := createCalendar(t, tt)
	masterID := weeklySeries(t, tt, calID, time.Date(2027, 5, 3, 10, 0, 0, 0, time.UTC))

	for _, scope := range []string{"occurrence", "thisAndFollowing"} {
		t.Run(scope, func(t *testing.T) {
			status, body := helpers.DoJSONStatus(t, http.MethodPatch, tt.WsPath("calendars", calID, "events", masterID), tt.AccessToken, map[string]any{
				"scope": scope,
				"title": "Nameless occurrence",
			})
			assert.Equal(t, http.StatusUnprocessableEntity, status, "body=%s", string(body))
			assert.Contains(t, string(body), "CALENDAR.EVENT.OCCURRENCE_START_REQUIRED", "body=%s", string(body))
		})
	}
}

func TestPatchEventOccurrence_RefusesOnNonRecurringEvent(t *testing.T) {
	bootstrap(t)
	t.Parallel()

	tt := newTenant(t)
	calID := createCalendar(t, tt)

	start := time.Date(2027, 6, 7, 10, 0, 0, 0, time.UTC)
	var created eventView
	helpers.DoJSON(t, http.MethodPost, tt.WsPath("calendars", calID, "events"), tt.AccessToken, map[string]any{
		"kind":     "event",
		"title":    "One-off",
		"startAt":  start.Unix(),
		"endAt":    start.Add(time.Hour).Unix(),
		"timezone": "UTC",
	}, &created)

	status, body := helpers.DoJSONStatus(t, http.MethodPatch, tt.WsPath("calendars", calID, "events", created.ID), tt.AccessToken, map[string]any{
		"scope":           "occurrence",
		"occurrenceStart": start.Unix(),
		"title":           "No such occurrence",
	})
	assert.Equal(t, http.StatusUnprocessableEntity, status, "body=%s", string(body))
	assert.Contains(t, string(body), "CALENDAR.EVENT.NOT_RECURRING", "body=%s", string(body))
}

func TestPatchEventOccurrence_RefusesOnOverrideRow(t *testing.T) {
	bootstrap(t)
	t.Parallel()

	tt := newTenant(t)
	calID := createCalendar(t, tt)

	first := time.Date(2027, 7, 5, 10, 0, 0, 0, time.UTC)
	masterID := weeklySeries(t, tt, calID, first)
	second := first.AddDate(0, 0, 7)

	var override eventView
	helpers.DoJSON(t, http.MethodPatch, tt.WsPath("calendars", calID, "events", masterID), tt.AccessToken, map[string]any{
		"scope":           "occurrence",
		"occurrenceStart": second.Unix(),
		"title":           "Overridden",
	}, &override)
	require.NotEmpty(t, override.ID)

	// An override of an override. The projection guard reads only the row
	// being written and never follows the parent link, so the database
	// would accept the chain and nothing downstream could read it.
	status, body := helpers.DoJSONStatus(t, http.MethodPatch, tt.WsPath("calendars", calID, "events", override.ID), tt.AccessToken, map[string]any{
		"scope":           "occurrence",
		"occurrenceStart": second.Unix(),
		"title":           "Second level",
	})
	assert.Equal(t, http.StatusUnprocessableEntity, status, "body=%s", string(body))
	assert.Contains(t, string(body), "CALENDAR.EVENT.ALREADY_OCCURRENCE_OVERRIDE", "body=%s", string(body))
}

func TestPatchEventOccurrence_RefusesRecurrenceFields(t *testing.T) {
	bootstrap(t)
	t.Parallel()

	tt := newTenant(t)
	calID := createCalendar(t, tt)

	first := time.Date(2027, 8, 2, 10, 0, 0, 0, time.UTC)
	masterID := weeklySeries(t, tt, calID, first)
	second := first.AddDate(0, 0, 7)

	cases := []struct {
		name string
		body map[string]any
	}{
		{"rule", map[string]any{"recurrenceRule": map[string]any{"freq": "daily"}}},
		{"end", map[string]any{"recurrenceEnd": first.AddDate(0, 1, 0).Unix()}},
		{"exceptions", map[string]any{"recurrenceExceptions": []string{"2027-08-16"}}},
		{"clear", map[string]any{"clear": []string{"recurrenceRule"}}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			body := map[string]any{"scope": "occurrence", "occurrenceStart": second.Unix()}
			for k, v := range tc.body {
				body[k] = v
			}
			status, raw := helpers.DoJSONStatus(t, http.MethodPatch, tt.WsPath("calendars", calID, "events", masterID), tt.AccessToken, body)
			assert.Equal(t, http.StatusUnprocessableEntity, status, "body=%s", string(raw))
			assert.Contains(t, string(raw), "CALENDAR.EVENT.RECURRENCE_ON_OCCURRENCE_NOT_ALLOWED", "body=%s", string(raw))
		})
	}
}

// TestPatchEvent_RefusesRecurrenceRuleOnOverrideRow covers the path that
// needs no scope at all: PatchCalendarEvent matches by public id and will
// match an override directly, and the projection guard answers a rule on
// such a row with SQLSTATE 45000 — an unexplained 500 unless the handler
// refuses first.
func TestPatchEvent_RefusesRecurrenceRuleOnOverrideRow(t *testing.T) {
	bootstrap(t)
	t.Parallel()

	tt := newTenant(t)
	calID := createCalendar(t, tt)

	first := time.Date(2027, 9, 6, 10, 0, 0, 0, time.UTC)
	masterID := weeklySeries(t, tt, calID, first)
	second := first.AddDate(0, 0, 7)

	var override eventView
	helpers.DoJSON(t, http.MethodPatch, tt.WsPath("calendars", calID, "events", masterID), tt.AccessToken, map[string]any{
		"scope":           "occurrence",
		"occurrenceStart": second.Unix(),
		"title":           "Overridden",
	}, &override)
	require.NotEmpty(t, override.ID)

	status, body := helpers.DoJSONStatus(t, http.MethodPatch, tt.WsPath("calendars", calID, "events", override.ID), tt.AccessToken, map[string]any{
		"recurrenceRule": map[string]any{"freq": "weekly", "interval": 1},
	})
	assert.Equal(t, http.StatusUnprocessableEntity, status, "body=%s", string(body))
	assert.Contains(t, string(body), "CALENDAR.EVENT.RECURRENCE_ON_OCCURRENCE_NOT_ALLOWED", "body=%s", string(body))

	// A patch that leaves the recurrence columns alone still works on an
	// override row.
	var renamed eventView
	helpers.DoJSON(t, http.MethodPatch, tt.WsPath("calendars", calID, "events", override.ID), tt.AccessToken, map[string]any{
		"title": "Override renamed",
	}, &renamed)
	assert.Equal(t, "Override renamed", renamed.Title)
}

func TestPatchEventThisAndFollowing_SplitsSeries(t *testing.T) {
	bootstrap(t)
	t.Parallel()

	tt := newTenant(t)
	calID := createCalendar(t, tt)

	first := time.Date(2027, 10, 4, 10, 0, 0, 0, time.UTC)
	masterID := weeklySeries(t, tt, calID, first)
	split := first.AddDate(0, 0, 14)

	var continuing eventView
	helpers.DoJSON(t, http.MethodPatch, tt.WsPath("calendars", calID, "events", masterID), tt.AccessToken, map[string]any{
		"scope":           "thisAndFollowing",
		"occurrenceStart": split.Unix(),
		"title":           "Stand-up (new format)",
	}, &continuing)

	require.NotEqual(t, masterID, continuing.ID)
	assert.Equal(t, "Stand-up (new format)", continuing.Title)
	require.NotNil(t, continuing.RecurrenceRule, "the remainder keeps a rule of its own")
	assert.Equal(t, "weekly", continuing.RecurrenceRule["freq"])
	require.NotNil(t, continuing.StartAt)
	assert.Equal(t, split.Unix(), *continuing.StartAt)
	require.NotNil(t, continuing.RecurrenceEnd, "the remainder inherits the original recurrence end")
	assert.Equal(t, first.AddDate(1, 0, 0).Unix(), *continuing.RecurrenceEnd)

	// The truncated master stops before the split. recurrence_end is
	// exclusive of the split by one millisecond, which reads as the second
	// before it in unix seconds.
	var truncated eventView
	helpers.DoJSON(t, http.MethodGet, tt.WsPath("calendars", calID, "events", masterID), tt.AccessToken, nil, &truncated)
	assert.Equal(t, "Weekly Stand-up", truncated.Title)
	require.NotNil(t, truncated.RecurrenceEnd)
	assert.Equal(t, split.Unix()-1, *truncated.RecurrenceEnd)

	// The new master is a master, not an override.
	assert.False(t, readEventRow(t, continuing.ID).parentID.Valid)
}

func TestPatchEventThisAndFollowing_ReparentsOverridesFromSplit(t *testing.T) {
	bootstrap(t)
	t.Parallel()

	tt := newTenant(t)
	calID := createCalendar(t, tt)

	first := time.Date(2027, 11, 1, 10, 0, 0, 0, time.UTC)
	masterID := weeklySeries(t, tt, calID, first)
	before := first.AddDate(0, 0, 7)
	after := first.AddDate(0, 0, 21)
	split := first.AddDate(0, 0, 14)

	var earlyOverride, lateOverride eventView
	helpers.DoJSON(t, http.MethodPatch, tt.WsPath("calendars", calID, "events", masterID), tt.AccessToken, map[string]any{
		"scope":           "occurrence",
		"occurrenceStart": before.Unix(),
		"title":           "Before the split",
	}, &earlyOverride)
	helpers.DoJSON(t, http.MethodPatch, tt.WsPath("calendars", calID, "events", masterID), tt.AccessToken, map[string]any{
		"scope":           "occurrence",
		"occurrenceStart": after.Unix(),
		"title":           "After the split",
	}, &lateOverride)

	var continuing eventView
	helpers.DoJSON(t, http.MethodPatch, tt.WsPath("calendars", calID, "events", masterID), tt.AccessToken, map[string]any{
		"scope":           "thisAndFollowing",
		"occurrenceStart": split.Unix(),
		"title":           "Stand-up (new format)",
	}, &continuing)

	oldMaster := readEventRow(t, masterID)
	newMaster := readEventRow(t, continuing.ID)

	early := readEventRow(t, earlyOverride.ID)
	require.True(t, early.parentID.Valid)
	assert.Equal(t, int64(oldMaster.id), early.parentID.Int64, "an override before the split belongs to the truncated series")

	late := readEventRow(t, lateOverride.ID)
	require.True(t, late.parentID.Valid)
	assert.Equal(t, int64(newMaster.id), late.parentID.Int64, "an override at or after the split belongs to the continuing series")
}

// TestPatchEvent_OmittedScopeStillPatchesTheSeries pins the default: a
// body that carries no scope reaches the master, which is what every
// caller written before the field did.
func TestPatchEvent_OmittedScopeStillPatchesTheSeries(t *testing.T) {
	bootstrap(t)
	t.Parallel()

	tt := newTenant(t)
	calID := createCalendar(t, tt)

	first := time.Date(2027, 12, 6, 10, 0, 0, 0, time.UTC)
	masterID := weeklySeries(t, tt, calID, first)

	var patched eventView
	helpers.DoJSON(t, http.MethodPatch, tt.WsPath("calendars", calID, "events", masterID), tt.AccessToken, map[string]any{
		"title": "Renamed series",
	}, &patched)

	assert.Equal(t, masterID, patched.ID)
	assert.Equal(t, "Renamed series", patched.Title)
	require.NotNil(t, patched.RecurrenceRule)
	assert.Equal(t, "weekly", patched.RecurrenceRule["freq"])
}
