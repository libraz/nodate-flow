// One calendar rule, one answer, whichever transport asked.
//
// A refusal that differs between the web app and an agent is a rule that
// exists twice, and the copy nobody is looking at is the one that drifts:
// the surface people use keeps working, so the divergence is invisible
// from either side alone. These tests drive both surfaces against the same
// rows and compare the code a client receives.
package e2e

import (
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/libraz/nodate-flow/apps/flow-api/tests/helpers"
)

// insertSystemCalendarWithOwner creates a provider-fed calendar and puts
// the tenant on it at the strongest role the enum has.
//
// A system calendar is populated by the platform rather than by an API
// call, so the row is written directly; the owner grant is what makes the
// refusal that follows attributable to the calendar's kind alone.
func insertSystemCalendarWithOwner(t *testing.T, tt *helpers.TestTenant) string {
	t.Helper()
	var calID string
	require.NoError(t, testDB.QueryRow(`SELECT UUID()`).Scan(&calID))
	_, err := testDB.Exec(
		`INSERT INTO calendars (public_id, workspace_id, kind, name, color, system_slug)
		 VALUES (UUID_TO_BIN(?, 0),
		         (SELECT id FROM workspaces WHERE public_id = UUID_TO_BIN(?, 0)),
		         'system', 'Parity Holidays', '#EA4335', ?)`,
		calID, tt.WorkspacePublicID, "parity."+randomHex(6))
	require.NoError(t, err)
	_, err = testDB.Exec(
		`INSERT INTO calendar_members (public_id, workspace_id, calendar_id, user_id, role, member_color)
		 VALUES (UUID_TO_BIN(UUID(), 0),
		         (SELECT id FROM workspaces WHERE public_id = UUID_TO_BIN(?, 0)),
		         (SELECT id FROM calendars WHERE public_id = UUID_TO_BIN(?, 0)),
		         (SELECT id FROM users WHERE public_id = UUID_TO_BIN(?, 0)),
		         'owner', '#EA4335')`,
		tt.WorkspacePublicID, calID, tt.UserPublicID)
	require.NoError(t, err)
	return calID
}

// restErrorCode returns the catalogue code a REST refusal carries,
// alongside its status, so a comparison against MCP is made on the value a
// client actually reads.
func restErrorCode(t *testing.T, method, url, bearer string, body any) (int, string) {
	t.Helper()
	status, raw := doJSONStatus(t, method, url, bearer, body)
	require.GreaterOrEqualf(t, status, 400, "expected a refusal from %s %s; body=%s", method, url, string(raw))
	return status, decodeErrorCode(t, raw)
}

