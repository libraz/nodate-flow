package notifications

import (
	"context"
	"database/sql"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/generated"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/types"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/notification"
	"github.com/libraz/nodate-flow/apps/flow-api/tests/helpers"
	"github.com/libraz/nodate-flow/packages/go-shared/email"
	"github.com/libraz/nodate-flow/packages/go-shared/recurrence"
	"github.com/libraz/nodate-flow/packages/go-shared/testhelpers"
)

// seedWorkspaceUser adds a user to the fixture's workspace, as an enabled
// member or as one who has left it.
func seedWorkspaceUser(t *testing.T, db *sql.DB, fx *claimFixture, member bool) uint32 {
	t.Helper()
	ctx := context.Background()
	res, err := db.ExecContext(ctx,
		`INSERT INTO users (public_id, email, display_name, locale) VALUES (?, ?, ?, 'en')`,
		types.New(), "reminder-scope-"+uuid.New().String()[:8]+"@example.test", "Reminder scope")
	require.NoError(t, err)
	raw, err := res.LastInsertId()
	require.NoError(t, err)
	userID := uint32(raw) //#nosec G115 -- LastInsertId in test seed, fits uint32

	_, err = db.ExecContext(ctx,
		`INSERT INTO workspace_members (public_id, workspace_id, user_id, role, enabled) VALUES (?, ?, ?, 'member', ?)`,
		types.New(), fx.workspaceID, userID, member)
	require.NoError(t, err)
	return userID
}

func seedAttendee(t *testing.T, db *sql.DB, fx *claimFixture, eventID, userID uint32) {
	t.Helper()
	_, err := db.ExecContext(context.Background(),
		`INSERT INTO calendar_event_attendees (public_id, workspace_id, event_id, user_id, rsvp, can_edit)
		 VALUES (?, ?, ?, ?, 'accepted', FALSE)`,
		types.New(), fx.workspaceID, eventID, userID)
	require.NoError(t, err)
}

// TestReminderIsNotSentForAnEventOnADeletedCalendar holds the scan to the
// scope the calendar read paths apply: a calendar the user deleted takes
// its events' reminders with it.
func TestReminderIsNotSentForAnEventOnADeletedCalendar(t *testing.T) {
	testhelpers.SkipUnlessIntegration(t)
	inst := helpers.StartShared(t)
	db := inst.DB
	ctx := context.Background()

	fx := seedClaimFixture(t, db)
	_, err := db.ExecContext(ctx, `UPDATE calendars SET enabled = FALSE WHERE id = ?`, fx.calendarID)
	require.NoError(t, err)

	fanout := notification.NewFanout(db, generated.New(db), email.NoopSender{})
	fanout.SetTimeout(5 * time.Second)
	CheckAndNotify(ctx, db, fanout)

	require.Equal(t, 0, claimRowsFor(ctx, t, db, fx.eventID),
		"an event on a deleted calendar must not be claimed for a reminder")
	require.Equal(t, 0, reminderRowsFor(ctx, t, db, fx.eventPublicID),
		"an event on a deleted calendar must not be reminded")
}

// TestReminderRecipientsAreEnabledWorkspaceMembers keeps a removed
// attendee — and an owner who left — off the recipient list, while a
// current member stays on it.
func TestReminderRecipientsAreEnabledWorkspaceMembers(t *testing.T) {
	testhelpers.SkipUnlessIntegration(t)
	inst := helpers.StartShared(t)
	db := inst.DB
	ctx := context.Background()

	fx := seedClaimFixture(t, db)
	current := seedWorkspaceUser(t, db, fx, true)
	removed := seedWorkspaceUser(t, db, fx, false)
	seedAttendee(t, db, fx, fx.eventID, current)
	seedAttendee(t, db, fx, fx.eventID, removed)

	n := pendingNotification{ID: fx.eventID, OwnerUserID: fx.userID, WorkspaceID: fx.workspaceID}
	got, err := reminderRecipients(ctx, db, n)
	require.NoError(t, err)
	require.ElementsMatch(t, []uint32{fx.userID, current}, got,
		"only enabled members of the workspace are reminded")

	_, err = db.ExecContext(ctx,
		`UPDATE workspace_members SET enabled = FALSE WHERE workspace_id = ? AND user_id = ?`,
		fx.workspaceID, fx.userID)
	require.NoError(t, err)
	got, err = reminderRecipients(ctx, db, n)
	require.NoError(t, err)
	require.ElementsMatch(t, []uint32{current}, got,
		"an owner who left the workspace is not reminded")
}

