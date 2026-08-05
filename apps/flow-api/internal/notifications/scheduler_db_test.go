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

	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/generated"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/types"
	"github.com/libraz/nodate-flow/apps/flow-api/tests/helpers"
	"github.com/libraz/nodate-flow/packages/go-shared/testhelpers"
)

// claimFixture is the minimum scaffolding a claim test needs: one
// calendar event (with its owning workspace / calendar / user) whose
// reminder window is open and notified_at is still NULL.
type claimFixture struct {
	workspaceID   uint32
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
		workspaceID:   wsID,
		eventID:       uint32(evRaw), //#nosec G115 -- LastInsertId in test seed, fits uint32
		eventPublicID: eventPub,
	}
}

// TestClaimReminderOccurrence_SingleWinner verifies the claim contract
// the scheduler relies on: the first INSERT creates the (event,
// occurrence) row and reports one affected row, every subsequent attempt
// reports zero, and releasing the claim (the dispatch-failure retry
// path) makes that occurrence claimable again.
func TestClaimReminderOccurrence_SingleWinner(t *testing.T) {
	testhelpers.SkipUnlessIntegration(t)
	inst := helpers.StartShared(t)
	db := inst.DB
	fx := seedClaimFixture(t, db)

	ctx := context.Background()
	queries := generated.New(db)
	occurrence := time.Date(2030, 6, 3, 9, 0, 0, 0, time.UTC)

	claim := func() (int64, error) {
		return queries.ClaimReminderOccurrence(ctx, generated.ClaimReminderOccurrenceParams{
			WorkspaceID:     fx.workspaceID,
			EventID:         fx.eventID,
			OccurrenceStart: occurrence,
		})
	}

	affected, err := claim()
	require.NoError(t, err)
	require.Equal(t, int64(1), affected, "first claim must win exactly one row")

	affected, err = claim()
	require.NoError(t, err)
	require.Equal(t, int64(0), affected, "re-claim must lose while the row exists")

	// A different occurrence of the same event is a separate claim. This
	// is the whole point of the table: one reminder per occurrence, not
	// one for the lifetime of the row.
	affected, err = queries.ClaimReminderOccurrence(ctx, generated.ClaimReminderOccurrenceParams{
		WorkspaceID:     fx.workspaceID,
		EventID:         fx.eventID,
		OccurrenceStart: occurrence.AddDate(0, 0, 7),
	})
	require.NoError(t, err)
	require.Equal(t, int64(1), affected, "the next week's occurrence must be claimable")

	// Releasing re-arms that occurrence for the next tick.
	releaseReminderClaim(ctx, db, pendingNotification{
		ID:              fx.eventID,
		PublicID:        fx.eventPublicID,
		OccurrenceStart: occurrence,
	})
	affected, err = claim()
	require.NoError(t, err)
	require.Equal(t, int64(1), affected, "a released occurrence must be claimable again")
}
