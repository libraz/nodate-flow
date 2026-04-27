package e2e

import (
	"context"
	"database/sql"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/nodate-flow/nodate-flow/apps/flow-api/internal/db/generated"
	"github.com/nodate-flow/nodate-flow/apps/flow-api/internal/db/types"
	"github.com/nodate-flow/nodate-flow/apps/flow-api/internal/notification"
	calnotifs "github.com/nodate-flow/nodate-flow/apps/flow-api/internal/notifications"
	"github.com/nodate-flow/nodate-flow/packages/go-shared/email"
)

// TestCalendarReminderDispatch verifies the calendar reminder scheduler
// pipeline: a calendar event with notification_offset whose reminder
// window has opened gets fanned out as in-app notifications to its
// attendees and owner, and notified_at is marked exactly once so a
// second tick does not duplicate the rows.
func TestCalendarReminderDispatch(t *testing.T) {
	bootstrap(t)
	t.Parallel()

	owner := newTenant(t)
	attendee := newTenant(t)

	ctx := context.Background()
	queries := generated.New(testDB)

	wsID := lookupWorkspaceInternalID(ctx, t, testDB, owner.WorkspacePublicID)
	ownerID := lookupUserInternalID(ctx, t, testDB, owner.UserPublicID)
	attendeeID := lookupUserInternalID(ctx, t, testDB, attendee.UserPublicID)

	// The attendee belongs to a different workspace by default. Add them
	// as a member of the owner's workspace so the FK on
	// calendar_event_attendees.user_id is satisfied via a workspace they
	// share.
	_, err := testDB.ExecContext(ctx, `
		INSERT IGNORE INTO workspace_members (workspace_id, user_id, role, enabled)
		VALUES (?, ?, 'member', TRUE)
	`, wsID, attendeeID)
	require.NoError(t, err)

	// Seed a personal calendar owned by the tenant.
	calRes, err := testDB.ExecContext(ctx, `
		INSERT INTO calendars (public_id, workspace_id, kind, name, owner_user_id)
		VALUES (?, ?, 'personal', 'reminder cal', ?)
	`, types.New(), wsID, ownerID)
	require.NoError(t, err)
	calLastID, err := calRes.LastInsertId()
	require.NoError(t, err)
	calID := uint32(calLastID) //#nosec G115 -- LastInsertId in test seed, fits uint32

	// Insert a calendar event whose reminder window has already opened
	// but whose start is still in the future. start_at = now + 5min,
	// notification_offset = 30min ⇒ DATE_SUB(start_at, 30 MINUTE) is in
	// the past, satisfying the scheduler's WHERE clause.
	startAt := time.Now().UTC().Add(5 * time.Minute)
	eventPub := types.New()
	evRes, err := testDB.ExecContext(ctx, `
		INSERT INTO calendar_events (
			public_id, workspace_id, calendar_id,
			title, start_at, end_at, timezone,
			owner_user_id, created_by_user_id,
			notification_offset
		) VALUES (?, ?, ?, ?, ?, ?, 'UTC', ?, ?, 30)
	`, eventPub, wsID, calID,
		"Reminder me", startAt, startAt.Add(time.Hour),
		ownerID, ownerID)
	require.NoError(t, err)
	evLastID, err := evRes.LastInsertId()
	require.NoError(t, err)
	eventID := uint32(evLastID) //#nosec G115 -- LastInsertId in test seed, fits uint32

	// Add the attendee.
	_, err = testDB.ExecContext(ctx, `
		INSERT INTO calendar_event_attendees (
			public_id, workspace_id, event_id, user_id, rsvp, can_edit, enabled
		) VALUES (?, ?, ?, ?, 'accepted', FALSE, TRUE)
	`, types.New(), wsID, eventID, attendeeID)
	require.NoError(t, err)

	beforeAttendee := notificationCountForUser(ctx, t, testDB, attendeeID)
	beforeOwner := notificationCountForUser(ctx, t, testDB, ownerID)

	// Drive a single scheduler tick directly so the assertions are
	// deterministic. We use the real Fanout so the production path is
	// exercised end-to-end.
	f := notification.NewFanout(testDB, queries, email.NoopSender{})
	f.SetTimeout(5 * time.Second)
	calnotifs.CheckAndNotify(ctx, testDB, f)

	// Each recipient gets exactly one new row, and notified_at is set so
	// future ticks do not re-fire.
	require.Equalf(t, int64(1),
		notificationCountForUser(ctx, t, testDB, attendeeID)-beforeAttendee,
		"attendee should receive exactly one reminder notification")
	require.Equalf(t, int64(1),
		notificationCountForUser(ctx, t, testDB, ownerID)-beforeOwner,
		"owner should receive exactly one reminder notification")

	var notifiedAt sql.NullTime
	err = testDB.QueryRowContext(ctx,
		`SELECT notified_at FROM calendar_events WHERE id = ?`, eventID,
	).Scan(&notifiedAt)
	require.NoError(t, err)
	require.True(t, notifiedAt.Valid, "notified_at should be set after dispatch")

	// Inspect a delivered row to confirm the channel + resource link
	// match the scheduler contract.
	var (
		eventType        string
		resourceType     string
		resourcePublicID types.PublicID
		channel          string
		title            string
	)
	err = testDB.QueryRowContext(ctx, `
		SELECT event_type, resource_type, resource_public_id, channel, title
		FROM notifications
		WHERE recipient_user_id = ?
		  AND event_type = 'calendar.reminder'
		ORDER BY id DESC
		LIMIT 1
	`, attendeeID).Scan(&eventType, &resourceType, &resourcePublicID, &channel, &title)
	require.NoError(t, err)
	require.Equal(t, "calendar.reminder", eventType)
	require.Equal(t, "calendar_event", resourceType)
	require.Equal(t, eventPub.String(), resourcePublicID.String())
	require.Equal(t, "in_app", channel)
	require.Equal(t, "Reminder me", title)

	// A second tick should be a no-op now that notified_at is set.
	calnotifs.CheckAndNotify(ctx, testDB, f)
	require.Equalf(t, int64(1),
		notificationCountForUser(ctx, t, testDB, attendeeID)-beforeAttendee,
		"second tick must not duplicate the attendee's reminder")
	require.Equalf(t, int64(1),
		notificationCountForUser(ctx, t, testDB, ownerID)-beforeOwner,
		"second tick must not duplicate the owner's reminder")
}
