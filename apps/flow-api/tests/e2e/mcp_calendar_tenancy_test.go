package e2e

import (
	"encoding/json"
	"net/http"
	"regexp"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// mcpToolResultText extracts the tool output payload from a JSON-RPC
// tools/call response. The MCP transport wraps the tool's JSON output as
// a single text content part (see writeRPCResult in
// apps/flow-api/internal/mcp/server.go), so the structured payload lives
// in result.content[0].text as a JSON string.
func mcpToolResultText(t *testing.T, body []byte) string {
	t.Helper()
	var env struct {
		Result *struct {
			Content []struct {
				Type string `json:"type"`
				Text string `json:"text"`
			} `json:"content"`
			IsError bool `json:"isError"`
		} `json:"result"`
	}
	require.NoError(t, json.Unmarshal(body, &env), "decode jsonrpc envelope: %s", string(body))
	require.NotNil(t, env.Result, "expected a result envelope, got: %s", string(body))
	require.False(t, env.Result.IsError, "tool returned isError: %s", string(body))
	require.NotEmpty(t, env.Result.Content, "tool result had no content: %s", string(body))
	return env.Result.Content[0].Text
}

// createMCPCalendar creates a personal calendar via REST and returns its
// public id. Creating a calendar auto-subscribes the owner (visible=TRUE),
// which is what the cross-calendar MCP list query joins on.
func createMCPCalendar(t *testing.T, accessToken, workspacePublicID, name string) string {
	t.Helper()
	var resp struct {
		ID string `json:"id"`
	}
	doJSON(t, http.MethodPost,
		testServerURL+"/workspaces/"+workspacePublicID+"/calendars",
		accessToken, map[string]any{
			"kind":  "personal",
			"name":  name,
			"color": "#4285F4",
		}, &resp)
	require.NotEmpty(t, resp.ID, "calendar create did not return id")
	return resp.ID
}

// TestMCPListCalendarEventsHidesInternalIDs verifies that the
// list_calendar_events MCP tool exposes only public UUID strings and
// never emits internal sequential ids (calendars.id / users.id). This is
// the regression guard for internal-id leakage: REST already exposes
// only public_id, and the MCP surface must match.
func TestMCPListCalendarEventsHidesInternalIDs(t *testing.T) {
	bootstrap(t)
	t.Parallel()

	tt := newTenant(t)
	tok := mintMCPToken(t, tt.AccessToken, tt.WorkspacePublicID,
		"list-events-token", []string{"read:workspace", "write:workspace"})

	calID := createMCPCalendar(t, tt.AccessToken, tt.WorkspacePublicID, "MCP List Cal "+t.Name())

	// Create a timed event via REST so the MCP list returns at least one
	// row to inspect.
	start := time.Date(2026, 6, 1, 10, 0, 0, 0, time.UTC)
	end := start.Add(time.Hour)
	var created struct {
		ID string `json:"id"`
	}
	doJSON(t, http.MethodPost,
		testServerURL+"/workspaces/"+tt.WorkspacePublicID+"/calendars/"+calID+"/events",
		tt.AccessToken, map[string]any{
			"kind":     "event",
			"title":    "internal id probe",
			"startAt":  start.Unix(),
			"endAt":    end.Unix(),
			"timezone": "UTC",
		}, &created)
	require.NotEmpty(t, created.ID)

	body := mcpCall(t, tok, "tools/call", map[string]any{
		"name": "list_calendar_events",
		"arguments": map[string]any{
			"startAt": start.Add(-24 * time.Hour).Unix(),
			"endAt":   end.Add(24 * time.Hour).Unix(),
		},
	})

	payload := mcpToolResultText(t, body)

	var parsed struct {
		Events []map[string]any `json:"events"`
	}
	require.NoError(t, json.Unmarshal([]byte(payload), &parsed),
		"decode tool payload: %s", payload)
	require.NotEmpty(t, parsed.Events, "expected at least one event, payload=%s", payload)

	// The internal-id leak surfaced as ownerUserId (users.id) and a
	// numeric calendarId (calendars.id). Assert the leaked key is gone and
	// every emitted *Id value is a UUID string, never a number.
	uuidRe := regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)
	for _, ev := range parsed.Events {
		_, hasOwner := ev["ownerUserId"]
		require.False(t, hasOwner,
			"list_calendar_events must not emit ownerUserId (internal users.id), event=%v", ev)

		calRaw, ok := ev["calendarId"]
		require.True(t, ok, "event missing calendarId: %v", ev)
		calStr, ok := calRaw.(string)
		require.Truef(t, ok, "calendarId must be a UUID string, got %T (%v)", calRaw, calRaw)
		require.Regexpf(t, uuidRe, calStr, "calendarId must be a UUID v7 string, got %q", calStr)

		idRaw, ok := ev["id"]
		require.True(t, ok, "event missing id: %v", ev)
		idStr, ok := idRaw.(string)
		require.Truef(t, ok, "event id must be a UUID string, got %T", idRaw)
		require.Regexpf(t, uuidRe, idStr, "event id must be a UUID v7 string, got %q", idStr)

		// Defence in depth: no value anywhere in the event object may be a
		// bare JSON number masquerading as an id (json.Unmarshal decodes
		// numbers into float64). Time fields (startAt/endAt) are unix
		// seconds and are allowed; everything else must not be numeric.
		for k, v := range ev {
			if k == "startAt" || k == "endAt" {
				continue
			}
			_, isNum := v.(float64)
			require.Falsef(t, isNum,
				"event field %q is a numeric value %v; internal ids must never be emitted", k, v)
		}
	}
}

