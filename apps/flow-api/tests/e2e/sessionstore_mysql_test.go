package e2e

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/nodate-flow/nodate-flow/apps/flow-api/internal/auth/sessadapter"
	"github.com/nodate-flow/nodate-flow/apps/flow-api/internal/db/generated"
	"github.com/nodate-flow/nodate-flow/packages/go-shared/sessionstore"
	"github.com/nodate-flow/nodate-flow/apps/flow-api/internal/db/types"
)

// TestSessionstoreMySQLDriver exercises the MySQL [sessionstore.Store]
// driver end-to-end against the shared test database. The auth flow
// already exercises the driver indirectly; this test pins the
// contract (Create → Find → Rotate → Find → Revoke → Find) so a
// future Redis driver can be swapped in under the same assertions.
func TestSessionstoreMySQLDriver(t *testing.T) {
	bootstrap(t)
	t.Parallel()

	tt := newTenant(t)
	queries := generated.New(testDB)
	store := sessadapter.NewMySQLStore(testDB, queries)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// Resolve the tenant's internal user id via the public id.
	userPub, err := types.Parse(tt.UserPublicID)
	require.NoError(t, err)
	var userID uint32
	require.NoError(t,
		testDB.QueryRowContext(ctx, `SELECT id FROM users WHERE public_id = ?`, userPub).Scan(&userID),
	)

	pub := types.New()
	hash := "mysql-driver-test-" + pub.String()
	expires := time.Now().Add(15 * time.Minute).UTC()

	_, err = store.Create(ctx, sessionstore.CreateParams{
		PublicID:    pub,
		UserID:      userID,
		RefreshHash: hash,
		UserAgent:   "go-test",
		IPAddress:   "127.0.0.1",
		ExpiresAt:   expires,
	})
	require.NoError(t, err)
	t.Cleanup(func() {
		_, _ = testDB.Exec(`DELETE FROM sessions WHERE public_id = ?`, pub)
	})

	got, err := store.FindByRefreshHash(ctx, hash)
	require.NoError(t, err)
	require.Equal(t, userID, got.UserID)
	require.Equal(t, pub, got.PublicID)

	newHash := hash + "-rot"
	newExp := expires.Add(5 * time.Minute)
	require.NoError(t, store.RotateRefreshHash(ctx, hash, newHash, newExp))

	_, err = store.FindByRefreshHash(ctx, hash)
	require.ErrorIs(t, err, sessionstore.ErrNotFound, "old hash must be gone after rotation")

	rotated, err := store.FindByRefreshHash(ctx, newHash)
	require.NoError(t, err)
	require.Equal(t, pub, rotated.PublicID)

	require.NoError(t, store.Revoke(ctx, userID, pub))
	_, err = store.FindByRefreshHash(ctx, newHash)
	require.ErrorIs(t, err, sessionstore.ErrNotFound, "revoked session must not be findable")
}
