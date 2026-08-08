package main

import (
	"context"
	"io"
	"log/slog"
	"net"
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/libraz/nodate-flow/apps/auth-api/internal/config"
	"github.com/libraz/nodate-flow/packages/go-shared/sessionstore"
)

func discardLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

// redisAddrForTest returns a reachable Redis address, or skips. The
// compose stack publishes one on 127.0.0.1:6379; NF_REDIS_ADDR overrides.
func redisAddrForTest(t *testing.T) string {
	t.Helper()
	addr := os.Getenv("NF_REDIS_ADDR")
	if addr == "" {
		addr = "127.0.0.1:6379"
	}
	conn, err := net.DialTimeout("tcp", addr, time.Second) //#nosec G704 -- test-only reachability probe; the address is a loopback default or an operator-supplied test override
	if err != nil {
		t.Skipf("no reachable redis at %s: %v", addr, err)
	}
	_ = conn.Close()
	return addr
}

// TestBuildSessionStore_DefaultsToMySQL pins the driver a deployment
// gets when it says nothing.
func TestBuildSessionStore_DefaultsToMySQL(t *testing.T) {
	t.Parallel()
	store, closeFn, err := buildSessionStore(
		context.Background(), &config.Config{SessionStore: "mysql"}, nil, nil, discardLogger())
	require.NoError(t, err)
	require.NotNil(t, closeFn)
	defer closeFn()
	assert.IsType(t, &sessionstore.MySQLStore{}, store)
}

// TestBuildSessionStore_RedisSelectionIsHonoured is the wiring proof: a
// deployment that asks for Redis must actually get the Redis driver.
// The selector existed, was validated, and was documented for a long
// time while nothing read it, so every session still landed in MySQL and
// the setting was a silent no-op.
func TestBuildSessionStore_RedisSelectionIsHonoured(t *testing.T) {
	t.Parallel()
	addr := redisAddrForTest(t)

	store, closeFn, err := buildSessionStore(
		context.Background(),
		&config.Config{SessionStore: "redis", RedisAddr: addr},
		nil, nil, discardLogger())
	require.NoError(t, err)
	require.NotNil(t, closeFn)
	defer closeFn()
	assert.IsType(t, &sessionstore.RedisStore{}, store,
		"NF_AUTH_SESSION_STORE=redis must select the redis driver, not fall through to MySQL")
}

// TestBuildSessionStore_RedisWithoutAddrFailsClosed and its unreachable
// sibling pin the refusal. Falling back to MySQL would put sessions
// somewhere other than where the operator configured, and every symptom
// of that — a "sign out everywhere" that clears an unread store — is
// silent.
func TestBuildSessionStore_RedisWithoutAddrFailsClosed(t *testing.T) {
	t.Parallel()
	store, _, err := buildSessionStore(
		context.Background(), &config.Config{SessionStore: "redis"}, nil, nil, discardLogger())
	require.Error(t, err, "a redis selection with no address must refuse the start")
	assert.Nil(t, store)
	assert.Contains(t, err.Error(), "NF_REDIS_ADDR")
}

func TestBuildSessionStore_UnreachableRedisFailsClosed(t *testing.T) {
	t.Parallel()
	// Port 1 on the loopback interface refuses connections promptly.
	store, _, err := buildSessionStore(
		context.Background(),
		&config.Config{SessionStore: "redis", RedisAddr: "127.0.0.1:1"},
		nil, nil, discardLogger())
	require.Error(t, err, "an unreachable redis must refuse the start, not fall back to MySQL")
	assert.Nil(t, store)
}
