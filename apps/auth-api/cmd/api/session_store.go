package main

import (
	"context"
	"database/sql"
	"fmt"
	"log/slog"
	"time"

	"github.com/redis/go-redis/v9"

	"github.com/libraz/nodate-flow/apps/auth-api/internal/auth/sessadapter"
	"github.com/libraz/nodate-flow/apps/auth-api/internal/config"
	"github.com/libraz/nodate-flow/apps/auth-api/internal/db/generated"
	"github.com/libraz/nodate-flow/packages/go-shared/sessionstore"
)

// redisDialTimeout bounds the startup reachability check. It only has to
// be long enough to reach a Redis on the same network, because the point
// is to turn an unreachable store into a refused start rather than into
// a login endpoint that 500s.
const redisDialTimeout = 5 * time.Second

// buildSessionStore returns the refresh-token session driver named by
// NF_AUTH_SESSION_STORE, plus a closer for whatever it opened.
//
// Selecting redis and getting mysql anyway would be worse than refusing
// to start: sessions would silently live somewhere other than where the
// operator configured, and "sign out of all other devices" would then
// clear a store nobody is reading. So a redis selection that cannot be
// satisfied is a startup failure, not a fallback.
func buildSessionStore(
	ctx context.Context,
	cfg *config.Config,
	db *sql.DB,
	q *generated.Queries,
	logger *slog.Logger,
) (sessionstore.Store, func(), error) {
	if cfg.SessionStore != "redis" {
		logger.Info("session store: mysql")
		return sessadapter.NewMySQLStore(db, q), func() {}, nil
	}
	if cfg.RedisAddr == "" {
		return nil, nil, fmt.Errorf("config: NF_AUTH_SESSION_STORE=redis requires NF_REDIS_ADDR")
	}
	rdb := redis.NewClient(&redis.Options{Addr: cfg.RedisAddr})
	pingCtx, cancel := context.WithTimeout(ctx, redisDialTimeout)
	defer cancel()
	if err := rdb.Ping(pingCtx).Err(); err != nil {
		_ = rdb.Close()
		return nil, nil, fmt.Errorf("session store: redis at %s unreachable: %w", cfg.RedisAddr, err)
	}
	logger.Info("session store: redis", "addr", cfg.RedisAddr)
	return sessionstore.NewRedisStore(rdb), func() { _ = rdb.Close() }, nil
}
