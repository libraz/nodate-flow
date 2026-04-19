package main

import (
	"database/sql"
	"log/slog"

	"github.com/redis/go-redis/v9"

	"github.com/nodate-flow/nodate-flow/apps/flow-api/internal/ai/providers"
	"github.com/nodate-flow/nodate-flow/apps/flow-api/internal/auth/sessadapter"
	"github.com/nodate-flow/nodate-flow/apps/flow-api/internal/config"
	"github.com/nodate-flow/nodate-flow/packages/go-shared/sessionstore"
	"github.com/nodate-flow/nodate-flow/apps/flow-api/internal/db/generated"
	"github.com/nodate-flow/nodate-flow/apps/flow-api/internal/outbound"
	"github.com/nodate-flow/nodate-flow/apps/flow-api/internal/stream"
)

// buildSessionStore selects the Redis driver when NF_FLOW_SESSION_STORE=redis
// and a Redis client is available; otherwise it falls through to the
// MySQL driver.
func buildSessionStore(cfg *config.Config, db *sql.DB, q *generated.Queries, logger *slog.Logger) sessionstore.Store {
	if cfg.SessionStore != "redis" {
		return sessadapter.NewMySQLStore(db, q)
	}
	rdb := dialRedis(cfg, logger)
	if rdb == nil {
		return sessadapter.NewMySQLStore(db, q)
	}
	logger.Info("session store: redis", "addr", cfg.RedisAddr)
	return sessionstore.NewRedisStore(rdb)
}

// buildStreamNotifier returns a RedisNotifier when the env asks for
// it, otherwise nil so main.go falls through to the in-process path.
func buildStreamNotifier(cfg *config.Config, logger *slog.Logger) stream.Notifier {
	if cfg.StreamBackend != "redis" {
		return nil
	}
	rdb := dialRedis(cfg, logger)
	if rdb == nil {
		return nil
	}
	logger.Info("stream notifier: redis", "addr", cfg.RedisAddr)
	return stream.NewRedisNotifier(rdb, logger)
}

// configureOutboundLimiters swaps in RedisLimiter for each LLM
// destination when NF_FLOW_OUTBOUNF_BACKEND=redis. Called from main.go
// instead of the default in-process ConfigureLimiter branch.
func configureOutboundLimiters(cfg *config.Config, logger *slog.Logger, dests []string) bool {
	if cfg.OutboundBackend != "redis" {
		return false
	}
	rdb := dialRedis(cfg, logger)
	if rdb == nil {
		return false
	}
	burst := cfg.OutboundLlmBurst
	if burst <= 0 {
		if b := int(cfg.OutboundLlmRps); b > 0 {
			burst = b
		} else {
			burst = 1
		}
	}
	for _, dest := range dests {
		lim := outbound.NewRedisLimiter(rdb, dest, cfg.OutboundLlmRps, burst)
		providers.ConfigureLimiter(dest, lim)
	}
	logger.Info("outbound limiter: redis", "addr", cfg.RedisAddr, "rps", cfg.OutboundLlmRps)
	return true
}

// dialRedis constructs a client from NF_REDIS_ADDR. On an empty addr
// it logs and returns nil so the caller can fall back gracefully.
func dialRedis(cfg *config.Config, logger *slog.Logger) *redis.Client {
	if cfg.RedisAddr == "" {
		logger.Error("redis backend requested but NF_REDIS_ADDR is empty; falling back")
		return nil
	}
	return redis.NewClient(&redis.Options{Addr: cfg.RedisAddr})
}
