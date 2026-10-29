package calendars_test

import (
	"database/sql"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/libraz/nodate-flow/apps/flow-api/tests/helpers"
	"github.com/libraz/nodate-flow/packages/go-shared/eventbus"
	"github.com/libraz/nodate-flow/packages/go-shared/testhelpers"
)

// notDrivenByThisTest are the registry kinds with a subject that no REST
// route in this suite emits: the attachment pair needs the object-store
// harness (covered with the storage e2e), the deleted and attendee
// update kinds have no REST writer here.
var notDrivenByThisTest = map[string]string{
	string(eventbus.CalEventDeleted):           "emitted only by the itemkit dual-write",
	string(eventbus.CalEventAttachmentCreated): "needs object storage",
	string(eventbus.CalEventAttachmentDeleted): "needs object storage",
	"calendar.event.attendee.updated":          "registered for readers; no writer",
	string(eventbus.CalMemoCompleted):          "the memo patch appends calendar.memo.updated",
}

// newestSubject returns the subject of the newest events row of the given
// type in the workspace. The subject is "" when the column is NULL.
func newestSubject(t *testing.T, db *sql.DB, wsPublicID string, kind eventbus.Kind) (found bool, subject string) {
	t.Helper()
	var sub sql.NullString
	err := db.QueryRow(`
		SELECT BIN_TO_UUID(e.subject_public_id, 0)
		FROM events e
		INNER JOIN workspaces w ON w.id = e.workspace_id
		WHERE w.public_id = UUID_TO_BIN(?, 0) AND e.type = ?
		ORDER BY e.id DESC LIMIT 1`, wsPublicID, string(kind)).Scan(&sub)
	if err == sql.ErrNoRows {
		return false, ""
	}
	require.NoError(t, err)
	return true, sub.String
}

