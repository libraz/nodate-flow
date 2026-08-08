// Attendance is the authority for the two per-attendee writes on an
// event: RSVP (about the caller) and can_edit (about a named target).
//
// Both endpoints sit behind gates that only prove the event is visible —
// workspace membership, calendar membership, event-owner for the grant.
// None of them says the person in question is on the attendee list, and
// the UPDATE cannot say it either: the connection does not set
// CLIENT_FOUND_ROWS, so its count reports changed rows rather than
// matched ones, and someone re-submitting the value they already hold
// looks exactly like someone writing into nothing. The tests below pin
// both ends of that: a non-attendee is refused with a specific code, and
// an attendee re-submitting an unchanged value still succeeds.
package e2e

import (
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/libraz/nodate-flow/apps/flow-api/tests/helpers"
)

// attendeeNotFoundCode is the catalogue code both endpoints answer with
// when the person named by the request holds no live attendee row on the
// event.
const attendeeNotFoundCode = "CALENDAR.ATTENDEE.NOT_FOUND"

// attendeeView is the subset of AttendeeResponse these tests assert on.
type attendeeView struct {
	UserID  string `json:"userId"`
	Rsvp    string `json:"rsvp"`
	CanEdit bool   `json:"canEdit"`
}

// createEventInWindow creates a one-hour event at the given start so each test
// owns a distinct window and parallel runs cannot collide on overlap.
func createEventInWindow(t *testing.T, owner *helpers.TestTenant, calID, title string, start time.Time) string {
	t.Helper()
	var resp struct {
		ID string `json:"id"`
	}
	doJSON(t, http.MethodPost,
		testServerURL+"/workspaces/"+owner.WorkspacePublicID+"/calendars/"+calID+"/events",
		owner.AccessToken, map[string]any{
			"kind":     "event",
			"title":    title,
			"startAt":  start.Unix(),
			"endAt":    start.Add(time.Hour).Unix(),
			"timezone": "UTC",
		}, &resp)
	require.NotEmpty(t, resp.ID, "create event must return a public id")
	return resp.ID
}

// listAttendeesAs reads the event's attendee list as the given actor and
// returns it keyed by user public id.
func listAttendeesAs(t *testing.T, actor *helpers.TestTenant, calID, evtID string) map[string]attendeeView {
	t.Helper()
	var listed struct {
		Attendees []attendeeView `json:"attendees"`
	}
	doJSON(t, http.MethodGet,
		testServerURL+"/workspaces/"+actor.WorkspacePublicID+
			"/calendars/"+calID+"/events/"+evtID+"/attendees",
		actor.AccessToken, nil, &listed)
	byUser := make(map[string]attendeeView, len(listed.Attendees))
	for _, a := range listed.Attendees {
		byUser[a.UserID] = a
	}
	return byUser
}

// patchRsvp sends the self-RSVP PATCH as the given actor.
func patchRsvp(t *testing.T, actor *helpers.TestTenant, calID, evtID, rsvp string) (int, []byte) {
	t.Helper()
	return doJSONStatus(t, http.MethodPatch,
		testServerURL+"/workspaces/"+actor.WorkspacePublicID+
			"/calendars/"+calID+"/events/"+evtID+"/attendees/rsvp",
		actor.AccessToken, map[string]any{"rsvp": rsvp})
}

// patchCanEdit sends the can_edit grant for a named target user.
func patchCanEdit(
	t *testing.T,
	actor *helpers.TestTenant,
	calID, evtID, targetUserID string,
	canEdit bool,
) (int, []byte) {
	t.Helper()
	return doJSONStatus(t, http.MethodPatch,
		testServerURL+"/workspaces/"+actor.WorkspacePublicID+
			"/calendars/"+calID+"/events/"+evtID+"/attendees/"+targetUserID+"/can-edit",
		actor.AccessToken, map[string]any{"canEdit": canEdit})
}

// requireUpdated asserts a 200 carrying updated=true.
func requireUpdated(t *testing.T, status int, raw []byte, label string) {
	t.Helper()
	require.Equalf(t, http.StatusOK, status, "%s: body=%s", label, string(raw))
	assert.Containsf(t, string(raw), `"updated":true`,
		"%s: handler must report the write as applied; body=%s", label, string(raw))
}

