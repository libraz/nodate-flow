// Calendar role refusals, read as the person receiving them.
//
// A 403 on a calendar is a message the caller acts on: they are already a
// member, so what they lack is a grant, and the code tells them which one
// to go and ask for. Three different floors are enforced on calendars —
// editor to change the contents, manager to change the calendar or its
// membership, owner to delete it — so a refusal that names one role for
// all three sends two callers out of three to request the wrong thing.
package e2e

import (
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/libraz/nodate-flow/apps/flow-api/tests/helpers"
)

// setCalendarMemberRole raises or lowers a member's role on a calendar
// using the host's own grant, and asserts the change was accepted.
func setCalendarMemberRole(t *testing.T, host *helpers.TestTenant, calID, userPublicID, role string) {
	t.Helper()
	var updated struct {
		Updated bool `json:"updated"`
	}
	doJSON(t, http.MethodPatch,
		testServerURL+"/workspaces/"+host.WorkspacePublicID+"/calendars/"+calID+"/members/"+userPublicID,
		host.AccessToken, map[string]any{"role": role}, &updated)
}

// TestCalendarRefusalNamesTheRoleThatWouldAdmit walks one member up the
// role ladder. At each rung the refusal has to name the next grant, and
// receiving that grant has to open exactly the operation that was refused
// — so an implementation that simply denies everyone, or that names one
// role everywhere, fails on a different leg than one that grants too much.
func TestCalendarRefusalNamesTheRoleThatWouldAdmit(t *testing.T) {
	bootstrap(t)
	t.Parallel()

	host := newTenant(t)
	member := inviteAndJoinWorkspace(t, host)
	calID := createCalendarMut(t, host, "Role Ladder Calendar")
	base := testServerURL + "/workspaces/" + host.WorkspacePublicID + "/calendars/" + calID

	start := time.Date(2027, 9, 14, 10, 0, 0, 0, time.UTC)
	newEvent := map[string]any{
		"kind":     "event",
		"title":    "Ladder event",
		"startAt":  start.Unix(),
		"endAt":    start.Add(time.Hour).Unix(),
		"timezone": "UTC",
	}

	// A viewer changing the calendar's contents needs editor, and that is
	// what they are told.
	addCalendarMemberWithRole(t, host, calID, member.Email, "viewer")
	status, body := doJSONStatus(t, http.MethodPost, base+"/events", member.AccessToken, newEvent)
	requireDenied(t, status, body, http.StatusForbidden, "CALENDAR.CALENDAR.EDITOR_ROLE_REQUIRED",
		"a calendar viewer creating an event")

	// The grant the refusal named opens the operation it was refused for.
	setCalendarMemberRole(t, host, calID, member.UserPublicID, "editor")
	var created struct {
		ID string `json:"id"`
	}
	doJSON(t, http.MethodPost, base+"/events", member.AccessToken, newEvent, &created)
	require.NotEmpty(t, created.ID, "the role the refusal asked for must be the role that admits")

	// An editor changing the calendar itself needs manager. Editing the
	// name, colour or cover is administration rather than use, so the
	// grant that opened the contents does not open this.
	status, body = doJSONStatus(t, http.MethodPatch, base, member.AccessToken,
		map[string]any{"name": "Renamed by an editor"})
	requireDenied(t, status, body, http.StatusForbidden, "CALENDAR.CALENDAR.MANAGER_ROLE_REQUIRED",
		"a calendar editor renaming the calendar")

	// Adding somebody to the calendar is the other half of that floor.
	status, body = doJSONStatus(t, http.MethodPost, base+"/members", member.AccessToken,
		map[string]any{"email": host.Email, "role": "viewer"})
	requireDenied(t, status, body, http.StatusForbidden, "CALENDAR.CALENDAR.MANAGER_ROLE_REQUIRED",
		"a calendar editor changing who may reach the calendar")

	setCalendarMemberRole(t, host, calID, member.UserPublicID, "manager")
	doJSON(t, http.MethodPatch, base, member.AccessToken,
		map[string]any{"name": "Renamed by a manager"}, nil)

	// Deleting the calendar is the one thing a manager does not get:
	// managers curate membership and contents, owners decide whether the
	// calendar exists. This is the call site that genuinely requires an
	// owner, and the only one entitled to say so.
	status, body = doJSONStatus(t, http.MethodDelete, base, member.AccessToken, nil)
	requireDenied(t, status, body, http.StatusForbidden, "CALENDAR.CALENDAR.OWNER_ROLE_REQUIRED",
		"a calendar manager deleting the calendar")

	// The calendar survived every refusal above.
	var stillThere int
	require.NoError(t, testDB.QueryRow(
		`SELECT COUNT(*) FROM calendars WHERE public_id = UUID_TO_BIN(?, 0) AND enabled = TRUE`,
		calID).Scan(&stillThere))
	require.Equal(t, 1, stillThere, "a refused delete must leave the calendar live")
}
