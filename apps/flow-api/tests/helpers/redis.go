package helpers

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/testcontainers/testcontainers-go/modules/redis"
)

// RedisInstance is a running Valkey/Redis container and the host:port
// address that go-redis clients should connect to.
type RedisInstance struct {
	Container *redis.RedisContainer
	// Addr is the host:port suitable for NF_REDIS_ADDR / redis.Options.Addr.
	Addr string
}

const (
	// valkeyImage matches the compose.yml service definition.
	valkeyImage = "valkey/valkey:8-alpine"
)

var (
	sharedRedisOnce sync.Once
	sharedRedisInst *RedisInstance
	sharedRedisErr  error
)

// StartSharedRedis returns a process-wide Valkey instance. Subsequent
// callers receive the same handle. The container is never explicitly
// terminated; testcontainers-ryuk reaps it when the process exits.
func StartSharedRedis(t *testing.T) *RedisInstance {
	t.Helper()
	sharedRedisOnce.Do(func() {
		sharedRedisInst, sharedRedisErr = startRedis(context.Background())
	})
	require.NoError(t, sharedRedisErr, "shared Redis container failed to start")
	require.NotNil(t, sharedRedisInst)
	return sharedRedisInst
}

// EnsureSharedRedis is the same as StartSharedRedis but without a
// *testing.T dependency, so it can be called from TestMain.
func EnsureSharedRedis() (*RedisInstance, error) {
	sharedRedisOnce.Do(func() {
		sharedRedisInst, sharedRedisErr = startRedis(context.Background())
	})
	return sharedRedisInst, sharedRedisErr
}

// StartIsolatedRedis returns a brand new Valkey container, terminated
// when the test ends.
func StartIsolatedRedis(t *testing.T) *RedisInstance {
	t.Helper()
	inst, err := startRedis(context.Background())
	require.NoError(t, err)
	t.Cleanup(func() {
		_ = inst.Container.Terminate(context.Background())
	})
	return inst
}

// startRedis boots a Valkey 8 container (Redis-compatible) using the
// testcontainers redis module, matching the compose.yml flags
// (appendonly=no, no persistence).
func startRedis(ctx context.Context) (*RedisInstance, error) {
	startCtx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()

	container, err := redis.Run(
		startCtx,
		valkeyImage,
	)
	if err != nil {
		return nil, fmt.Errorf("start redis container: %w", err)
	}

	host, err := container.Host(ctx)
	if err != nil {
		_ = container.Terminate(ctx)
		return nil, fmt.Errorf("redis host: %w", err)
	}

	port, err := container.MappedPort(ctx, "6379/tcp")
	if err != nil {
		_ = container.Terminate(ctx)
		return nil, fmt.Errorf("redis mapped port: %w", err)
	}

	addr := fmt.Sprintf("%s:%s", host, port.Port())

	return &RedisInstance{Container: container, Addr: addr}, nil
}
