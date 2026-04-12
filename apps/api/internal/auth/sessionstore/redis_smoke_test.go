package sessionstore

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/require"

	"github.com/nodate-flow/nodate-flow/apps/api/internal/db/types"
)

// TestRedisStoreSmoke exercises the Redis driver end-to-end against a
// real Redis / Valkey instance. It is gated on NF_TEST_REDIS_ADDR so
// CI without a broker still passes; operators point it at the compose
// service ("localhost:6379") to smoke-test the driver before rolling
// out NF_SESSION_STORE=redis. Works against both redis:7+ and
// valkey:8+ because the wire protocol is identical.
func TestRedisStoreSmoke(t *testing.T) {
	addr := os.Getenv("NF_TEST_REDIS_ADDR")
	if addr == "" {
		t.Skip("set NF_TEST_REDIS_ADDR=host:port to run the redis smoke test")
	}
	rdb := redis.NewClient(&redis.Options{Addr: addr})
	t.Cleanup(func() { _ = rdb.Close() })
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	require.NoError(t, rdb.Ping(ctx).Err(), "redis ping")

	store := NewRedisStore(rdb)

	pub := types.New()
	hash := "smoke-hash-" + pub.String()
	expires := time.Now().Add(15 * time.Minute).UTC()
	_, err := store.Create(ctx, CreateParams{
		PublicID:    pub,
		UserID:      42,
		RefreshHash: hash,
		UserAgent:   "go-test",
		IPAddress:   "127.0.0.1",
		ExpiresAt:   expires,
	})
	require.NoError(t, err)

	got, err := store.FindByRefreshHash(ctx, hash)
	require.NoError(t, err)
	require.Equal(t, uint32(42), got.UserID)

	newHash := hash + "-rot"
	newExp := expires.Add(5 * time.Minute)
	require.NoError(t, store.RotateRefreshHash(ctx, hash, newHash, newExp))

	_, err = store.FindByRefreshHash(ctx, hash)
	require.ErrorIs(t, err, ErrNotFound, "old hash must be gone after rotation")

	rotated, err := store.FindByRefreshHash(ctx, newHash)
	require.NoError(t, err)
	require.Equal(t, pub, rotated.PublicID)

	require.NoError(t, store.Revoke(ctx, 42, pub))
	_, err = store.FindByRefreshHash(ctx, newHash)
	require.ErrorIs(t, err, ErrNotFound, "revoked session must be gone")
}
