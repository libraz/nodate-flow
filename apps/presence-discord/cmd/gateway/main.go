// Command gateway is the entry point for the nodate-flow Discord
// presence gateway. It hosts a single Discord WebSocket connection
// (privileged GUILD_PRESENCES + GUILD_MEMBERS intents) and translates
// each PresenceUpdate event into a signal POSTed to flow-api.
//
// Wiring mirrors apps/flow-worker/cmd/worker/main.go: config → logger →
// tracer → metrics server → gateway → signal-driven graceful shutdown.
//
// The actual wiring lives in internal/lifecycle so that lifecycle tests
// can drive the binary in-process via a cancellable context. main() is
// a thin shell that loads config, builds the logger, installs a
// SIGINT/SIGTERM handler, and translates lifecycle.Run's error into an
// exit code.
package main

import (
	"context"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/libraz/nodate-flow/apps/presence-discord/internal/config"
	"github.com/libraz/nodate-flow/apps/presence-discord/internal/lifecycle"
)

func main() {
	cfg, err := config.Load()
	if err != nil {
		// No logger yet; bare slog default so the error reaches stderr.
		slog.Error("config load failed", "err", err)
		os.Exit(1)
	}

	logger := lifecycle.NewLogger(cfg.LogLevel)
	slog.SetDefault(logger)

	// SIGINT/SIGTERM cancels the context lifecycle.Run watches.
	// Translating signals to ctx cancel keeps Run signal-agnostic so
	// tests can drive shutdown by cancelling their own context.
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	if err := lifecycle.Run(ctx, cfg, logger, nil); err != nil {
		logger.Error("gateway exited with error", "err", err)
		os.Exit(1)
	}
}
