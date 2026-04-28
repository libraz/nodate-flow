package calendar

import (
	"context"
	"database/sql"
	"net/http"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/nodate-flow/nodate-flow/apps/flow-api/tests/helpers"
)

// TestSoftDelete_EventExcludedFromListAndFind verifies that the soft-delete
// path stamped by DELETE /events/{evtId} (which sets calendar_events.deleted_at
// and clears `enabled`) makes the event invisible to LIST and GET. Both
// queries filter on `deleted_at IS NULL` per sql/queries/calendars/events.sql.
//
// Run prerequisites: NF_TEST_INTEGRATION=1 + Docker (testcontainers MySQL).
// Invoke locally with `make test-integration` or
// `NF_TEST_INTEGRATION=1 go test ./apps/flow-api/tests/calendar/...`.
func TestSoftDelete_EventExcludedFromListAndFind(t *testing.T) {
	bootstrap(t)
	t.Parallel()

	tt := newTenant(t)
	calID := createCalendar(t, tt)

	start := time.Date(2026, 9, 1, 10, 0, 0, 0, time.UTC)
	end := start.Add(1 * time.Hour)

	// Two events in the same range: one stays, one gets soft-deleted.
	_ = createEventForSoftDelete(t, tt, calID, "Keeper", start, end)
	dropID := createEventForSoftDelete(t, tt, calID, "Goner", start, end)

	rangeStart := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
	rangeEnd := rangeStart.AddDate(0, 1, 0)

	// Sanity: both visible before delete.
	titles := listEventTitles(t, tt, calID, rangeStart, rangeEnd)
	assert.ElementsMatch(t, []string{"Keeper", "Goner"}, titles, "both events should be listed before soft-delete")

	// Soft-delete via the public API.
	var deleted struct {
		Deleted bool `json:"deleted"`
	}
	helpers.DoJSON(t, http.MethodDelete, tt.WsPath("calendars", calID, "events", dropID), tt.AccessToken, nil, &deleted)
	require.True(t, deleted.Deleted, "delete endpoint should report success")

	// LIST excludes the soft-deleted event.
	titles = listEventTitles(t, tt, calID, rangeStart, rangeEnd)
	assert.ElementsMatch(t, []string{"Keeper"}, titles, "soft-deleted event must disappear from LIST")

	// GET on the soft-deleted event must 404.
	status, _ := helpers.DoJSONStatus(t, http.MethodGet, tt.WsPath("calendars", calID, "events", dropID), tt.AccessToken, nil)
	assert.Equal(t, http.StatusNotFound, status, "GET on soft-deleted event must 404")

	// And the row itself must still exist with deleted_at set, proving
	// soft-delete (not hard-delete) and supporting audit trails.
	dropInternalID := resolveEventInternalID(t, dropID)
	var deletedAt sql.NullTime
	var enabled bool
	require.NoError(t, testDB.QueryRowContext(context.Background(),
		`SELECT deleted_at, enabled FROM calendar_events WHERE id = ?`, dropInternalID).
		Scan(&deletedAt, &enabled))
	assert.True(t, deletedAt.Valid, "soft-deleted event row must carry deleted_at")
	assert.False(t, enabled, "soft-deleted event row must have enabled=false")
}

// TestSoftDelete_ChildRowsRetainParentLink verifies that soft-deleting a
// calendar event leaves its child rows (comments, attendees, invites,
// attachments) pointing at the parent unchanged. Soft-delete must never
// rewrite child FKs; only the parent's `deleted_at` column is mutated.
func TestSoftDelete_ChildRowsRetainParentLink(t *testing.T) {
	bootstrap(t)
	t.Parallel()

	tt := newTenant(t)
	calID := createCalendar(t, tt)

	evtID := createEventForSoftDelete(t, tt, calID,
		"Soft Delete Children",
		time.Date(2026, 10, 1, 9, 0, 0, 0, time.UTC),
		time.Date(2026, 10, 1, 10, 0, 0, 0, time.UTC))

	evtInternalID := resolveEventInternalID(t, evtID)
	calInternalID := helpers.ResolveCalendarInternalID(t, testDB, calID)
	seedEventChildren(t, tt, evtInternalID, calInternalID)

	// Soft-delete via the API.
	var deleted struct {
		Deleted bool `json:"deleted"`
	}
	helpers.DoJSON(t, http.MethodDelete, tt.WsPath("calendars", calID, "events", evtID), tt.AccessToken, nil, &deleted)
	require.True(t, deleted.Deleted)

	// Child rows must still resolve event_id = evtInternalID. Soft-delete
	// is a column update on the parent; child FKs are untouched.
	for _, c := range childTables() {
		var matched int
		require.NoError(t, testDB.QueryRowContext(context.Background(),
			"SELECT COUNT(*) FROM "+c+" WHERE event_id = ?", evtInternalID).Scan(&matched))
		assert.Equalf(t, 1, matched,
			"%s should still have 1 row pointing at the soft-deleted event", c)

		var nulled int
		require.NoError(t, testDB.QueryRowContext(context.Background(),
			"SELECT COUNT(*) FROM "+c+" WHERE event_id IS NULL").Scan(&nulled))
		assert.Equalf(t, 0, nulled,
			"%s must not have any NULL event_id rows after soft-delete", c)
	}
}

