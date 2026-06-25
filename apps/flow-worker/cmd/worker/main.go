// Command worker is the entry point for the nodate-flow scheduled job
// runner. It hosts cron-like jobs (calendar_event_day materialiser in W2,
// future ones in later phases) that previously lived inline in flow-api.
//
// Wiring mirrors apps/flow-api/cmd/api/main.go: config → logger → tracer →
// MySQL → metrics server → job runner → signal-driven graceful shutdown.
//
// The actual wiring lives in internal/lifecycle so that lifecycle tests
// can drive the binary in-process via a cancellable context. main() is
// a thin shell that loads config, builds the logger, installs a
// SIGINT/SIGTERM handler, and translates lifecycle.Run's error into an
// exit code.
package main

import (
	"context"
	"database/sql"
	"log/slog"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"github.com/nodate-flow/nodate-flow/apps/flow-worker/internal/config"
	"github.com/nodate-flow/nodate-flow/apps/flow-worker/internal/jobs"
	"github.com/nodate-flow/nodate-flow/apps/flow-worker/internal/jobs/calendar_event_day"
	"github.com/nodate-flow/nodate-flow/apps/flow-worker/internal/lifecycle"
)

// registerProductionJobs is invoked by lifecycle.Run with the live
// *jobs.Runner and *sql.DB once the database pool and metrics endpoint
// are up. The hook gives main() one place to construct concrete jobs
// from the resolved config without polluting lifecycle.Run with per-
// job dependencies.
//
// A nil service token disables the calendar event-day job: emitting
// unauthenticated POST /signals would just log per-tick 401s, which
// would be loud and useless. The deploy still boots so operators can
// roll out the worker binary before flipping the token on flow-api.
func registerProductionJobs(cfg *config.Config, logger *slog.Logger) func(*jobs.Runner, *sql.DB) {
	return func(runner *jobs.Runner, db *sql.DB) {
		if strings.TrimSpace(cfg.ServiceToken) == "" {
			logger.Warn("flow-worker: calendar_event_day disabled (NF_FLOW_API_SIGNAL_TOKEN unset)")
			return
		}
		userAgent := "flow-worker/" + lifecycle.ResolveVersion()
		job, err := calendar_event_day.New(db, cfg.FlowAPIBaseURL, cfg.ServiceToken, userAgent, logger)
		if job != nil {
			// Keep the fire-once scan window aligned with the cadence the
			// runner actually ticks the job at; a mismatch would skip a
			// day boundary that landed between ticks.
			job.TickInterval = cfg.JobTickInterval
		}
		if err != nil {
			// Surfacing this as a fatal would prevent the rest of the
			// runner from booting; instead, log loudly and skip
			// registration so future jobs (which may have separate
			// failure modes) still run. The /metrics page makes the
			// missing tick counter obvious to operators.
			logger.Error("flow-worker: calendar_event_day construction failed", "err", err)
			return
		}
		runner.Register(job)
		logger.Info("flow-worker: calendar_event_day registered",
			"flow_api_base_url", cfg.FlowAPIBaseURL,
			"tick_interval", cfg.JobTickInterval,
		)
	}
}

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

	// The Register hook receives the live runner and DB pool before
	// Start is called; this is the seam lifecycle.RunOptions exposes
	// so tests and production share one boot path.
	opts := &lifecycle.RunOptions{
		Register: registerProductionJobs(cfg, logger),
	}
	if err := lifecycle.Run(ctx, cfg, logger, opts); err != nil {
		logger.Error("worker exited with error", "err", err)
		os.Exit(1)
	}
}