// TestEventSubjectIsTheTargetsPublicID drives every subject-bearing kind
// the REST handlers emit and checks the newest row of that kind carries the
// target's public_id in events.subject_public_id. Which entity is the
// target comes from the registry's subject table, and a kind with no
// subject must leave the column NULL.
func TestEventSubjectIsTheTargetsPublicID(t *testing.T) {
	testhelpers.SkipUnlessIntegration(t)

	inst := helpers.StartShared(t)
	helpers.RegisterCleanupDB(inst.DB)
	srv := helpers.StartTestServer(t, inst.DB)

	owner := helpers.CreateCalendarTestTenant(t, srv)
	wsID := owner.WorkspacePublicID.String()
	ws := srv.BaseURL + "/workspaces/" + wsID

	call := func(method, path string, token string, body any, out any) {
		helpers.DoJSON(t, method, ws+path, token, body, out)
	}

	var cal struct {
		ID string `json:"id"`
	}
	call(http.MethodPost, "/calendars", owner.AccessToken,
		map[string]any{"kind": "personal", "name": "Subject calendar", "color": "#4285F4"}, &cal)
	calPath := "/calendars/" + cal.ID
	guest := helpers.CreateExtraCalendarMember(t, srv, owner,
		helpers.ResolveCalendarInternalID(t, srv.DB, cal.ID), "editor")

	start := time.Date(2027, 6, 1, 10, 0, 0, 0, time.UTC)
	var evt struct {
		ID string `json:"id"`
	}
	call(http.MethodPost, calPath+"/events", owner.AccessToken, map[string]any{
		"kind": "event", "title": "Subject event",
		"startAt": start.Unix(), "endAt": start.Add(time.Hour).Unix(), "timezone": "UTC",
	}, &evt)
	evtPath := calPath + "/events/" + evt.ID

	var memo struct {
		ID string `json:"id"`
	}
	var comment, item, invitation struct {
		ID string `json:"id"`
	}
	var attendees struct {
		Attendees []struct {
			ID string `json:"id"`
		} `json:"attendees"`
	}

	// target names the entity whose public_id the registry says the row
	// carries, by the table the registry names.
	target := func(kind eventbus.Kind) string {
		switch eventbus.SubjectOf(kind) {
		case eventbus.SubjectCalendarEvents:
			return evt.ID
		case eventbus.SubjectCalendarMemos:
			return memo.ID
		}
		require.Failf(t, "unmapped subject table", "%s names %q", kind, eventbus.SubjectOf(kind))
		return ""
	}

	// Each step performs one action; the kind it must have appended is
	// checked right after it.
	steps := []struct {
		kind eventbus.Kind
		do   func()
	}{
		{eventbus.CalEventCreated, func() {}},
		{eventbus.CalEventUpdated, func() {
			call(http.MethodPatch, evtPath, owner.AccessToken, map[string]any{"title": "Renamed"}, nil)
		}},
		{eventbus.CalEventCommentCreated, func() {
			call(http.MethodPost, evtPath+"/comments", owner.AccessToken, map[string]any{"body": "first"}, &comment)
		}},
		{eventbus.CalEventCommentUpdated, func() {
			call(http.MethodPatch, evtPath+"/comments/"+comment.ID, owner.AccessToken, map[string]any{"body": "edited"}, nil)
		}},
		{eventbus.CalEventCommentDeleted, func() {
			call(http.MethodDelete, evtPath+"/comments/"+comment.ID, owner.AccessToken, nil, nil)
		}},
		{eventbus.CalEventChecklistCreated, func() {
			call(http.MethodPost, evtPath+"/checklist", owner.AccessToken, map[string]any{"title": "step"}, &item)
		}},
		{eventbus.CalEventChecklistUpdated, func() {
			call(http.MethodPatch, evtPath+"/checklist/"+item.ID, owner.AccessToken, map[string]any{"done": true}, nil)
		}},
		{eventbus.CalEventChecklistDeleted, func() {
			call(http.MethodDelete, evtPath+"/checklist/"+item.ID, owner.AccessToken, nil, nil)
		}},
		{eventbus.CalEventAttendeeAdded, func() {
			call(http.MethodPost, evtPath+"/attendees", owner.AccessToken,
				map[string]any{"userIds": []string{guest.UserPublicID.String()}}, &attendees)
			require.Len(t, attendees.Attendees, 1)
		}},
		{eventbus.CalEventRsvpUpdated, func() {
			call(http.MethodPatch, evtPath+"/attendees/rsvp", guest.AccessToken, map[string]any{"rsvp": "accepted"}, nil)
		}},
		{eventbus.CalEventInviteCreated, func() {
			call(http.MethodPost, evtPath+"/attendees/"+attendees.Attendees[0].ID+"/invite", owner.AccessToken,
				map[string]any{}, &invitation)
		}},
		{eventbus.CalEventInviteRotated, func() {
			call(http.MethodPost, evtPath+"/attendees/"+attendees.Attendees[0].ID+"/invite", owner.AccessToken,
				map[string]any{}, &invitation)
		}},
		{eventbus.CalEventInviteRevoked, func() {
			call(http.MethodDelete, evtPath+"/invites/"+invitation.ID, owner.AccessToken, nil, nil)
		}},
		{eventbus.CalEventAttendeeRemoved, func() {
			call(http.MethodDelete, evtPath+"/attendees/"+guest.UserPublicID.String(), owner.AccessToken, nil, nil)
		}},
		{eventbus.CalMemoCreated, func() {
			call(http.MethodPost, calPath+"/memos", owner.AccessToken, map[string]any{"title": "memo", "body": "note"}, &memo)
		}},
		{eventbus.CalMemoUpdated, func() {
			call(http.MethodPatch, calPath+"/memos/"+memo.ID, owner.AccessToken, map[string]any{"done": true}, nil)
		}},
		{eventbus.CalMemoDeleted, func() {
			call(http.MethodDelete, calPath+"/memos/"+memo.ID, owner.AccessToken, nil, nil)
		}},
	}

	driven := map[eventbus.Kind]bool{}
	for _, step := range steps {
		step.do()
		driven[step.kind] = true

		require.NotEmptyf(t, eventbus.SubjectOf(step.kind), "%s is in this test but its registry entry names no subject", step.kind)
		found, got := newestSubject(t, inst.DB, wsID, step.kind)
		require.Truef(t, found, "%s: no events row after the action", step.kind)
		require.Equalf(t, target(step.kind), got, "%s: subject_public_id is not the target's public_id", step.kind)
	}

	// A kind with no subject writes NULL.
	call(http.MethodPatch, calPath, owner.AccessToken, map[string]any{"name": "Renamed calendar"}, nil)
	require.Empty(t, eventbus.SubjectOf(eventbus.CalendarUpdated))
	found, got := newestSubject(t, inst.DB, wsID, eventbus.CalendarUpdated)
	require.True(t, found, "calendar.updated: no events row after the action")
	require.Empty(t, got, "calendar.updated has no subject, so the column must stay NULL")

	// Every registry kind with a subject is either driven above or named
	// here with the reason it is not, so a new kind cannot go unchecked.
	for _, name := range eventbus.CoreKindNames() {
		kind := eventbus.Kind(name)
		if eventbus.SubjectOf(kind) == "" || driven[kind] {
			continue
		}
		_, skipped := notDrivenByThisTest[name]
		require.Truef(t, skipped, "%s names a subject in the registry but this test neither drives nor excuses it", kind)
	}
}
