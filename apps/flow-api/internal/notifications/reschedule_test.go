package notifications

import (
	"context"
	"database/sql"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/generated"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/types"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/notification"
	"github.com/libraz/nodate-flow/apps/flow-api/tests/helpers"
	"github.com/libraz/nodate-flow/packages/go-shared/email"
	"github.com/libraz/nodate-flow/packages/go-shared/testhelpers"
)

// claimRowsFor counts the delivery claims recorded for one event. The
// claim table is the record of what has been sent: a row is inserted
// before dispatch and deleted again if the dispatch fails, so a claim
// that is still there means a reminder went out for that occurrence.
func claimRowsFor(ctx context.Context, t *testing.T, db *sql.DB, eventID uint32) int {
	t.Helper()
	var n int
	require.NoError(t, db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM calendar_event_reminders WHERE event_id = ?`, eventID).Scan(&n))
	return n
}

// reminderRowsFor counts the in-app notifications delivered about one
// event.
func reminderRowsFor(ctx context.Context, t *testing.T, db *sql.DB, eventPublicID types.PublicID) int {
	t.Helper()
	var n int
	require.NoError(t, db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM notifications
		 WHERE resource_public_id = ? AND event_type = 'calendar.reminder'`,
		eventPublicID).Scan(&n))
	return n
}

// TestReminderFiresAgainAfterReschedule is the behaviour a calendar has
// to have: move a meeting and the reminder follows it.
//
// Delivery used to be remembered on a column of the event row, so the
// first send marked the event notified for good. Push the meeting an
// hour later and it went quiet — the owner had already been reminded
// about the time the meeting was no longer at, and would hear nothing
// about the time it now was. A claim is taken per occurrence instead,
// and a new start is a new occurrence.
//
// The assertions are about the rows, not about who wrote them. Ticks
// are shared: this suite runs in parallel against one database and
// several packages drive CheckAndNotify, so the claim for this event
// may well be taken by somebody else's tick. That is what a second
// replica does in production too, and the outcome it has to produce is
// the same one — a claim per occurrence and a notification per claim.
func TestReminderFiresAgainAfterReschedule(t *testing.T) {
	testhelpers.SkipUnlessIntegration(t)
	inst := helpers.StartShared(t)
	db := inst.DB
	ctx := context.Background()

	fx := seedClaimFixture(t, db)
	fanout := notification.NewFanout(db, generated.New(db), email.NoopSender{})
	fanout.SetTimeout(5 * time.Second)

	// The fixture's reminder window is already open: it starts in five
	// minutes and asks for thirty minutes' notice.
	CheckAndNotify(ctx, db, fanout)
	require.Equal(t, 1, claimRowsFor(ctx, t, db, fx.eventID),
		"the open reminder window must produce one claim")
	require.Equal(t, 1, reminderRowsFor(ctx, t, db, fx.eventPublicID),
		"the owner must be notified once")

	// Nothing has changed, so the occurrence stays claimed.
	CheckAndNotify(ctx, db, fanout)
	require.Equal(t, 1, claimRowsFor(ctx, t, db, fx.eventID),
		"a second tick must not re-claim the same occurrence")
	require.Equal(t, 1, reminderRowsFor(ctx, t, db, fx.eventPublicID),
		"a second tick must not duplicate the notification")

	// Move the meeting later, still inside its reminder window so the
	// new occurrence is due now rather than at some point this suite
	// would have to wait for.
	moved := time.Now().UTC().Add(20 * time.Minute)
	_, err := db.ExecContext(ctx,
		`UPDATE calendar_events SET start_at = ?, end_at = ? WHERE id = ?`,
		moved, moved.Add(time.Hour), fx.eventID)
	require.NoError(t, err)

	CheckAndNotify(ctx, db, fanout)
	require.Equal(t, 2, claimRowsFor(ctx, t, db, fx.eventID),
		"the rescheduled occurrence must be claimed in its own right")
	require.Equal(t, 2, reminderRowsFor(ctx, t, db, fx.eventPublicID),
		"the reminder must follow the meeting to its new time")

	// And the new occurrence is claimed once like any other, so moving a
	// meeting does not turn one reminder into a stream of them.
	CheckAndNotify(ctx, db, fanout)
	require.Equal(t, 2, claimRowsFor(ctx, t, db, fx.eventID))
	require.Equal(t, 2, reminderRowsFor(ctx, t, db, fx.eventPublicID))

	// The two claims name the two starts, which is what makes them
	// distinct: a claim keyed on anything else would have collided.
	rows, err := db.QueryContext(ctx,
		`SELECT occurrence_start FROM calendar_event_reminders
		 WHERE event_id = ? ORDER BY occurrence_start`, fx.eventID)
	require.NoError(t, err)
	defer func() { _ = rows.Close() }()
	var starts []time.Time
	for rows.Next() {
		var s time.Time
		require.NoError(t, rows.Scan(&s))
		starts = append(starts, s)
	}
	require.NoError(t, rows.Err())
	require.Len(t, starts, 2)
	require.True(t, starts[1].After(starts[0]),
		"the second claim must name the later start the meeting was moved to")
}

// TestReminderIsNotResentWhenOnlyTheTitleChanges separates "the row was
// touched" from "the meeting moved". An edit that leaves the start
// where it is describes an occurrence that has already been reminded
// about, so re-sending on any UPDATE would make every corrected typo
// notify the whole invitee list.
func TestReminderIsNotResentWhenOnlyTheTitleChanges(t *testing.T) {
	testhelpers.SkipUnlessIntegration(t)
	inst := helpers.StartShared(t)
	db := inst.DB
	ctx := context.Background()

	fx := seedClaimFixture(t, db)
	fanout := notification.NewFanout(db, generated.New(db), email.NoopSender{})
	fanout.SetTimeout(5 * time.Second)

	CheckAndNotify(ctx, db, fanout)
	require.Equal(t, 1, claimRowsFor(ctx, t, db, fx.eventID))

	_, err := db.ExecContext(ctx,
		`UPDATE calendar_events SET title = ? WHERE id = ?`,
		"Claim me (renamed)", fx.eventID)
	require.NoError(t, err)

	CheckAndNotify(ctx, db, fanout)
	require.Equal(t, 1, claimRowsFor(ctx, t, db, fx.eventID),
		"renaming a meeting is not rescheduling it")
	require.Equal(t, 1, reminderRowsFor(ctx, t, db, fx.eventPublicID))
}