// countRsvpActivityFor returns how many calendar.event.rsvp.updated rows
// the log holds for the given actor. A refused RSVP must leave none: the
// activity feed is what a workspace reads to believe a response was
// recorded, so an entry with no attendee row behind it is a false record
// of someone having answered.
func countRsvpActivityFor(t *testing.T, userPublicID string) int {
	t.Helper()
	var n int
	require.NoError(t, testDB.QueryRow(
		`SELECT COUNT(*)
		 FROM events e
		 JOIN users u ON u.id = e.actor_user_id
		 WHERE e.type = 'calendar.event.rsvp.updated'
		   AND u.public_id = UUID_TO_BIN(?, 0)`,
		userPublicID).Scan(&n))
	return n
}

// TestEventRsvpRequiresAttendance covers the RSVP endpoint.
//
// Seeing an event is not being invited to it. A workspace member who is
// also a calendar member clears every gate the handler applies before
// the write, so without an explicit attendance check they receive
// updated=true — and an activity entry saying they responded — while no
// RSVP is recorded anywhere.
func TestEventRsvpRequiresAttendance(t *testing.T) {
	bootstrap(t)
	t.Parallel()

	owner := newTenant(t)
	calID := createCalendarMut(t, owner, "RSVP Authority Cal")
	evtID := createEventInWindow(t, owner, calID, "RSVP Authority Event",
		time.Date(2027, 7, 12, 9, 0, 0, 0, time.UTC))

	// Both users reach the calendar the same way. The only difference
	// between them is the attendee row, which is exactly the axis under
	// test — a refusal that came from the calendar ACL instead would show
	// up as a different status and code.
	attendee := inviteAndJoinWorkspace(t, owner)
	bystander := inviteAndJoinWorkspace(t, owner)
	addCalendarMemberByEmail(t, owner, calID, attendee.Email, "viewer")
	addCalendarMemberByEmail(t, owner, calID, bystander.Email, "viewer")
	addAttendeeMut(t, owner, calID, evtID, attendee.UserPublicID)

	// The bystander can read the event's attendee list, so the refusal
	// below is about attendance and not about visibility.
	before := listAttendeesAs(t, bystander, calID, evtID)
	require.Contains(t, before, attendee.UserPublicID, "the attendee must be on the list")
	require.NotContains(t, before, bystander.UserPublicID, "the bystander must not be on the list")
	require.Equal(t, "pending", before[attendee.UserPublicID].Rsvp,
		"a freshly added attendee starts pending")

	rsvpActivityBefore := countRsvpActivityFor(t, bystander.UserPublicID)

	t.Run("non_attendee_refused", func(t *testing.T) {
		status, raw := patchRsvp(t, bystander, calID, evtID, "accepted")
		requireDenied(t, status, raw, http.StatusNotFound, attendeeNotFoundCode,
			"rsvp from a calendar member who is not an attendee")

		after := listAttendeesAs(t, owner, calID, evtID)
		assert.NotContains(t, after, bystander.UserPublicID,
			"a refused RSVP must not conjure an attendee row")
		assert.Equal(t, "pending", after[attendee.UserPublicID].Rsvp,
			"a refused RSVP must not touch anyone else's response")
		assert.Equal(t, rsvpActivityBefore, countRsvpActivityFor(t, bystander.UserPublicID),
			"a refused RSVP must not record the caller as having responded")
	})

	t.Run("attendee_accepted", func(t *testing.T) {
		status, raw := patchRsvp(t, attendee, calID, evtID, "accepted")
		requireUpdated(t, status, raw, "rsvp from a genuine attendee")

		after := listAttendeesAs(t, owner, calID, evtID)
		require.Contains(t, after, attendee.UserPublicID)
		assert.Equal(t, "accepted", after[attendee.UserPublicID].Rsvp,
			"the attendee's response must be recorded")
	})

	t.Run("attendee_resubmits_same_rsvp", func(t *testing.T) {
		// Re-sending the response already on file changes no row. That is
		// the caller getting what they asked for, not a missing attendee,
		// so an existence check driven by the affected-row count would
		// turn a settled RSVP into a 404 the second time the user clicks.
		status, raw := patchRsvp(t, attendee, calID, evtID, "accepted")
		requireUpdated(t, status, raw, "attendee re-submitting an unchanged rsvp")

		after := listAttendeesAs(t, owner, calID, evtID)
		assert.Equal(t, "accepted", after[attendee.UserPublicID].Rsvp,
			"re-submitting must leave the recorded response as it was")
	})

	t.Run("attendee_changes_rsvp", func(t *testing.T) {
		status, raw := patchRsvp(t, attendee, calID, evtID, "declined")
		requireUpdated(t, status, raw, "attendee changing their rsvp")

		after := listAttendeesAs(t, owner, calID, evtID)
		assert.Equal(t, "declined", after[attendee.UserPublicID].Rsvp,
			"changing the response must take effect")
	})
}