// TestMCPCreateCalendarEventRejectsNonMemberOwner verifies that
// create_calendar_event refuses to assign an owner who is not a member of
// the caller's workspace. The FK on calendar_events.owner_user_id
// references the global users table, so without a membership check a
// globally-existing non-member would be assignable as owner. The fix
// resolves the owner through a
// workspace-membership-scoped query and rejects non-members with
// MCP.TOKEN.WORKSPACE_MISMATCH.
func TestMCPCreateCalendarEventRejectsNonMemberOwner(t *testing.T) {
	bootstrap(t)
	t.Parallel()

	tt1 := newTenant(t)
	// tt2's user lives in its own workspace and is therefore NOT a member
	// of tt1's workspace. Its user public id is a valid, globally-existing
	// user id that must still be rejected as an owner in tt1.
	tt2 := newTenant(t)

	tok := mintMCPToken(t, tt1.AccessToken, tt1.WorkspacePublicID,
		"cross-owner-token", []string{"read:workspace", "write:workspace"})

	calID := createMCPCalendar(t, tt1.AccessToken, tt1.WorkspacePublicID, "MCP Owner Cal "+t.Name())

	start := time.Date(2026, 7, 1, 10, 0, 0, 0, time.UTC)
	end := start.Add(time.Hour)

	status, body := mcpCallRaw(t, tok, "tools/call", map[string]any{
		"name": "create_calendar_event",
		"arguments": map[string]any{
			"calendarId":  calID,
			"title":       "cross-tenant owner attempt",
			"startAt":     start.Unix(),
			"endAt":       end.Unix(),
			"ownerUserId": tt2.UserPublicID,
		},
	})
	require.Equal(t, http.StatusOK, status,
		"app-layer rejection must use HTTP 200 + envelope, body=%s", string(body))
	require.Equal(t, "MCP.TOKEN.WORKSPACE_MISMATCH", mcpErrorCode(t, body),
		"non-member owner must be rejected with MCP.TOKEN.WORKSPACE_MISMATCH, body=%s", string(body))

	// Sanity: the same call WITHOUT a foreign owner must succeed, so the
	// rejection above is specifically about cross-tenant ownership and not
	// an unrelated failure.
	okBody := mcpCall(t, tok, "tools/call", map[string]any{
		"name": "create_calendar_event",
		"arguments": map[string]any{
			"calendarId": calID,
			"title":      "self-owned control event",
			"startAt":    start.Unix(),
			"endAt":      end.Unix(),
		},
	})
	payload := mcpToolResultText(t, okBody)
	require.Contains(t, payload, "self-owned control event",
		"control event create should succeed, payload=%s", payload)
}
