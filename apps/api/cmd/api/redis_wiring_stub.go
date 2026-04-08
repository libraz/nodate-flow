//go:build !redis

package main

import (
	"log/slog"

	"github.com/nodate-flow/nodate-flow/apps/api/internal/auth/sessionstore"
	"github.com/nodate-flow/nodate-flow/apps/api/internal/config"
	"github.com/nodate-flow/nodate-flow/apps/api/internal/db/generated"
	"github.com/nodate-flow/nodate-flow/apps/api/internal/stream"
)

// buildSessionStore stub: always returns the MySQL driver. Logs a
// warning when the env asked for Redis so operators can diagnose.
func buildSessionStore(cfg *config.Config, q *generated.Queries, logger *slog.Logger) sessionstore.Store {
	if cfg.SessionStore == "redis" {
		logger.Warn("NF_SESSION_STORE=redis requires the -tags redis build; falling back to MySQL")
	}
	return sessionstore.NewMySQLStore(q)
}

// buildStreamNotifier stub: nil so main.go falls through to the
// in-process notifier.
func buildStreamNotifier(cfg *config.Config, logger *slog.Logger) stream.Notifier {
	if cfg.StreamBackend == "redis" {
		logger.Warn("NF_STREAM_BACKEND=redis requires the -tags redis build; using in-process notifier")
	}
	return nil
}

// configureOutboundLimiters stub: always returns false so main.go
// uses the in-process ConfigureLimiter branch.
func configureOutboundLimiters(cfg *config.Config, logger *slog.Logger, _ []string) bool {
	if cfg.OutboundBackend == "redis" {
		logger.Warn("NF_OUTBOUND_BACKEND=redis requires the -tags redis build; using in-process limiter")
	}
	return false
}
