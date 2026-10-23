package e2e

import (
	"fmt"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// TestEventShapeRefusalsAreValidationErrors holds create and patch to a
// 422 naming the rule for a row the calendar_events CHECKs would reject,
// instead of the store failure the constraint answers with.
func TestEventShapeRefusalsAreValidationErrors(t *testing.T) {
	bootstrap(t)
	t.Parallel()
	tt := newTenant(t)

	calID := createCalendarForTenant(t, tt.AccessToken, tt.WorkspacePublicID, "Shape "+randomHex(4))
	eventsURL := fmt.Sprintf("%s/workspaces/%s/calendars/%s/events", testServerURL, tt.WorkspacePublicID, calID)
	start := time.Date(2027, 8, 2, 9, 0, 0, 0, time.UTC)

	status, raw := doJSONStatus(t, http.MethodPost, eventsURL, tt.AccessToken, map[string]any{
		"kind":           "event",
		"title":          "Undated series",
		"timezone":       "UTC",
		"recurrenceRule": map[string]any{"freq": "weekly"},
	})
	require.Equal(t, http.StatusUnprocessableEntity, status, "body=%s", string(raw))
	require.Equal(t, "CALENDAR.EVENT.RECURRENCE_REQUIRES_START", problemType(t, raw))

	status, raw = doJSONStatus(t, http.MethodPost, eventsURL, tt.AccessToken, map[string]any{
		"kind":           "milestone",
		"title":          "Recurring milestone",
		"startAt":        start.Unix(),
		"endAt":          start.Unix(),
		"timezone":       "UTC",
		"recurrenceRule": map[string]any{"freq": "weekly"},
	})
	require.Equal(t, http.StatusUnprocessableEntity, status, "body=%s", string(raw))
	require.Equal(t, "CALENDAR.EVENT.MILESTONE_RECURRENCE_NOT_ALLOWED", problemType(t, raw))

	evtID := createCalendarEventForTenant(t, tt.AccessToken, tt.WorkspacePublicID, calID, "Becomes a milestone")
	doJSON(t, http.MethodPatch, eventsURL+"/"+evtID, tt.AccessToken, map[string]any{
		"recurrenceRule": map[string]any{"freq": "weekly"},
	}, nil)
	status, raw = doJSONStatus(t, http.MethodPatch, eventsURL+"/"+evtID, tt.AccessToken, map[string]any{
		"kind": "milestone",
	})
	require.Equal(t, http.StatusUnprocessableEntity, status, "body=%s", string(raw))
	require.Equal(t, "CALENDAR.EVENT.MILESTONE_RECURRENCE_NOT_ALLOWED", problemType(t, raw))
}
