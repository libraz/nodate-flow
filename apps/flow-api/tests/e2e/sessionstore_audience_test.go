package e2e

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/libraz/nodate-flow/apps/flow-api/internal/auth/sessadapter"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/generated"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/types"
	"github.com/libraz/nodate-flow/packages/go-shared/sessionstore"
)

// TestSessionstoreAudienceIsolation pins the sessions.audience rules: flow
// writes 'flow', refreshes and looks up only its own rows, and a
// user-wide revoke reaches rows of every audience.
func TestSessionstoreAudienceIsolation(t *testing.T) {
	bootstrap(t)
	t.Parallel()

	tt := newTenant(t)
	store := sessadapter.NewMySQLStore(testDB, generated.New(testDB))

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	userPub, err := types.Parse(tt.UserPublicID)
	require.NoError(t, err)
	var userID uint32
	require.NoError(t,
		testDB.QueryRowContext(ctx, `SELECT id FROM users WHERE public_id = ?`, userPub).Scan(&userID),
	)

	// A row written by the other application (audience 'time').
	foreignPub := types.New()
	foreignHash := "audience-time-" + foreignPub.String()
	_, err = testDB.ExecContext(ctx,
		`INSERT INTO sessions (public_id, user_id, refresh_hash, user_agent, ip_address, expires_at, audience)
		 VALUES (?, ?, ?, 'go-test', '127.0.0.1', ?, 'time')`,
		foreignPub, userID, foreignHash, time.Now().Add(time.Hour).UTC())
	require.NoError(t, err)
	t.Cleanup(func() {
		_, _ = testDB.Exec(`DELETE FROM sessions WHERE public_id = ?`, foreignPub)
	})

	// A row created through flow's own store.
	ownPub := types.New()
	ownHash := "audience-flow-" + ownPub.String()
	_, err = store.Create(ctx, sessionstore.CreateParams{
		PublicID:    ownPub,
		UserID:      userID,
		RefreshHash: ownHash,
		UserAgent:   "go-test",
		IPAddress:   "127.0.0.1",
		ExpiresAt:   time.Now().Add(time.Hour).UTC(),
	})
	require.NoError(t, err)
	t.Cleanup(func() {
		_, _ = testDB.Exec(`DELETE FROM sessions WHERE public_id = ?`, ownPub)
	})

	audienceOf := func(pub types.PublicID) string {
		var a string
		require.NoError(t,
			testDB.QueryRowContext(ctx, `SELECT audience FROM sessions WHERE public_id = ?`, pub).Scan(&a))
		return a
	}
	revoked := func(pub types.PublicID) bool {
		var n int
		require.NoError(t, testDB.QueryRowContext(ctx,
			`SELECT COUNT(*) FROM sessions WHERE public_id = ? AND revoked_at IS NOT NULL AND enabled = FALSE`,
			pub).Scan(&n))
		return n == 1
	}

	require.Equal(t, "flow", audienceOf(ownPub), "flow must write its own audience")

	// Lookup: the foreign row is invisible, the own row is found.
	_, err = store.FindByRefreshHash(ctx, foreignHash)
	require.ErrorIs(t, err, sessionstore.ErrNotFound, "flow must not look up a 'time' session")
	found, err := store.FindByRefreshHash(ctx, ownHash)
	require.NoError(t, err)
	require.Equal(t, ownPub, found.PublicID)

	// Refresh: rotating the foreign hash must fail and leave the row untouched.
	err = store.RotateRefreshHash(ctx, foreignHash, foreignHash+"-rot", time.Now().Add(2*time.Hour).UTC())
	require.ErrorIs(t, err, sessionstore.ErrNotFound, "flow must not refresh a 'time' session")
	var stillHash string
	require.NoError(t,
		testDB.QueryRowContext(ctx, `SELECT refresh_hash FROM sessions WHERE public_id = ?`, foreignPub).Scan(&stillHash))
	require.Equal(t, foreignHash, stillHash)

	// The user's session list shows only flow's own rows.
	active, err := store.ListActive(ctx, userID)
	require.NoError(t, err)
	for _, s := range active {
		require.NotEqual(t, foreignPub, s.PublicID, "flow session list must not include a 'time' session")
	}

	// Revoke-all ignores audience.
	require.False(t, revoked(foreignPub))
	require.NoError(t, store.RevokeAllForUser(ctx, userID))
	require.True(t, revoked(foreignPub), "revoke-all must revoke a 'time' session")
	require.True(t, revoked(ownPub), "revoke-all must revoke the 'flow' session")
}
