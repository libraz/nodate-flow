package e2e

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/generated"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/types"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/notification"
	calnotifs "github.com/libraz/nodate-flow/apps/flow-api/internal/notifications"
	"github.com/libraz/nodate-flow/packages/go-shared/email"
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
	//
	// Supply public_id explicitly: workspace_members.public_id is
	// BINARY(16) NOT NULL with no default, and STRICT_TRANS_TABLES
	// (MySQL 9 default) turns the missing column into a warning when
	// combined with INSERT IGNORE — silently dropping the row.
	memberPID := types.New()
	_, err := testDB.ExecContext(ctx, `
		INSERT IGNORE INTO workspace_members (public_id, workspace_id, user_id, role, enabled)
		VALUES (?, ?, ?, 'member', TRUE)
	`, memberPID, wsID, attendeeID)
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

	// Each recipient gets exactly one new row, and the occurrence is
	// claimed so future ticks do not re-fire it.
	require.Equalf(t, int64(1),
		notificationCountForUser(ctx, t, testDB, attendeeID)-beforeAttendee,
		"attendee should receive exactly one reminder notification")
	require.Equalf(t, int64(1),
		notificationCountForUser(ctx, t, testDB, ownerID)-beforeOwner,
		"owner should receive exactly one reminder notification")

	var claims int
	err = testDB.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM calendar_event_reminders WHERE event_id = ?`, eventID,
	).Scan(&claims)
	require.NoError(t, err)
	require.Equal(t, 1, claims, "the dispatched occurrence should be claimed")

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

	// A second tick should be a no-op now that the occurrence is claimed.
	calnotifs.CheckAndNotify(ctx, testDB, f)
	require.Equalf(t, int64(1),
		notificationCountForUser(ctx, t, testDB, attendeeID)-beforeAttendee,
		"second tick must not duplicate the attendee's reminder")
	require.Equalf(t, int64(1),
		notificationCountForUser(ctx, t, testDB, ownerID)-beforeOwner,
		"second tick must not duplicate the owner's reminder")
}

// TestRecurringCalendarReminderFiresEveryOccurrence is H-20.
//
// A reminder used to be claimed on one column of the event row, so a
// series rang once for its lifetime: "every Monday, 15 minutes before"
// notified the first Monday and was silent for the remaining
// fifty-one. Adding a rule to an event that had already fired meant it
// never rang again at all.
//
// The test drives the same tick twice for two different occurrences,
// because a single occurrence firing is exactly what the old code did.
func TestRecurringCalendarReminderFiresEveryOccurrence(t *testing.T) {
	bootstrap(t)
	t.Parallel()

	owner := newTenant(t)
	ctx := context.Background()
	queries := generated.New(testDB)

	wsID := lookupWorkspaceInternalID(ctx, t, testDB, owner.WorkspacePublicID)
	ownerID := lookupUserInternalID(ctx, t, testDB, owner.UserPublicID)

	calRes, err := testDB.ExecContext(ctx, `
		INSERT INTO calendars (public_id, workspace_id, kind, name, owner_user_id)
		VALUES (?, ?, 'personal', 'recurring reminder cal', ?)
	`, types.New(), wsID, ownerID)
	require.NoError(t, err)
	calLastID, err := calRes.LastInsertId()
	require.NoError(t, err)
	calID := uint32(calLastID) //#nosec G115 -- LastInsertId in test seed, fits uint32

	// A daily series anchored thirty days in the past, at a time of day
	// five minutes ahead of now. The occurrence the tick should find is
	// today's — which exists only as an expansion of the rule, because
	// the master row's own start is a month old and long past due. That
	// is the arrangement the old claim could not serve.
	anchor := time.Now().UTC().Add(5*time.Minute).Truncate(time.Minute).AddDate(0, 0, -30)
	eventPub := types.New()
	evRes, err := testDB.ExecContext(ctx, `
		INSERT INTO calendar_events (
			public_id, workspace_id, calendar_id,
			title, start_at, end_at, timezone,
			owner_user_id, created_by_user_id,
			notification_offset, recurrence_rule
		) VALUES (?, ?, ?, ?, ?, ?, 'UTC', ?, ?, 30, ?)
	`, eventPub, wsID, calID,
		"Daily standup reminder", anchor, anchor.Add(time.Hour),
		ownerID, ownerID, `{"freq":"daily","interval":1}`)
	require.NoError(t, err)
	evLastID, err := evRes.LastInsertId()
	require.NoError(t, err)
	eventID := uint32(evLastID) //#nosec G115 -- LastInsertId in test seed, fits uint32

	f := notification.NewFanout(testDB, queries, email.NoopSender{})
	f.SetTimeout(5 * time.Second)

	before := notificationCountForUser(ctx, t, testDB, ownerID)
	calnotifs.CheckAndNotify(ctx, testDB, f)
	require.NoError(t, f.Shutdown(ctxWithTimeout(t, 10*time.Second)))

	afterFirst := notificationCountForUser(ctx, t, testDB, ownerID)
	require.Equalf(t, int64(1), afterFirst-before,
		"the occurrence whose reminder window is open must fire (before=%d after=%d)",
		before, afterFirst)

	// A second tick must not re-fire the same occurrence.
	calnotifs.CheckAndNotify(ctx, testDB, f)
	require.Equal(t, afterFirst, notificationCountForUser(ctx, t, testDB, ownerID),
		"a second tick must not duplicate the same occurrence")

	// Exactly one claim exists, and it names an occurrence rather than
	// the series anchor — the anchor is a month old and was never due.
	var claims int
	var claimed time.Time
	require.NoError(t, testDB.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM calendar_event_reminders WHERE event_id = ?`, eventID).Scan(&claims))
	require.Equal(t, 1, claims)
	require.NoError(t, testDB.QueryRowContext(ctx,
		`SELECT occurrence_start FROM calendar_event_reminders WHERE event_id = ?`, eventID).Scan(&claimed))
	require.Truef(t, claimed.After(anchor.Add(24*time.Hour)),
		"the claim must name an expanded occurrence (%s), not the series anchor (%s)",
		claimed.Format(time.RFC3339), anchor.Format(time.RFC3339))

	// Releasing that claim is what the dispatch-failure path does; the
	// next tick must retake it rather than treat the series as done.
	_, err = testDB.ExecContext(ctx,
		`DELETE FROM calendar_event_reminders WHERE event_id = ?`, eventID)
	require.NoError(t, err)

	beforeRetry := notificationCountForUser(ctx, t, testDB, ownerID)
	calnotifs.CheckAndNotify(ctx, testDB, f)
	require.NoError(t, f.Shutdown(ctxWithTimeout(t, 10*time.Second)))
	require.Equalf(t, int64(1),
		notificationCountForUser(ctx, t, testDB, ownerID)-beforeRetry,
		"a released occurrence must be retried, not skipped as already sent")
}