// TestOverrideReminderReachesTheSeriesAttendees: moving or renaming one
// occurrence must not silence its reminder for the people invited to the
// series, because the override row has no attendee rows of its own.
func TestOverrideReminderReachesTheSeriesAttendees(t *testing.T) {
	testhelpers.SkipUnlessIntegration(t)
	inst := helpers.StartShared(t)
	db := inst.DB
	ctx := context.Background()

	fx := seedClaimFixture(t, db)
	invitee := seedWorkspaceUser(t, db, fx, true)
	// The fixture's event is the master here; the override names it.
	seedAttendee(t, db, fx, fx.eventID, invitee)

	start := time.Now().UTC().Add(10 * time.Minute)
	res, err := db.ExecContext(ctx,
		`INSERT INTO calendar_events (
			public_id, workspace_id, calendar_id, recurrence_parent_id, recurrence_original_start,
			title, start_at, end_at, timezone, owner_user_id, created_by_user_id, notification_offset
		) VALUES (?, ?, ?, ?, ?, 'Moved', ?, ?, 'UTC', ?, ?, 30)`,
		types.New(), fx.workspaceID, fx.calendarID, fx.eventID, start.Add(-time.Hour),
		start, start.Add(time.Hour), fx.userID, fx.userID)
	require.NoError(t, err)
	raw, err := res.LastInsertId()
	require.NoError(t, err)

	got, err := reminderRecipients(ctx, db, pendingNotification{
		ID:          uint32(raw), //#nosec G115 -- LastInsertId in test seed, fits uint32
		OwnerUserID: fx.userID,
		WorkspaceID: fx.workspaceID,
		SeriesID:    fx.eventID,
	})
	require.NoError(t, err)
	require.ElementsMatch(t, []uint32{fx.userID, invitee}, got,
		"the series' invitee is reminded of the overridden occurrence")
}

// TestClaimedOccurrencesAreReadBeforeRecipients pins the pre-check that
// keeps a claimed occurrence from costing a recipient read every tick.
func TestClaimedOccurrencesAreReadBeforeRecipients(t *testing.T) {
	testhelpers.SkipUnlessIntegration(t)
	inst := helpers.StartShared(t)
	db := inst.DB
	ctx := context.Background()

	fx := seedClaimFixture(t, db)
	occurrence := time.Now().UTC().Add(time.Hour).Truncate(time.Millisecond)
	_, err := generated.New(db).ClaimReminderOccurrence(ctx, generated.ClaimReminderOccurrenceParams{
		WorkspaceID:     fx.workspaceID,
		EventID:         fx.eventID,
		OccurrenceStart: occurrence,
	})
	require.NoError(t, err)

	claimed, err := claimedOccurrences(ctx, db, []pendingNotification{
		{ID: fx.eventID, OccurrenceStart: occurrence},
		{ID: fx.eventID, OccurrenceStart: occurrence.Add(24 * time.Hour)},
	}, time.Now().UTC())
	require.NoError(t, err)
	require.True(t, claimed[claimKey{eventID: fx.eventID, occurrence: occurrence.UnixMilli()}])
	require.False(t, claimed[claimKey{eventID: fx.eventID, occurrence: occurrence.Add(24 * time.Hour).UnixMilli()}])
}

// TestOverrideLookupBindsABoundedNumberOfPlaceholders: with one IN list
// per id slice, more than 32,767 series pass the protocol's placeholder
// limit and every reminder on the instance stops.
func TestOverrideLookupBindsABoundedNumberOfPlaceholders(t *testing.T) {
	testhelpers.SkipUnlessIntegration(t)
	inst := helpers.StartShared(t)
	db := inst.DB

	rule := &recurrence.Rule{Freq: recurrence.FreqDaily}
	candidates := make([]reminderCandidate, 40000)
	for i := range candidates {
		candidates[i] = reminderCandidate{
			n:    pendingNotification{ID: uint32(4_000_000_000 - i), WorkspaceID: 1}, //#nosec G115 -- synthetic ids no row holds
			rule: rule,
		}
	}
	_, err := overriddenReminderStarts(context.Background(), db, candidates)
	require.NoError(t, err)
}
