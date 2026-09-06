package calendar

import (
	"bytes"
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/libraz/nodate-flow/apps/flow-api/tests/helpers"
)

// Tests in this file pin what the workspace-authenticated share editor is
// told about occurrences its page still advertises at a start they moved
// away from.
//
// Publishing a master publishes every occurrence its rule expands to.
// Moving one occurrence writes a separate override row and leaves the rule
// alone, so unless that override is published on the same share the page
// keeps drawing the occurrence where it no longer happens. Neither side
// reports the disagreement on its own; unpublishedOverrides is what names
// it, on the master row the editor already lists.
//
// The handler under test is calendars.GetPublicShare.

// editorOverrideWarning is one entry of the field these tests exist for.
type editorOverrideWarning struct {
	OriginalStart int64  `json:"originalStart"`
	EventID       string `json:"eventId"`
	StartAt       int64  `json:"startAt"`
	Title         string `json:"title"`
	Visibility    string `json:"visibility"`
}

// editorShareEvent names every field the editor listing carries.
type editorShareEvent struct {
	LinkID               string                  `json:"linkId"`
	EventID              string                  `json:"eventId"`
	Title                string                  `json:"title"`
	StartAt              *int64                  `json:"startAt"`
	EndAt                *int64                  `json:"endAt"`
	AllDay               bool                    `json:"allDay"`
	Timezone             string                  `json:"timezone"`
	Location             *string                 `json:"location"`
	Visibility           string                  `json:"visibility"`
	CalendarID           string                  `json:"calendarId"`
	CalendarName         string                  `json:"calendarName"`
	LinkSortWeight       int32                   `json:"linkSortWeight"`
	LinkCreatedAt        int64                   `json:"linkCreatedAt"`
	UnpublishedOverrides []editorOverrideWarning `json:"unpublishedOverrides"`
}

// editorShareHeader names every field the share itself carries.
type editorShareHeader struct {
	ID                  string  `json:"id"`
	Title               string  `json:"title"`
	Description         *string `json:"description"`
	IconURL             *string `json:"iconUrl"`
	CoverURL            *string `json:"coverUrl"`
	Timezone            string  `json:"timezone"`
	ShowHolidaysCountry *string `json:"showHolidaysCountry"`
	ExpiresAt           *int64  `json:"expiresAt"`
	SortWeight          int32   `json:"sortWeight"`
	EventCount          int64   `json:"eventCount"`
	CreatorID           *string `json:"creatorId"`
	CreatorDisplayName  *string `json:"creatorDisplayName"`
	UpdatedAt           *int64  `json:"updatedAt"`
	CreatedAt           int64   `json:"createdAt"`
}

// editorShareView is the whole response, spelled out so it can be decoded
// strictly. $schema is the transport's own link to the response schema and
// belongs to every body the API returns; it is named here for the same
// reason the rest are, since a decode that refuses unknown fields refuses
// it too.
type editorShareView struct {
	Schema string             `json:"$schema"`
	Share  editorShareHeader  `json:"share"`
	Events []editorShareEvent `json:"events"`
}

