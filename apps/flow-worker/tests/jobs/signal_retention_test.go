package jobs

import (
	"context"
	"database/sql"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/libraz/nodate-flow/apps/flow-api/tests/helpers"
	"github.com/libraz/nodate-flow/apps/flow-worker/internal/jobs/signal_retention"
)

// insertSignal writes one signals row with the given expiry and applied
// instants (nil for NULL) and returns its internal id.
func insertSignal(t *testing.T, db *sql.DB, wsID uint32, externalID string, expiresAt, appliedAt *time.Time) int64 {
	t.Helper()
	res, err := db.ExecContext(context.Background(),
		`INSERT INTO signals (public_id, workspace_id, source, kind, external_id, payload_json,
			received_at, subject_type, expires_at, applied_at)
		 VALUES (UUID_TO_BIN(UUID(), 0), ?, 'discord', 'discord.presence', ?, '{}',
			NOW(3), 'workspace', ?, ?)`,
		wsID, externalID, expiresAt, appliedAt)
	require.NoError(t, err, "insert signal %s", externalID)
	id, err := res.LastInsertId()
	require.NoError(t, err)
	return id
}

// signalExists reports whether the signals row with internal id is present.
func signalExists(t *testing.T, db *sql.DB, id int64) bool {
	t.Helper()
	var n int
	require.NoError(t, db.QueryRowContext(context.Background(),
		`SELECT COUNT(*) FROM signals WHERE id = ?`, id).Scan(&n))
	return n == 1
}

// TestSignalRetentionDeletesOnlyExpiredUnappliedSignals drives one sweep
// against real rows: an expired, unapplied signal is gone afterwards, and
// an applied one, one with no expiry, and one not yet expired are kept.
func TestSignalRetentionDeletesOnlyExpiredUnappliedSignals(t *testing.T) {
	h := getHarness(t)
	tt := helpers.CreateCalendarTestTenant(t, h.srv)

	now := time.Now().UTC().Truncate(time.Millisecond)
	past := now.Add(-time.Hour)
	future := now.Add(time.Hour)

	expired := insertSignal(t, h.db, tt.WorkspaceID, "retention-expired", &past, nil)
	applied := insertSignal(t, h.db, tt.WorkspaceID, "retention-applied", &past, &past)
	noExpiry := insertSignal(t, h.db, tt.WorkspaceID, "retention-no-expiry", nil, nil)
	notYet := insertSignal(t, h.db, tt.WorkspaceID, "retention-not-yet", &future, nil)

	job, err := signal_retention.New(h.db, silentLogger())
	require.NoError(t, err)
	require.NoError(t, job.Tick(context.Background(), now))

	require.False(t, signalExists(t, h.db, expired), "an expired, unapplied signal must be deleted")
	require.True(t, signalExists(t, h.db, applied), "an applied signal must be kept")
	require.True(t, signalExists(t, h.db, noExpiry), "a signal with no expiry must be kept")
	require.True(t, signalExists(t, h.db, notYet), "a signal not yet expired must be kept")
}
