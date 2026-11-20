// Attendees are calendar members: AddAttendees writes a row only for a
// workspace user who also holds the calendar, and records one log row per
// call that added someone, naming the rows added in that call.
package e2e

import (
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/libraz/nodate-flow/apps/flow-api/tests/helpers"
)

// addAttendeesAs posts the given users and returns the user ids echoed back.
func addAttendeesAs(t *testing.T, owner *helpers.TestTenant, calID, evtID string, userIDs ...string) []string {
	t.Helper()
	var resp struct {
		Attendees []struct {
			UserID string `json:"userId"`
		} `json:"attendees"`
	}
	doJSON(t, http.MethodPost,
		testServerURL+"/workspaces/"+owner.WorkspacePublicID+"/calendars/"+calID+"/events/"+evtID+"/attendees",
		owner.AccessToken, map[string]any{"userIds": userIDs}, &resp)
	got := make([]string, 0, len(resp.Attendees))
	for _, a := range resp.Attendees {
		got = append(got, a.UserID)
	}
	return got
}

// attendeeAddedPayloads returns the payloads of the event's
// calendar.event.attendee.added rows, oldest first.
func attendeeAddedPayloads(t *testing.T, evtID string) []map[string]any {
	t.Helper()
	rows, err := testDB.Query(
		`SELECT payload_json FROM events
		 WHERE type = 'calendar.event.attendee.added' AND subject_public_id = UUID_TO_BIN(?, 0)
		 ORDER BY id ASC`, evtID)
	require.NoError(t, err)
	defer rows.Close()
	var out []map[string]any
	for rows.Next() {
		var raw []byte
		require.NoError(t, rows.Scan(&raw))
		var p map[string]any
		require.NoError(t, json.Unmarshal(raw, &p))
		out = append(out, p)
	}
	require.NoError(t, rows.Err())
	return out
}

// TestAddAttendeesRequiresCalendarMembership pins who may be written as an
// attendee and what the log says about a call.
func TestAddAttendeesRequiresCalendarMembership(t *testing.T) {
	bootstrap(t)
	t.Parallel()

	owner := newTenant(t)
	calID := createCalendarMut(t, owner, "Attendee Membership Cal")
	evtID := createEventInWindow(t, owner, calID, "Attendee Membership Event",
		time.Date(2027, 8, 3, 9, 0, 0, 0, time.UTC))

	member := inviteAndJoinWorkspace(t, owner)
	outsider := inviteAndJoinWorkspace(t, owner)
	addCalendarMemberByEmail(t, owner, calID, member.Email, "viewer")

	t.Run("workspace_user_off_the_calendar_is_skipped", func(t *testing.T) {
		got := addAttendeesAs(t, owner, calID, evtID, outsider.UserPublicID)
		assert.Empty(t, got, "a non-member must be omitted from the response")
		assert.NotContains(t, listAttendeesAs(t, owner, calID, evtID), outsider.UserPublicID)
		assert.Empty(t, attendeeAddedPayloads(t, evtID), "nothing was added, so nothing is logged")
	})

	t.Run("mixed_call_logs_only_the_rows_added", func(t *testing.T) {
		got := addAttendeesAs(t, owner, calID, evtID, member.UserPublicID, outsider.UserPublicID)
		assert.Equal(t, []string{member.UserPublicID}, got)

		logged := attendeeAddedPayloads(t, evtID)
		require.Len(t, logged, 1)
		assert.EqualValues(t, 1, logged[0]["count"])
		assert.Equal(t, []any{member.UserPublicID}, logged[0]["userIds"])
		assert.Equal(t, member.DisplayName, logged[0]["summary"])
	})

	t.Run("repeat_call_adds_nothing_and_logs_nothing", func(t *testing.T) {
		got := addAttendeesAs(t, owner, calID, evtID, member.UserPublicID)
		assert.Equal(t, []string{member.UserPublicID}, got, "a listed member is echoed back")
		assert.Len(t, attendeeAddedPayloads(t, evtID), 1, "an echo is not a change")
	})

	t.Run("departed_attendee_is_omitted", func(t *testing.T) {
		res, err := testDB.Exec(
			`UPDATE calendar_members cm
			   JOIN calendars c ON c.id = cm.calendar_id
			   JOIN users u ON u.id = cm.user_id
			    SET cm.enabled = FALSE
			  WHERE c.public_id = UUID_TO_BIN(?, 0) AND u.public_id = UUID_TO_BIN(?, 0)`,
			calID, member.UserPublicID)
		require.NoError(t, err)
		n, err := res.RowsAffected()
		require.NoError(t, err)
		require.EqualValues(t, 1, n, "the fixture must actually depart the member")

		got := addAttendeesAs(t, owner, calID, evtID, member.UserPublicID)
		assert.Empty(t, got, "a listed attendee who has since left is omitted, not echoed")
		assert.Len(t, attendeeAddedPayloads(t, evtID), 1)
	})
}