// TestCalendarRefusalParityAcrossTransports covers the situations both
// transports can reach: a caller with no membership, a member below the
// write floor, and a system calendar. Each is asserted as an equality
// between the two answers as well as against the expected code, so a
// change that moves both together is still reported rather than silently
// accepted.
func TestCalendarRefusalParityAcrossTransports(t *testing.T) {
	bootstrap(t)
	t.Parallel()

	host := newTenant(t)
	outsider := inviteAndJoinWorkspace(t, host)
	calID := createCalendarMut(t, host, "Parity Calendar")
	evtID := createEventMut(t, host, calID, "Parity event")

	start := time.Date(2027, 10, 4, 10, 0, 0, 0, time.UTC)

	t.Run("no membership on the event path", func(t *testing.T) {
		// Neither request names a calendar: REST is given an event id in
		// the path, MCP an eventId argument. A refusal that could be told
		// apart from an unknown id would answer "is this a live event in
		// this workspace" to somebody who cannot open, list or read the
		// calendar it sits on, and who therefore cannot act on it.
		restStatus, restCode := restErrorCode(t, http.MethodPost,
			testServerURL+"/workspaces/"+outsider.WorkspacePublicID+
				"/calendar-events/"+evtID+"/propose-shift",
			outsider.AccessToken, map[string]any{"newStartAt": start.Unix()})
		assert.Equal(t, http.StatusNotFound, restStatus,
			"a request that never named a calendar must not confirm the event exists")
		assert.Equal(t, "CALENDAR.EVENT.NOT_FOUND", restCode)

		mcpCode := mcpToolErrorCode(t, outsider, "update_calendar_event", map[string]any{
			"eventId": evtID,
			"title":   "Renamed by a non-member",
		})
		assert.Equal(t, restCode, mcpCode,
			"REST and MCP must answer the same refusal for a caller with no membership")
	})

	t.Run("below the write floor on the named-calendar path", func(t *testing.T) {
		addCalendarMemberWithRole(t, host, calID, outsider.Email, "viewer")

		restStatus, restCode := restErrorCode(t, http.MethodPost,
			testServerURL+"/workspaces/"+outsider.WorkspacePublicID+
				"/calendars/"+calID+"/events",
			outsider.AccessToken, map[string]any{
				"kind":     "event",
				"title":    "Viewer authored",
				"startAt":  start.Unix(),
				"endAt":    start.Add(time.Hour).Unix(),
				"timezone": "UTC",
			})
		assert.Equal(t, http.StatusForbidden, restStatus)
		assert.Equal(t, "CALENDAR.CALENDAR.EDITOR_ROLE_REQUIRED", restCode,
			"a member below the write floor is told which role writes")

		mcpCode := mcpToolErrorCode(t, outsider, "create_calendar_event", map[string]any{
			"calendarId": calID,
			"title":      "Viewer authored via MCP",
			"startAt":    start.Unix(),
			"endAt":      start.Add(time.Hour).Unix(),
		})
		assert.Equal(t, restCode, mcpCode,
			"REST and MCP must name the same role for a member below the write floor")
	})

	t.Run("above the write floor, both transports admit", func(t *testing.T) {
		// The floor above editor is a REST-only surface: MCP has no tool
		// that renames a calendar or changes its membership, so there is
		// no MCP refusal to agree or disagree with. What has to hold is
		// that MCP does not invent one — an editor refused by the REST
		// admin path is still admitted by the MCP write tools.
		setCalendarMemberRole(t, host, calID, outsider.UserPublicID, "editor")

		restStatus, restCode := restErrorCode(t, http.MethodPatch,
			testServerURL+"/workspaces/"+outsider.WorkspacePublicID+"/calendars/"+calID,
			outsider.AccessToken, map[string]any{"name": "Renamed by an editor"})
		assert.Equal(t, http.StatusForbidden, restStatus)
		assert.Equal(t, "CALENDAR.CALENDAR.MANAGER_ROLE_REQUIRED", restCode,
			"changing the calendar itself is a manager floor on REST")

		out := mcpTool(t, outsider, "create_calendar_event", map[string]any{
			"calendarId": calID,
			"title":      "Editor authored via MCP",
			"startAt":    start.Add(2 * time.Hour).Unix(),
			"endAt":      start.Add(3 * time.Hour).Unix(),
		})
		assert.NotContains(t, out, "ROLE_REQUIRED",
			"MCP applies no calendar floor above editor, so an editor must be admitted: %s", out)
	})

	t.Run("system calendar", func(t *testing.T) {
		// A provider feed owns these rows, so the refusal is not about the
		// caller's role and must not name one. The fixture grants the
		// strongest role the enum has, so only the calendar's kind can
		// produce the answer.
		sysCalID := insertSystemCalendarWithOwner(t, host)

		restStatus, restCode := restErrorCode(t, http.MethodPost,
			testServerURL+"/workspaces/"+host.WorkspacePublicID+"/calendars/"+sysCalID+"/events",
			host.AccessToken, map[string]any{
				"kind":     "event",
				"title":    "Hand-written holiday",
				"startAt":  start.Unix(),
				"endAt":    start.Add(time.Hour).Unix(),
				"timezone": "UTC",
			})
		assert.Equal(t, http.StatusForbidden, restStatus)
		assert.Equal(t, "CALENDAR.CALENDAR.ACCESS_DENIED", restCode)

		mcpCode := mcpToolErrorCode(t, host, "create_calendar_event", map[string]any{
			"calendarId": sysCalID,
			"title":      "Hand-written holiday via MCP",
			"startAt":    start.Unix(),
			"endAt":      start.Add(time.Hour).Unix(),
		})
		assert.Equal(t, restCode, mcpCode,
			"REST and MCP must answer the same refusal for a provider-fed calendar")
	})
}