// readShareEditor reads the editor view of a share and holds the response
// to the fields named above.
//
// The strict decode is the leak guard. A row is addressed two ways: inside
// the database by a dense counter that every foreign key is written
// against, and on the wire by a UUID v7. Handing the counter out cannot be
// undone — a client that learned one keeps it, and it names the row in
// every workspace at once. Every field named here is public by
// construction, so a field the response carries and this type does not is
// the shape a counter reaches the wire in, and DisallowUnknownFields
// refuses it whatever it is called.
//
// The repository's existing guard against this (apps/flow-api/tests/
// responseids) derives the same property for MCP tool responses, which are
// hand-built map[string]any with no schema between the row and the
// transport. It reads only internal/mcp, so it does not reach a REST
// handler; the decode is that reading applied to the one response this
// feature adds a table-to-wire path into.
func readShareEditor(t *testing.T, tt *helpers.CalendarTestTenant, shareID string) (editorShareView, []byte) {
	t.Helper()

	status, raw := helpers.DoJSONStatus(t, http.MethodGet, tt.WsPath("public-shares", shareID), tt.AccessToken, nil)
	require.Equal(t, http.StatusOK, status, "GET share editor -> %d body=%s", status, string(raw))
	require.NotEmpty(t, raw, "the editor response must carry a body to read")

	var view editorShareView
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	require.NoError(t, dec.Decode(&view),
		"the editor response carries a field this test does not name; an unnamed field is "+
			"how a row's internal counter reaches a caller. body=%s", string(raw))

	// Every identifier the response states has to be the public spelling.
	// A UUID parse is what separates it from the counter: a counter is a
	// small decimal and fails here.
	requirePublicID(t, view.Share.ID, "share id")
	for _, e := range view.Events {
		requirePublicID(t, e.LinkID, "link id")
		requirePublicID(t, e.EventID, "event id")
		requirePublicID(t, e.CalendarID, "calendar id")
		for _, w := range e.UnpublishedOverrides {
			requirePublicID(t, w.EventID, "unpublished override event id")
		}
	}
	return view, raw
}

func requirePublicID(t *testing.T, value, what string) {
	t.Helper()
	_, err := uuid.Parse(value)
	require.NoErrorf(t, err, "%s is %q, which is not a UUID; the wire carries public ids only", what, value)
}

func findEditorEvent(t *testing.T, events []editorShareEvent, id string) editorShareEvent {
	t.Helper()
	for _, e := range events {
		if e.EventID == id {
			return e
		}
	}
	t.Fatalf("event %s not in the editor listing (%d events)", id, len(events))
	return editorShareEvent{}
}

// TestGetPublicShare_MasterAloneReportsMovedOccurrence is the case the
// field exists for: the share publishes the series and not the row that
// moved one of its occurrences, so the page draws that occurrence at a
// time it no longer happens and says so to nobody.
func TestGetPublicShare_MasterAloneReportsMovedOccurrence(t *testing.T) {
	bootstrap(t)
	t.Parallel()

	tt := newTenant(t)
	calID := createCalendar(t, tt)

	first := time.Date(2028, 3, 6, 10, 0, 0, 0, time.UTC)
	masterID := weeklySeries(t, tt, calID, first)
	third := first.AddDate(0, 0, 14)
	overrideID := overrideOneOccurrence(t, tt, tt.AccessToken, calID, masterID, "Stand-up (moved)", third)

	shareID, _ := createShare(t, tt, "Series Only")
	attached, skipped := attachShareEvents(t, tt, shareID, masterID)
	require.Equal(t, 1, attached, "only the master is published")
	require.Equal(t, 0, skipped)

	view, _ := readShareEditor(t, tt, shareID)
	require.Len(t, view.Events, 1, "the share publishes the master alone")

	master := findEditorEvent(t, view.Events, masterID)
	require.Len(t, master.UnpublishedOverrides, 1,
		"the moved occurrence must be named on the master the page draws it from")

	warning := master.UnpublishedOverrides[0]
	assert.Equal(t, overrideID, warning.EventID, "the warning names the row that moved")
	assert.Equal(t, third.Unix(), warning.OriginalStart,
		"originalStart is the occurrence the page still shows, as unix seconds — the same "+
			"shape as the startAt beside it on this response")
	assert.Equal(t, third.Unix(), warning.StartAt,
		"an occurrence edit that sends no time keeps the occurrence's own slot; the "+
			"disagreement here is that the page shows it at all")
	assert.Equal(t, "Stand-up (moved)", warning.Title)
	assert.Equal(t, "default", warning.Visibility,
		"a publishable override reports the visibility that says so, so the editor can offer "+
			"to attach it")
}

