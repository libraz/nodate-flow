package e2e

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/libraz/nodate-flow/apps/flow-api/tests/helpers"
)

type memberColorJSON struct {
	UserID      string `json:"userId"`
	MemberColor string `json:"memberColor"`
	Role        string `json:"role"`
}

func memberColorURL(tt *helpers.TestTenant, calID, userPublicID string) string {
	return labelCalURL(tt, calID) + "/members/" + userPublicID + "/color"
}

func putMemberColor(t *testing.T, actor *helpers.TestTenant, calID, userPublicID, color string) (int, []byte) {
	t.Helper()
	return doJSONStatus(t, http.MethodPut, memberColorURL(actor, calID, userPublicID), actor.AccessToken,
		map[string]any{"color": color})
}

func memberColorOfUser(t *testing.T, viewer *helpers.TestTenant, calID, userPublicID string) string {
	t.Helper()
	var out struct {
		Members []memberColorJSON `json:"members"`
	}
	doJSON(t, http.MethodGet, labelCalURL(viewer, calID)+"/members", viewer.AccessToken, nil, &out)
	for _, m := range out.Members {
		if m.UserID == userPublicID {
			return m.MemberColor
		}
	}
	t.Fatalf("user %s is not a member of %s", userPublicID, calID)
	return ""
}

// addColourMember joins a fresh workspace member to the calendar as a
// viewer and returns it with the colour the add handed out.
func addColourMember(t *testing.T, host *helpers.TestTenant, calID string) (*helpers.TestTenant, string) {
	t.Helper()
	member := inviteAndJoinWorkspace(t, host)
	var added memberColorJSON
	doJSON(t, http.MethodPost, labelCalURL(host, calID)+"/members", host.AccessToken,
		map[string]any{"email": member.Email, "role": "viewer"}, &added)
	return member, added.MemberColor
}