// TestAttendeeCanEditRequiresAttendance covers the can_edit grant.
//
// The endpoint resolves the target's public id into an internal one,
// which proves only that the user exists somewhere. Granting edit rights
// against a user who holds no attendee row on this event writes nothing
// and reports success, leaving the owner believing they handed out a
// permission that does not exist.
func TestAttendeeCanEditRequiresAttendance(t *testing.T) {
	bootstrap(t)
	t.Parallel()

	owner := newTenant(t)
	calID := createCalendarMut(t, owner, "CanEdit Authority Cal")
	evtID := createEventInWindow(t, owner, calID, "CanEdit Authority Event",
		time.Date(2027, 7, 13, 9, 0, 0, 0, time.UTC))

	attendee := inviteAndJoinWorkspace(t, owner)
	bystander := inviteAndJoinWorkspace(t, owner)
	addCalendarMemberByEmail(t, owner, calID, attendee.Email, "viewer")
	addCalendarMemberByEmail(t, owner, calID, bystander.Email, "viewer")
	addAttendeeMut(t, owner, calID, evtID, attendee.UserPublicID)

	seeded := listAttendeesAs(t, owner, calID, evtID)
	require.Contains(t, seeded, attendee.UserPublicID)
	require.NotContains(t, seeded, bystander.UserPublicID)
	require.False(t, seeded[attendee.UserPublicID].CanEdit,
		"a freshly added attendee holds no edit rights")

	t.Run("non_attendee_target_refused", func(t *testing.T) {
		// The target is a real user and a member of both the workspace and
		// the calendar, so the only thing missing is the attendee row. A
		// generic "user not found" here would be the wrong answer and is
		// pinned by the code assertion.
		status, raw := patchCanEdit(t, owner, calID, evtID, bystander.UserPublicID, true)
		requireDenied(t, status, raw, http.StatusNotFound, attendeeNotFoundCode,
			"can-edit grant aimed at a workspace user who is not an attendee")

		after := listAttendeesAs(t, owner, calID, evtID)
		assert.NotContains(t, after, bystander.UserPublicID,
			"a refused grant must not conjure an attendee row")
		assert.False(t, after[attendee.UserPublicID].CanEdit,
			"a refused grant must not spill onto another attendee")
	})

	t.Run("attendee_target_granted", func(t *testing.T) {
		status, raw := patchCanEdit(t, owner, calID, evtID, attendee.UserPublicID, true)
		requireUpdated(t, status, raw, "can-edit grant aimed at a real attendee")

		after := listAttendeesAs(t, owner, calID, evtID)
		require.Contains(t, after, attendee.UserPublicID)
		assert.True(t, after[attendee.UserPublicID].CanEdit,
			"the grant must be recorded on the attendee row")
	})

	t.Run("attendee_target_regranted", func(t *testing.T) {
		// Re-granting rights the attendee already holds changes no row.
		// Reading that as "target not on the list" would make the second
		// click on an already-enabled toggle answer 404.
		status, raw := patchCanEdit(t, owner, calID, evtID, attendee.UserPublicID, true)
		requireUpdated(t, status, raw, "re-granting can-edit to an attendee who already holds it")

		after := listAttendeesAs(t, owner, calID, evtID)
		assert.True(t, after[attendee.UserPublicID].CanEdit,
			"re-granting must leave the permission in place")
	})

	t.Run("attendee_target_revoked", func(t *testing.T) {
		status, raw := patchCanEdit(t, owner, calID, evtID, attendee.UserPublicID, false)
		requireUpdated(t, status, raw, "revoking can-edit from an attendee")

		after := listAttendeesAs(t, owner, calID, evtID)
		assert.False(t, after[attendee.UserPublicID].CanEdit,
			"revoking must take effect")
	})
}