// TestGetPublicShare_MovedOccurrenceReportsItsNewStart moves the
// occurrence to a different time, which is what makes the two instants
// carry different answers.
func TestGetPublicShare_MovedOccurrenceReportsItsNewStart(t *testing.T) {
	bootstrap(t)
	t.Parallel()

	tt := newTenant(t)
	calID := createCalendar(t, tt)

	first := time.Date(2028, 4, 3, 10, 0, 0, 0, time.UTC)
	masterID := weeklySeries(t, tt, calID, first)
	third := first.AddDate(0, 0, 14)
	moved := third.Add(26 * time.Hour)

	var patched eventView
	helpers.DoJSON(t, http.MethodPatch, tt.WsPath("calendars", calID, "events", masterID), tt.AccessToken, map[string]any{
		"scope":           "occurrence",
		"occurrenceStart": third.Unix(),
		"title":           "Stand-up (rescheduled)",
		"startAt":         moved.Unix(),
		"endAt":           moved.Add(30 * time.Minute).Unix(),
	}, &patched)
	require.NotEqual(t, masterID, patched.ID)

	shareID, _ := createShare(t, tt, "Series With A Moved Week")
	attached, _ := attachShareEvents(t, tt, shareID, masterID)
	require.Equal(t, 1, attached)

	view, _ := readShareEditor(t, tt, shareID)
	master := findEditorEvent(t, view.Events, masterID)
	require.Len(t, master.UnpublishedOverrides, 1)

	warning := master.UnpublishedOverrides[0]
	assert.Equal(t, third.Unix(), warning.OriginalStart, "the start the page still advertises")
	assert.Equal(t, moved.Unix(), warning.StartAt, "the start the occurrence actually moved to")
	assert.NotEqual(t, warning.OriginalStart, warning.StartAt,
		"describing the discrepancy needs both instants; one of them alone says nothing")
}

// TestGetPublicShare_PublishedReplacementReportsNothing holds the field
// off a share that is telling the truth. With both halves published the
// page draws the occurrence where it happens, so there is nothing to warn
// about and the response must stay as it was.
func TestGetPublicShare_PublishedReplacementReportsNothing(t *testing.T) {
	bootstrap(t)
	t.Parallel()

	tt := newTenant(t)
	calID := createCalendar(t, tt)

	first := time.Date(2028, 5, 1, 10, 0, 0, 0, time.UTC)
	masterID := weeklySeries(t, tt, calID, first)
	third := first.AddDate(0, 0, 14)
	overrideID := overrideOneOccurrence(t, tt, tt.AccessToken, calID, masterID, "Stand-up (moved)", third)

	shareID, _ := createShare(t, tt, "Series And Its Override")
	attached, skipped := attachShareEvents(t, tt, shareID, masterID, overrideID)
	require.Equal(t, 2, attached, "master and override must both publish")
	require.Equal(t, 0, skipped)

	view, raw := readShareEditor(t, tt, shareID)
	require.Len(t, view.Events, 2)
	for _, e := range view.Events {
		assert.Empty(t, e.UnpublishedOverrides,
			"a share drawing every occurrence where it happens has nothing to be warned about")
	}
	assert.NotContains(t, string(raw), "unpublishedOverrides",
		"a share with nothing wrong must stay byte-identical to what it returned before the "+
			"field existed, so its presence reads as a statement rather than as noise")
}

