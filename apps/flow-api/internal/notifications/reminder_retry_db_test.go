package notifications

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/generated"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/types"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/notification"
	"github.com/libraz/nodate-flow/apps/flow-api/tests/helpers"
	"github.com/libraz/nodate-flow/packages/go-shared/testhelpers"
)

// TestRetriedReminderReachesEachRecipientOnce replays a reminder whose
// first delivery reached only half its recipients. The retry has to
// reuse that attempt's event row: a fresh one gives the notifications a
// new source_event_id, and the recipients who already got the reminder
// get it a second time.
func TestRetriedReminderReachesEachRecipientOnce(t *testing.T) {
	testhelpers.SkipUnlessIntegration(t)
	inst := helpers.StartShared(t)
	db := inst.DB
	fx := seedClaimFixture(t, db)
	ctx := context.Background()
	queries := generated.New(db)

	res, err := db.ExecContext(ctx,
		`INSERT INTO users (public_id, email, display_name, locale) VALUES (?, ?, 'Second', 'en')`,
		types.New(), "reminder-retry-"+uuid.NewString()[:8]+"@example.test")
	require.NoError(t, err)
	secondRaw, err := res.LastInsertId()
	require.NoError(t, err)
	second := uint32(secondRaw) //#nosec G115 -- LastInsertId in test seed, fits uint32

	n := pendingNotification{
		ID:              fx.eventID,
		PublicID:        fx.eventPublicID,
		Title:           "Claim me",
		OccurrenceStart: time.Date(2030, 6, 3, 9, 0, 0, 0, time.UTC),
		WorkspaceID:     fx.workspaceID,
	}
	f := notification.NewFanout(db, queries, nil)

	first, err := reminderEvent(ctx, queries, n)
	require.NoError(t, err)
	require.NoError(t, f.DeliverCalendarReminder(ctx, n.WorkspaceID, n.PublicID, n.Title, []uint32{fx.userID}, first))

	retry, err := reminderEvent(ctx, queries, n)
	require.NoError(t, err)
	require.Equal(t, first, retry, "a retry must reuse the occurrence's reminder event")
	require.NoError(t, f.DeliverCalendarReminder(ctx, n.WorkspaceID, n.PublicID, n.Title, []uint32{fx.userID, second}, retry))

	for _, recipient := range []uint32{fx.userID, second} {
		var count int
		require.NoError(t, db.QueryRowContext(ctx,
			`SELECT COUNT(*) FROM notifications
			  WHERE recipient_user_id = ? AND resource_public_id = ? AND event_type = 'calendar.reminder'`,
			recipient, n.PublicID).Scan(&count))
		require.Equal(t, 1, count, "recipient %d must hold exactly one reminder row", recipient)
	}

	// A different occurrence is a different reminder.
	next := n
	next.OccurrenceStart = n.OccurrenceStart.AddDate(0, 0, 7)
	other, err := reminderEvent(ctx, queries, next)
	require.NoError(t, err)
	require.NotEqual(t, first, other, "the next occurrence must get its own reminder event")
}

// TestReleaseSurvivesCancelledContext holds the release to the shutdown
// case: a tick cancelled mid-dispatch still gives its claim back, so the
// next process retries the reminder instead of finding it claimed.
func TestReleaseSurvivesCancelledContext(t *testing.T) {
	testhelpers.SkipUnlessIntegration(t)
	inst := helpers.StartShared(t)
	db := inst.DB
	fx := seedClaimFixture(t, db)
	queries := generated.New(db)
	occurrence := time.Date(2030, 7, 1, 9, 0, 0, 0, time.UTC)

	affected, err := queries.ClaimReminderOccurrence(context.Background(), generated.ClaimReminderOccurrenceParams{
		WorkspaceID: fx.workspaceID, EventID: fx.eventID, OccurrenceStart: occurrence,
	})
	require.NoError(t, err)
	require.EqualValues(t, 1, affected)

	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	releaseReminderClaim(cancelled, db, pendingNotification{ID: fx.eventID, PublicID: fx.eventPublicID, OccurrenceStart: occurrence})

	affected, err = queries.ClaimReminderOccurrence(context.Background(), generated.ClaimReminderOccurrenceParams{
		WorkspaceID: fx.workspaceID, EventID: fx.eventID, OccurrenceStart: occurrence,
	})
	require.NoError(t, err)
	require.EqualValues(t, 1, affected, "a claim released under a cancelled context must be free again")
}