// TestHardDelete_PreservesChildrenWithNullEventID verifies the FK
// ON DELETE SET NULL contract on calendar_event_comments,
// calendar_event_attendees, calendar_event_invites and
// calendar_event_attachments. After a hard `DELETE FROM calendar_events`
// (an admin / audit-trail operation, not exposed via the public API), the
// child rows must survive with event_id NULL so audit history is not lost.
func TestHardDelete_PreservesChildrenWithNullEventID(t *testing.T) {
	bootstrap(t)
	t.Parallel()

	tt := newTenant(t)
	calID := createCalendar(t, tt)

	evtID := createEventForSoftDelete(t, tt, calID,
		"Hard Delete Target",
		time.Date(2026, 11, 1, 9, 0, 0, 0, time.UTC),
		time.Date(2026, 11, 1, 10, 0, 0, 0, time.UTC))

	evtInternalID := resolveEventInternalID(t, evtID)
	calInternalID := helpers.ResolveCalendarInternalID(t, testDB, calID)
	seedEventChildren(t, tt, evtInternalID, calInternalID)

	// Hard delete the parent event row. This bypasses the API on
	// purpose: soft-delete is the only path the public API exposes, but
	// the FK contract (ON DELETE SET NULL) must still hold for any
	// future admin tooling, GDPR purges, or reconciler cleanups.
	res, err := testDB.ExecContext(context.Background(),
		`DELETE FROM calendar_events WHERE id = ?`, evtInternalID)
	require.NoError(t, err, "hard delete calendar_events row")
	rows, err := res.RowsAffected()
	require.NoError(t, err)
	require.Equal(t, int64(1), rows, "exactly one calendar_events row should be deleted")

	// Each child row must survive with event_id set to NULL.
	for _, c := range childTables() {
		var total int
		require.NoError(t, testDB.QueryRowContext(context.Background(),
			"SELECT COUNT(*) FROM "+c+" WHERE workspace_id = ?", tt.WorkspaceID).Scan(&total))
		assert.Equalf(t, 1, total,
			"%s row must survive parent hard-delete (FK ON DELETE SET NULL)", c)

		var nulled int
		require.NoError(t, testDB.QueryRowContext(context.Background(),
			"SELECT COUNT(*) FROM "+c+" WHERE workspace_id = ? AND event_id IS NULL",
			tt.WorkspaceID).Scan(&nulled))
		assert.Equalf(t, 1, nulled,
			"%s row must have event_id = NULL after parent hard-delete", c)
	}
}

// childTables enumerates the calendar event child tables that carry an
// `event_id` FK declared with ON DELETE SET NULL. Kept as a function so
// the slice is allocated per call and can't be mutated across tests.
func childTables() []string {
	return []string{
		"calendar_event_comments",
		"calendar_event_attendees",
		"calendar_event_invites",
		"calendar_event_attachments",
	}
}

// createEventForSoftDelete creates a single non-recurring event via the
// public API and returns its public ID.
func createEventForSoftDelete(
	t *testing.T,
	tt *helpers.CalendarTestTenant,
	calID, title string,
	start, end time.Time,
) string {
	t.Helper()
	var resp struct {
		ID string `json:"id"`
	}
	helpers.DoJSON(t, http.MethodPost, tt.WsPath("calendars", calID, "events"), tt.AccessToken, map[string]any{
		"kind":     "event",
		"title":    title,
		"startAt":  start.Unix(),
		"endAt":    end.Unix(),
		"timezone": "UTC",
	}, &resp)
	require.NotEmpty(t, resp.ID, "create event must return a public id")
	return resp.ID
}

