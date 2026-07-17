// Integration tests for the atomic reminder claim. These exercise the
// real calendar_events table against the shared MySQL testcontainer so
// the conditional UPDATE semantics (exactly one winner per NULL ->
// NOW() flip) are covered end-to-end.
package notifications

import (
	"context"
	"database/sql"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/nodate-flow/nodate-flow/apps/flow-api/internal/db/generated"
	"github.com/nodate-flow/nodate-flow/apps/flow-api/internal/db/types"
	"github.com/nodate-flow/nodate-flow/apps/flow-api/tests/helpers"
)

// claimFixture is the minimum scaffolding a claim test needs: one
// calendar event (with its owning workspace / calendar / user) whose
// reminder window is open and notified_at is still NULL.
type claimFixture struct {
	eventID       uint32
	eventPublicID types.PublicID
}

func seedClaimFixture(t *testing.T, db *sql.DB) *claimFixture {
	t.Helper()
	ctx := context.Background()
	suffix := uuid.New().String()[:8]

	res, err := db.ExecContext(ctx,
		`INSERT INTO users (public_id, email, display_name, locale)
		 VALUES (?, ?, ?, 'en')`,
		types.New(), "reminder-claim-"+suffix+"@example.test", "Reminder claim")
	require.NoError(t, err)
	userRaw, err := res.LastInsertId()
	require.NoError(t, err)
	userID := uint32(userRaw) //#nosec G115 -- LastInsertId in test seed, fits uint32

	res, err = db.ExecContext(ctx,
		`INSERT INTO workspaces (public_id, slug, name) VALUES (?, ?, ?)`,
		types.New(), "ws-remclaim-"+suffix, "Workspace reminder claim")
	require.NoError(t, err)
	wsRaw, err := res.LastInsertId()
	require.NoError(t, err)
	wsID := uint32(wsRaw) //#nosec G115 -- LastInsertId in test seed, fits uint32

	res, err = db.ExecContext(ctx,
		`INSERT INTO calendars (public_id, workspace_id, kind, name, owner_user_id)
		 VALUES (?, ?, 'personal', 'claim cal', ?)`,
		types.New(), wsID, userID)
	require.NoError(t, err)
	calRaw, err := res.LastInsertId()
	require.NoError(t, err)
	calID := uint32(calRaw) //#nosec G115 -- LastInsertId in test seed, fits uint32

	// Reminder window already open: start in 5 minutes with a 30 minute
	// offset, notified_at NULL so the claim is up for grabs.
	startAt := time.Now().UTC().Add(5 * time.Minute)
	eventPub := types.New()
	res, err = db.ExecContext(ctx,
		`INSERT INTO calendar_events (
			public_id, workspace_id, calendar_id,
			title, start_at, end_at, timezone,
			owner_user_id, created_by_user_id,
			notification_offset
		) VALUES (?, ?, ?, ?, ?, ?, 'UTC', ?, ?, 30)`,
		eventPub, wsID, calID,
		"Claim me", startAt, startAt.Add(time.Hour),
		userID, userID)
	require.NoError(t, err)
	evRaw, err := res.LastInsertId()
	require.NoError(t, err)

	return &claimFixture{
		eventID:       uint32(evRaw), //#nosec G115 -- LastInsertId in test seed, fits uint32
		eventPublicID: eventPub,
	}
}

// TestClaimReminderForDelivery_SingleWinner verifies the claim contract
// the scheduler relies on: the first conditional UPDATE flips
// notified_at from NULL to NOW() and reports one affected row, every
// subsequent attempt reports zero, and releasing the claim (the
// dispatch-failure retry path) makes the reminder claimable again.
func TestClaimReminderForDelivery_SingleWinner(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping reminder claim DB test in -short mode")
	}
	inst := helpers.StartShared(t)
	db := inst.DB
	fx := seedClaimFixture(t, db)

	ctx := context.Background()
	queries := generated.New(db)

	affected, err := queries.ClaimReminderForDelivery(ctx, fx.eventID)
	require.NoError(t, err)
	require.Equal(t, int64(1), affected, "first claim must win exactly one row")

	affected, err = queries.ClaimReminderForDelivery(ctx, fx.eventID)
	require.NoError(t, err)
	require.Equal(t, int64(0), affected, "re-claim must lose while notified_at is set")

	var notifiedAt sql.NullTime
	require.NoError(t, db.QueryRowContext(ctx,
		`SELECT notified_at FROM calendar_events WHERE id = ?`, fx.eventID,
	).Scan(&notifiedAt))
	require.True(t, notifiedAt.Valid, "winning claim must set notified_at")

	// Releasing the claim re-arms the reminder for the next tick.
	releaseReminderClaim(ctx, db, pendingNotification{
		ID:       fx.eventID,
		PublicID: fx.eventPublicID,
	})
	affected, err = queries.ClaimReminderForDelivery(ctx, fx.eventID)
	require.NoError(t, err)
	require.Equal(t, int64(1), affected, "released reminder must be claimable again")
}
