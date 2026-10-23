package e2e

import (
	"context"
	"fmt"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// TestEventTimesBeyondDatetimeAreRefused holds the event create and patch
// routes to a field refusal for a unix-seconds value the DATETIME column
// cannot store, instead of a store failure, and to writing nothing.
func TestEventTimesBeyondDatetimeAreRefused(t *testing.T) {
	bootstrap(t)
	t.Parallel()
	tt := newTenant(t)

	calID := createCalendarForTenant(t, tt.AccessToken, tt.WorkspacePublicID, "Range "+randomHex(4))
	eventsURL := fmt.Sprintf("%s/workspaces/%s/calendars/%s/events", testServerURL, tt.WorkspacePublicID, calID)
	start := time.Date(2027, 8, 1, 9, 0, 0, 0, time.UTC).Unix()

	title := "Never ends " + randomHex(6)
	status, raw := doJSONStatus(t, http.MethodPost, eventsURL, tt.AccessToken, map[string]any{
		"kind":     "event",
		"title":    title,
		"startAt":  start,
		"endAt":    beyondDatetimeUnix,
		"timezone": "UTC",
	})
	require.Equal(t, http.StatusUnprocessableEntity, status, "body=%s", string(raw))
	require.Equal(t, "VALIDATION.BODY.FIELD_INVALID", problemType(t, raw))

	var n int
	require.NoError(t, testDB.QueryRowContext(context.Background(),
		`SELECT COUNT(*) FROM calendar_events WHERE title = ?`, title).Scan(&n))
	require.Zero(t, n, "a refused create must not leave an event behind")

	evtID := createCalendarEventForTenant(t, tt.AccessToken, tt.WorkspacePublicID, calID, "Patch me")
	status, raw = doJSONStatus(t, http.MethodPatch, eventsURL+"/"+evtID, tt.AccessToken, map[string]any{
		"recurrenceEnd": beyondDatetimeUnix,
	})
	require.Equal(t, http.StatusUnprocessableEntity, status, "body=%s", string(raw))
	require.Equal(t, "VALIDATION.BODY.FIELD_INVALID", problemType(t, raw))
}