func TestCalendarMemberColor(t *testing.T) {
	bootstrap(t)
	t.Parallel()

	t.Run("permissions_and_input", func(t *testing.T) {
		t.Parallel()
		f := newLabelFixture(t)
		owner := f.owner
		viewer := f.addMember(t, "viewer")
		editor := f.addMember(t, "editor")
		manager := f.addMember(t, "manager")
		outsider := inviteAndJoinWorkspace(t, owner)

		status, raw := putMemberColor(t, viewer, f.calID, viewer.UserPublicID, "#00ff00")
		require.Equal(t, http.StatusOK, status, "a viewer changes their own colour: %s", raw)
		var self memberColorJSON
		require.NoError(t, json.Unmarshal(raw, &self))
		require.Equal(t, viewer.UserPublicID, self.UserID)
		require.Equal(t, "#00ff00", self.MemberColor, "the value is stored as sent")
		require.Equal(t, "viewer", self.Role)
		require.Equal(t, "#00ff00", memberColorOfUser(t, owner, f.calID, viewer.UserPublicID))

		status, raw = putMemberColor(t, manager, f.calID, viewer.UserPublicID, "#112233")
		require.Equal(t, http.StatusOK, status, "a manager changes another member's colour: %s", raw)
		require.Equal(t, "#112233", memberColorOfUser(t, owner, f.calID, viewer.UserPublicID))

		status, raw = putMemberColor(t, editor, f.calID, viewer.UserPublicID, "#445566")
		requireDenied(t, status, raw, http.StatusForbidden, "CALENDAR.CALENDAR.MANAGER_ROLE_REQUIRED", "editor changing another member's colour")
		require.Equal(t, "#112233", memberColorOfUser(t, owner, f.calID, viewer.UserPublicID))

		status, raw = putMemberColor(t, owner, f.calID, outsider.UserPublicID, "#445566")
		requireDenied(t, status, raw, http.StatusNotFound, "CALENDAR.MEMBER.NOT_FOUND", "target who is not a calendar member")

		for _, bad := range []string{"#FFF", "red", "#GGGGGG", "112233", "#11223344"} {
			status, raw = putMemberColor(t, owner, f.calID, viewer.UserPublicID, bad)
			require.Equalf(t, http.StatusUnprocessableEntity, status, "colour %q: %s", bad, raw)
		}
		require.Equal(t, "#112233", memberColorOfUser(t, owner, f.calID, viewer.UserPublicID))
	})

	t.Run("same_colour", func(t *testing.T) {
		t.Parallel()
		f := newLabelFixture(t)
		owner := f.owner
		member, current := addColourMember(t, owner, f.calID)
		require.Regexp(t, hexColorPattern, current)
		const kind = "calendar.member.updated"
		before := countCalendarEvents(t, owner.WorkspacePublicID, f.calID, kind)

		for i := 0; i < 2; i++ {
			status, raw := putMemberColor(t, owner, f.calID, member.UserPublicID, current)
			require.Equal(t, http.StatusOK, status, "send %d: %s", i, raw)
			var got memberColorJSON
			require.NoError(t, json.Unmarshal(raw, &got))
			require.Equal(t, current, got.MemberColor)
		}
		require.Equal(t, before, countCalendarEvents(t, owner.WorkspacePublicID, f.calID, kind),
			"sending the current colour records nothing")

		// The comparison is case-sensitive: only the stored text counts as unchanged.
		lower := strings.ToLower(current)
		status, raw := putMemberColor(t, owner, f.calID, member.UserPublicID, lower)
		require.Equal(t, http.StatusOK, status, "body=%s", raw)
		require.Equal(t, lower, memberColorOfUser(t, owner, f.calID, member.UserPublicID))
		require.Equal(t, before+1, countCalendarEvents(t, owner.WorkspacePublicID, f.calID, kind))
	})

	t.Run("effect_and_record", func(t *testing.T) {
		t.Parallel()
		f := newLabelFixture(t)
		owner := f.owner
		evtID := eventIDOf(t, postLabelEvent(t, owner, f.calID, labelEventBody("Member colour "+randomHex(3), labelSeriesStart, nil)))
		requireColorView(t, getLabelEvent(t, owner, f.calID, evtID),
			colorView{Color: labelOwnerColor, OwnerColor: labelOwnerColor}, "before the change")

		status, raw := putMemberColor(t, owner, f.calID, owner.UserPublicID, "#112233")
		require.Equal(t, http.StatusOK, status, "body=%s", raw)
		requireColorView(t, getLabelEvent(t, owner, f.calID, evtID),
			colorView{Color: "#112233", OwnerColor: "#112233"}, "after the change")

		wsID := helpers.ResolveWorkspaceInternalID(t, testDB, owner.WorkspacePublicID)
		calendarID := helpers.ResolveCalendarInternalID(t, testDB, f.calID)
		var payload []byte
		require.NoError(t, testDB.QueryRow(
			`SELECT payload_json FROM events WHERE workspace_id = ? AND calendar_id = ? AND type = 'calendar.member.updated'`,
			wsID, calendarID).Scan(&payload), "exactly one calendar.member.updated row")
		var got map[string]any
		require.NoError(t, json.Unmarshal(payload, &got))
		require.Equal(t, f.calID, got["calendarId"], "public calendar id")
		require.Equal(t, owner.UserPublicID, got["userId"], "public user id")
	})

	t.Run("system_calendar", func(t *testing.T) {
		t.Parallel()
		owner := newTenant(t)
		sysCalID := insertSystemCalendarWithOwner(t, owner)
		status, raw := putMemberColor(t, owner, sysCalID, owner.UserPublicID, "#112233")
		require.Equal(t, http.StatusOK, status, "a member picks their own colour on a system calendar: %s", raw)
		require.Equal(t, "#112233", memberColorOfUser(t, owner, sysCalID, owner.UserPublicID))
	})

	t.Run("assignment", func(t *testing.T) {
		t.Parallel()
		owner := newTenant(t)
		// #4285F4 is not a palette colour, so the owner uses up none of it.
		calID := createLabelCalendar(t, owner, "Palette "+randomHex(3), "#4285F4")

		first, c1 := addColourMember(t, owner, calID)
		_, c2 := addColourMember(t, owner, calID)
		_, c3 := addColourMember(t, owner, calID)
		require.Equal(t, []string{"#47B2F7", "#F35F8C", "#B38BDC"}, []string{c1, c2, c3},
			"unused palette colours are handed out in palette order")

		// Case is ignored when counting; a colour outside the palette is not counted.
		setColour := func(userPublicID, color string) {
			_, err := testDB.Exec(
				`UPDATE calendar_members cm
				   JOIN users u ON u.id = cm.user_id
				   JOIN calendars c ON c.id = cm.calendar_id
				    SET cm.member_color = ?
				  WHERE c.public_id = UUID_TO_BIN(?, 0) AND u.public_id = UUID_TO_BIN(?, 0)`,
				color, calID, userPublicID)
			require.NoError(t, err)
		}
		setColour(first.UserPublicID, "#47b2f7")
		var second struct {
			Members []memberColorJSON `json:"members"`
		}
		doJSON(t, http.MethodGet, labelCalURL(owner, calID)+"/members", owner.AccessToken, nil, &second)
		for _, m := range second.Members {
			if m.MemberColor == "#F35F8C" {
				setColour(m.UserID, "#ABCDEF")
			}
		}
		_, c4 := addColourMember(t, owner, calID)
		require.Equal(t, "#F35F8C", c4,
			"#47B2F7 still counts under another case, and #ABCDEF counts for no palette colour")
	})

	t.Run("input_pattern", func(t *testing.T) {
		t.Parallel()
		owner := newTenant(t)
		wsURL := testServerURL + "/workspaces/" + owner.WorkspacePublicID + "/calendars"
		status, raw := doJSONStatus(t, http.MethodPost, wsURL, owner.AccessToken,
			map[string]any{"kind": "personal", "name": "Bad colour", "color": "red"})
		require.Equal(t, http.StatusUnprocessableEntity, status, "create with a colour name: %s", raw)

		calID := createLabelCalendar(t, owner, "Good colour", "#34A853")
		status, raw = doJSONStatus(t, http.MethodPatch, wsURL+"/"+calID, owner.AccessToken, map[string]any{"color": "red"})
		require.Equal(t, http.StatusUnprocessableEntity, status, "update with a colour name: %s", raw)
	})
}
