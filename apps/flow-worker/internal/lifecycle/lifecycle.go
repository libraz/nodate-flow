// Package lifecycle wires the flow-worker process boot sequence so the
// main binary (cmd/worker) and the lifecycle tests (tests/) can share a
// single source of truth.
//
// Boot order mirrors apps/flow-api/cmd/api/main.go:
//
//	config → logger → tracer → MySQL → metrics server → job runner →
//	(block on ctx cancel or fatal subsystem error) → graceful shutdown
//
// All failures return wrapped errors; the package never calls os.Exit so
// tests can assert on the error path without forking a subprocess.
package lifecycle

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"runtime/debug"
	"strings"
	"time"

	// Side effect: register the MySQL database/sql driver.
	_ "github.com/go-sql-driver/mysql"

	"github.com/nodate-flow/nodate-flow/apps/flow-worker/internal/config"
	"github.com/nodate-flow/nodate-flow/apps/flow-worker/internal/jobs"
	"github.com/nodate-flow/nodate-flow/apps/flow-worker/internal/obs"
	"github.com/nodate-flow/nodate-flow/packages/go-shared/logutil"
)

// ServiceName is reported as the slog "service" field and the OTel
// service.name attribute. Exported so cmd/worker (and any future
// admin tool) sees the same constant.
const ServiceName = "flow-worker"

// RunOptions tunes Run for tests and production. Production callers
// (cmd/worker) use Register to plug in concrete jobs that need the
// resolved DB pool; tests use it to plug in fake jobs without exporting
// the runner.
type RunOptions struct {
	// Register is invoked with the live *jobs.Runner and the resolved
	// *sql.DB pool before Start is called. The function MUST NOT call
	// Start itself; lifecycle.Run owns the runner lifecycle.
	//
	// The DB pool is passed in so production jobs (calendar_event_day
	// and beyond) can read from MySQL without owning a connection
	// pool of their own — pool sizing stays a single decision in
	// config.Config. Tests that do not need a DB simply ignore the
	// argument.
	Register func(r *jobs.Runner, db *sql.DB)
	// MetricsReady, when non-nil, is closed once the metrics HTTP server
	// goroutine has been launched. Tests use this to avoid racing the
	// goroutine that calls ListenAndServe; production passes nil.
	MetricsReady chan<- struct{}
}

// NewLogger builds a JSON slog.Logger with the redact handler from
// packages/go-shared/logutil and a default service=flow-worker attr so
// all worker logs are filterable in aggregation. Exposed for cmd/worker
// and as a convenience for tests that want production-shaped logs.
func NewLogger(levelStr string) *slog.Logger {
	level := parseLevel(levelStr)
	base := slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: level})
	redacted := logutil.NewRedactHandler(base)
	return slog.New(redacted).With(
		slog.String("service", ServiceName),
		slog.String("version", ResolveVersion()),
	)
}

// ResolveVersion returns NF_FLOW_VERSION, the Go build main module
// version, or "dev". Used for OTel service.version and the slog
// "version" base attribute.
func ResolveVersion() string {
	if v := strings.TrimSpace(os.Getenv("NF_FLOW_VERSION")); v != "" {
		return v
	}
	if info, ok := debug.ReadBuildInfo(); ok && info.Main.Version != "" && info.Main.Version != "(devel)" {
		return info.Main.Version
	}
	return "dev"
}