// TestGetPublicShare_ConfidentialReplacementIsStillReported is the case a
// naive reading gets wrong.
//
// The override here is attached: a link row exists, and an implementation
// that decided "the share publishes it" by looking for that row would find
// one and report nothing. But the render query refuses a confidential
// event, so the page never draws it and keeps drawing the occurrence at
// its original start — the very disagreement this field names, on the one
// share the editor cannot fix by attaching, since the attach path refuses
// a confidential event too. The warning is the only thing that will ever
// mention it, and the visibility is what tells the editor not to offer an
// attach.
func TestGetPublicShare_ConfidentialReplacementIsStillReported(t *testing.T) {
	bootstrap(t)
	t.Parallel()

	tt := newTenant(t)
	calID := createCalendar(t, tt)

	first := time.Date(2028, 6, 5, 10, 0, 0, 0, time.UTC)
	masterID := weeklySeries(t, tt, calID, first)
	third := first.AddDate(0, 0, 14)
	overrideID := overrideOneOccurrence(t, tt, tt.AccessToken, calID, masterID, "Stand-up (moved)", third)

	shareID, _ := createShare(t, tt, "Series With A Confidential Week")
	attached, _ := attachShareEvents(t, tt, shareID, masterID, overrideID)
	require.Equal(t, 2, attached, "both publish while the override is still attachable")

	before, _ := readShareEditor(t, tt, shareID)
	require.Empty(t, findEditorEvent(t, before.Events, masterID).UnpublishedOverrides,
		"precondition: with both halves drawn there is nothing to report")

	// Turn the attached override confidential. The occurrence patch reuses
	// the row it already wrote, so the link survives and the share still
	// claims to publish it.
	var reclassified eventView
	helpers.DoJSON(t, http.MethodPatch, tt.WsPath("calendars", calID, "events", masterID), tt.AccessToken, map[string]any{
		"scope":           "occurrence",
		"occurrenceStart": third.Unix(),
		"visibility":      "confidential",
	}, &reclassified)
	require.Equal(t, overrideID, reclassified.ID,
		"the patch must reuse the existing override, or the link this case turns on is gone")

	view, _ := readShareEditor(t, tt, shareID)

	// The editor still lists it — the listing does not filter by
	// visibility, so a confidential event that reached a share can be
	// detached — which is exactly why its presence there is not evidence
	// the page draws it.
	listed := findEditorEvent(t, view.Events, overrideID)
	assert.Equal(t, "confidential", listed.Visibility)

	master := findEditorEvent(t, view.Events, masterID)
	require.Len(t, master.UnpublishedOverrides, 1,
		"an attached override the page cannot draw leaves the occurrence advertised at a time "+
			"it no longer happens, and a link row is not a reason to stay silent")

	warning := master.UnpublishedOverrides[0]
	assert.Equal(t, overrideID, warning.EventID)
	assert.Equal(t, third.Unix(), warning.OriginalStart)
	assert.Equal(t, "confidential", warning.Visibility,
		"the editor must be able to tell an override it may publish from one it cannot, "+
			"because the attach path refuses this row")
}

// TestGetPublicShare_NoRecurringEventsOmitsField keeps the field off a
// share that has no series at all, so a client can read its absence as
// "nothing to report" rather than having to distinguish an empty array.
func TestGetPublicShare_NoRecurringEventsOmitsField(t *testing.T) {
	bootstrap(t)
	t.Parallel()

	tt := newTenant(t)
	calID := createCalendar(t, tt)

	start := time.Date(2028, 7, 4, 10, 0, 0, 0, time.UTC)
	var evt struct {
		ID string `json:"id"`
	}
	helpers.DoJSON(t, http.MethodPost, tt.WsPath("calendars", calID, "events"), tt.AccessToken, map[string]any{
		"kind":     "event",
		"title":    "One-off Kickoff",
		"startAt":  start.Unix(),
		"endAt":    start.Add(time.Hour).Unix(),
		"timezone": "UTC",
	}, &evt)
	require.NotEmpty(t, evt.ID)

	shareID, _ := createShare(t, tt, "No Series Here")
	attached, _ := attachShareEvents(t, tt, shareID, evt.ID)
	require.Equal(t, 1, attached)

	view, raw := readShareEditor(t, tt, shareID)
	require.Len(t, view.Events, 1)
	assert.Nil(t, view.Events[0].UnpublishedOverrides)

	var body struct {
		Events []map[string]json.RawMessage `json:"events"`
	}
	require.NoError(t, json.Unmarshal(raw, &body))
	require.Len(t, body.Events, 1)
	_, present := body.Events[0]["unpublishedOverrides"]
	assert.False(t, present,
		"the field must be absent rather than an empty array, so its presence carries the "+
			"whole meaning")
}