// listEventTitles fetches event titles in the supplied range via the
// LIST endpoint, which filters on deleted_at IS NULL server-side.
func listEventTitles(
	t *testing.T,
	tt *helpers.CalendarTestTenant,
	calID string,
	start, end time.Time,
) []string {
	t.Helper()
	var resp struct {
		Events []struct {
			Title string `json:"title"`
		} `json:"events"`
	}
	url := tt.WsPath("calendars", calID, "events") +
		"?start=" + start.Format(time.RFC3339) +
		"&end=" + end.Format(time.RFC3339)
	helpers.DoJSON(t, http.MethodGet, url, tt.AccessToken, nil, &resp)
	titles := make([]string, 0, len(resp.Events))
	for _, e := range resp.Events {
		titles = append(titles, e.Title)
	}
	return titles
}

// resolveEventInternalID looks up the internal calendar_events.id for the
// given public ID. Direct SQL is used because the surface needed here is
// the FK contract, not the API response shape.
func resolveEventInternalID(t *testing.T, evtPublicID string) uint32 {
	t.Helper()
	var id uint32
	err := testDB.QueryRowContext(context.Background(),
		`SELECT id FROM calendar_events WHERE public_id = UUID_TO_BIN(?, 0) LIMIT 1`,
		evtPublicID).Scan(&id)
	require.NoError(t, err, "resolve event internal id for %s", evtPublicID)
	return id
}

// seedEventChildren inserts exactly one row in each of the four child
// tables that hold an event_id FK with ON DELETE SET NULL. Direct SQL is
// used because the goal is to probe the FK contract without depending on
// every child-row endpoint behaving correctly.
func seedEventChildren(
	t *testing.T,
	tt *helpers.CalendarTestTenant,
	evtInternalID, calInternalID uint32,
) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	pub := func() []byte {
		u := uuid.Must(uuid.NewV7())
		return u[:]
	}

	_, err := testDB.ExecContext(ctx, `
		INSERT INTO calendar_event_comments
		  (public_id, workspace_id, event_id, author_id, body)
		VALUES (?, ?, ?, ?, 'soft-delete probe comment')`,
		pub(), tt.WorkspaceID, evtInternalID, tt.UserInternalID)
	require.NoError(t, err, "seed calendar_event_comments")

	_, err = testDB.ExecContext(ctx, `
		INSERT INTO calendar_event_attendees
		  (public_id, workspace_id, event_id, user_id, rsvp)
		VALUES (?, ?, ?, ?, 'pending')`,
		pub(), tt.WorkspaceID, evtInternalID, tt.UserInternalID)
	require.NoError(t, err, "seed calendar_event_attendees")

	// The invite token_hash unique-key is BINARY(32). Seed it with a
	// per-test random value so parallel runs cannot collide.
	tokenHash := make([]byte, 32)
	for i := range tokenHash {
		tokenHash[i] = byte(i + int(evtInternalID)%251)
	}
	_, err = testDB.ExecContext(ctx, `
		INSERT INTO calendar_event_invites
		  (public_id, workspace_id, calendar_id, event_id, attendee_id,
		   email, token_hash, expires_at)
		VALUES (?, ?, ?, ?, NULL, 'probe@example.test', ?, DATE_ADD(NOW(3), INTERVAL 7 DAY))`,
		pub(), tt.WorkspaceID, calInternalID, evtInternalID, tokenHash)
	require.NoError(t, err, "seed calendar_event_invites")

	// storage_key carries a UNIQUE constraint, so randomize it.
	storageKey := "test/probe/" + uuid.Must(uuid.NewV7()).String()
	_, err = testDB.ExecContext(ctx, `
		INSERT INTO calendar_event_attachments
		  (public_id, workspace_id, event_id, uploader_id,
		   filename, storage_key, byte_size)
		VALUES (?, ?, ?, ?, 'probe.txt', ?, 12)`,
		pub(), tt.WorkspaceID, evtInternalID, tt.UserInternalID, storageKey)
	require.NoError(t, err, "seed calendar_event_attachments")
}