// Run wires the worker process and blocks until ctx is cancelled or a
// fatal subsystem error (metrics server bind failure, DB ping failure)
// occurs. Returns nil on graceful shutdown, a wrapped error otherwise.
//
// Run is the single source of truth for the boot sequence; cmd/worker
// is a thin shell around it. Lifecycle tests call Run directly so they
// don't need to fork a subprocess.
func Run(ctx context.Context, cfg *config.Config, logger *slog.Logger, opts *RunOptions) error {
	if cfg == nil {
		return errors.New("lifecycle: cfg is required")
	}
	if logger == nil {
		logger = NewLogger(cfg.LogLevel)
	}
	if opts == nil {
		opts = &RunOptions{}
	}

	// OpenTelemetry. When NF_OTEL_EXPORTER_OTLP_ENDPOINT is empty the
	// provider is a no-op, matching flow-api behaviour.
	traceShutdown, err := obs.InitTracer(context.Background(), obs.TracerConfig{
		Endpoint:       cfg.OTelEndpoint,
		ServiceName:    ServiceName,
		ServiceVersion: ResolveVersion(),
		Insecure:       cfg.OTelInsecure,
	})
	if err != nil {
		return fmt.Errorf("otel tracer init: %w", err)
	}
	defer func() {
		shutCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if shutErr := traceShutdown(shutCtx); shutErr != nil {
			logger.Error("otel tracer shutdown failed", "err", shutErr)
		}
	}()
	if cfg.OTelEndpoint != "" {
		logger.Info("otel tracing enabled", "endpoint", cfg.OTelEndpoint)
	}

	db, err := openAndPingDB(cfg, logger)
	if err != nil {
		return err
	}
	defer func() {
		if closeErr := db.Close(); closeErr != nil {
			logger.Warn("db close failed", "err", closeErr)
		}
	}()

	// Internal-only Prometheus listener. Worker has no public HTTP
	// surface, so this is the only network port we open.
	metricsMux := http.NewServeMux()
	metricsMux.Handle("/metrics", obs.Handler())
	metricsAddr := ":" + cfg.MetricsPort
	metricsSrv := &http.Server{
		Addr:              metricsAddr,
		Handler:           metricsMux,
		ReadHeaderTimeout: 5 * time.Second,
	}
	metricsErr := make(chan error, 1)
	go func() {
		logger.Info("metrics server listening", "addr", metricsAddr)
		if err := metricsSrv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			metricsErr <- err
		}
		close(metricsErr)
	}()

	// Up gauge flips on once MySQL is reachable and the metrics endpoint
	// is bound. Flipped back to 0 during graceful shutdown so dashboards
	// can distinguish "draining" from "dead".
	obs.UpGauge.Set(1)
	if opts.MetricsReady != nil {
		close(opts.MetricsReady)
	}

	// Job runner. W1 starts with no jobs registered so it just ticks
	// quietly at debug level — proving the loop, the signal handler, and
	// the shutdown drain all work end-to-end. W2 will Register the
	// calendar_event_day job here before Start.
	runner := &jobs.Runner{
		Interval:        cfg.JobTickInterval,
		Logger:          logger,
		ShutdownTimeout: cfg.JobShutdownTimeout,
	}
	if opts.Register != nil {
		opts.Register(runner, db)
	}

	runnerCtx, runnerCancel := context.WithCancel(context.Background())
	defer runnerCancel()
	if err := runner.Start(runnerCtx); err != nil {
		return fmt.Errorf("job runner start: %w", err)
	}
	logger.Info("job runner started",
		"interval", cfg.JobTickInterval,
		"shutdown_timeout", cfg.JobShutdownTimeout,
	)

	// Block on either a fatal metrics-server error or context cancel
	// (driven by SIGINT/SIGTERM in cmd/worker, by the test in
	// lifecycle_test).
	var runErr error
	select {
	case err := <-metricsErr:
		if err != nil {
			obs.UpGauge.Set(0)
			runErr = fmt.Errorf("metrics server exited: %w", err)
		}
	case <-ctx.Done():
		logger.Info("shutdown signal received")
	}

	// Graceful shutdown. Cancel the runner's context so the next tick
	// boundary observes ctx.Done(), then wait for the in-flight tick to
	// finish (capped by JobShutdownTimeout). Finally drain the metrics
	// server and close the DB. Order matches flow-api.
	runnerCancel()
	runner.Stop()

	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), cfg.JobShutdownTimeout)
	defer shutdownCancel()
	if err := metricsSrv.Shutdown(shutdownCtx); err != nil {
		logger.Error("metrics server shutdown failed", "err", err)
	}

	obs.UpGauge.Set(0)
	logger.Info("shutdown complete")
	return runErr
}

// openAndPingDB opens the MySQL pool described by cfg and verifies the
// connection with a Ping. Returns a wrapped error on either failure so
// Run can surface it to cmd/worker (which exits non-zero) without
// losing the underlying driver message.
func openAndPingDB(cfg *config.Config, logger *slog.Logger) (*sql.DB, error) {
	db, err := sql.Open("mysql", cfg.DBDSN)
	if err != nil {
		logger.Error("db open failed", "err", err)
		return nil, fmt.Errorf("db open: %w", err)
	}
	db.SetMaxOpenConns(cfg.DBMaxOpenConns)
	db.SetMaxIdleConns(cfg.DBMaxIdleConns)
	db.SetConnMaxLifetime(cfg.DBConnMaxLifetime)
	if pingErr := db.Ping(); pingErr != nil {
		logger.Error("db ping failed", "err", pingErr)
		_ = db.Close()
		return nil, fmt.Errorf("db ping: %w", pingErr)
	}
	return db, nil
}

// parseLevel mirrors the env values validated by config.Validate.
func parseLevel(s string) slog.Level {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "debug":
		return slog.LevelDebug
	case "warn", "warning":
		return slog.LevelWarn
	case "error":
		return slog.LevelError
	default:
		return slog.LevelInfo
	}
}
