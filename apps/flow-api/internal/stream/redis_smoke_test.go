package stream

import (
	"context"
	"log/slog"
	"os"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/require"
)

// TestRedisNotifierSmoke publishes one event over a live Redis /
// Valkey instance and asserts a local Subscribe call receives it.
// Gated on NF_TEST_REDIS_ADDR so the default CI matrix skips it.
func TestRedisNotifierSmoke(t *testing.T) {
	addr := os.Getenv("ND_TEST_REDIS_ADDR")
	if addr == "" {
		t.Skip("set NF_TEST_REDIS_ADDR=host:port to run the redis stream smoke test")
	}
	rdb := redis.NewClient(&redis.Options{Addr: addr})
	t.Cleanup(func() { _ = rdb.Close() })
	pingCtx, pingCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer pingCancel()
	require.NoError(t, rdb.Ping(pingCtx).Err())

	n := NewRedisNotifier(rdb, slog.Default())
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	ws := "smoke-ws"
	ch := n.Subscribe(ctx, ws)
	// Give Redis a beat to register the subscription before publish.
	time.Sleep(100 * time.Millisecond)

	n.Publish(ctx, Event{Kind: KindTaskChanged, WorkspaceID: ws})

	select {
	case got, ok := <-ch:
		require.True(t, ok, "channel closed before event arrived")
		require.Equal(t, KindTaskChanged, got.Kind)
		require.Equal(t, ws, got.WorkspaceID)
	case <-time.After(3 * time.Second):
		t.Fatal("timed out waiting for event")
	}
}
