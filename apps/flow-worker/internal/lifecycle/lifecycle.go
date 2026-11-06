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

	"github.com/libraz/nodate-flow/apps/flow-worker/internal/config"
	"github.com/libraz/nodate-flow/apps/flow-worker/internal/jobs"
	"github.com/libraz/nodate-flow/apps/flow-worker/internal/jobs/calendar_event_day"
	"github.com/libraz/nodate-flow/apps/flow-worker/internal/obs"
	"github.com/libraz/nodate-flow/packages/go-shared/dbtz"
	"github.com/libraz/nodate-flow/packages/go-shared/logutil"
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
	if ctx == nil {
		ctx = context.Background()
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
	if cfg.OTelEndpoint != "" {
		logger.Info("otel tracing enabled", "endpoint", cfg.OTelEndpoint)
	}

	db, err := openAndPingDB(cfg, logger)
	if err != nil {
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 5*time.Second)
		cleanupErr := runContextBound(cleanupCtx, traceShutdown)
		cleanupCancel()
		if cleanupErr != nil {
			logger.Error("otel tracer shutdown failed after db startup error", "err", cleanupErr)
		}
		return errors.Join(err, cleanupErr)
	}

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
	metricsDone := make(chan struct{})
	go func() {
		defer close(metricsDone)
		logger.Info("metrics server listening", "addr", metricsAddr)
		if err := metricsSrv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			metricsErr <- err
		}
		close(metricsErr)
	}()
	shutdownMetrics := func(ctx context.Context) error {
		shutdownErr := metricsSrv.Shutdown(ctx)
		if shutdownErr != nil {
			closeErr := metricsSrv.Close()
			if closeErr != nil && !errors.Is(closeErr, http.ErrServerClosed) {
				shutdownErr = errors.Join(shutdownErr, closeErr)
			}
		}
		select {
		case <-metricsDone:
		case <-ctx.Done():
			shutdownErr = errors.Join(shutdownErr, ctx.Err())
		}
		return shutdownErr
	}

	// Job runner. Jobs are registered before the up gauge is decided,
	// because whether this process is doing anything is part of what
	// "up" means here.
	runner := &jobs.Runner{
		Interval: cfg.JobTickInterval,
		Logger:   logger,
	}
	if opts.Register != nil {
		opts.Register(runner, db)
	}

	ReportRunnerHealth(logger, runner.Names())
	if opts.MetricsReady != nil {
		close(opts.MetricsReady)
	}

	runnerCtx, runnerCancel := context.WithCancel(context.Background())
	if err := runner.Start(runnerCtx); err != nil {
		startupErr := fmt.Errorf("job runner start: %w", err)
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 5*time.Second)
		cleanupErr := finishShutdown(cleanupCtx, runnerCancel, runner.Shutdown, shutdownMetrics, traceShutdown, db.Close)
		cleanupCancel()
		return errors.Join(startupErr, cleanupErr)
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

	// The configured shutdown budget covers the runner, metrics listener,
	// tracing provider, and DB close together. Create it before signaling
	// any component so later drains cannot reset the deadline.
	obs.UpGauge.Set(0)
	shutdownBudget := cfg.JobShutdownTimeout
	if shutdownBudget <= 0 {
		shutdownBudget = 30 * time.Second
	}
	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), shutdownBudget)
	shutdownErr := finishShutdown(shutdownCtx, runnerCancel, runner.Shutdown, shutdownMetrics, traceShutdown, db.Close)
	shutdownCancel()
	if shutdownErr != nil {
		logger.Error("graceful shutdown incomplete", "err", shutdownErr)
	}
	obs.UpGauge.Set(0)
	logger.Info("shutdown complete")
	return errors.Join(runErr, shutdownErr)
}

// finishShutdown applies one deadline to every flow-worker drain. It sends
// the runner cancellation before starting either wait, joins the runner and
// metrics drains, and closes tracing and the DB only after those drains have
// completed or the shared deadline has expired.
func finishShutdown(
	ctx context.Context,
	cancelRunner func(),
	waitRunner func(context.Context) error,
	shutdownMetrics func(context.Context) error,
	shutdownTracing func(context.Context) error,
	closeDB func() error,
) error {
	if ctx == nil {
		ctx = context.Background()
	}
	if cancelRunner != nil {
		cancelRunner()
	}

	shutdownErr := runConcurrent(ctx, waitRunner, shutdownMetrics)
	shutdownErr = errors.Join(shutdownErr, runContextBound(ctx, shutdownTracing))
	if closeDB != nil {
		shutdownErr = errors.Join(shutdownErr, runWithoutContext(ctx, closeDB))
	}
	if ctx.Err() != nil {
		shutdownErr = errors.Join(shutdownErr, ctx.Err())
	}
	return shutdownErr
}

// runConcurrent starts every drain before waiting, so each receives the
// same shutdown context and deadline.
func runConcurrent(ctx context.Context, drains ...func(context.Context) error) error {
	results := make(chan error, len(drains))
	remaining := 0
	for _, drain := range drains {
		if drain == nil {
			continue
		}
		remaining++
		go func(drain func(context.Context) error) {
			results <- drain(ctx)
		}(drain)
	}
	if remaining == 0 {
		return nil
	}

	var drainErr error
	for remaining > 0 {
		select {
		case err := <-results:
			remaining--
			drainErr = errors.Join(drainErr, err)
		case <-ctx.Done():
			return errors.Join(drainErr, ctx.Err())
		}
	}
	return drainErr
}

// runContextBound calls a context-aware shutdown function without allowing
// it to hold up process exit beyond ctx's deadline.
func runContextBound(ctx context.Context, shutdown func(context.Context) error) error {
	if shutdown == nil {
		return nil
	}
	result := make(chan error, 1)
	go func() { result <- shutdown(ctx) }()
	select {
	case err := <-result:
		return err
	case <-ctx.Done():
		return ctx.Err()
	}
}

// runWithoutContext bounds a legacy close operation with the same caller
// deadline. The DB close remains the final resource action.
func runWithoutContext(ctx context.Context, closeResource func() error) error {
	result := make(chan error, 1)
	go func() { result <- closeResource() }()
	select {
	case err := <-result:
		return err
	case <-ctx.Done():
		return ctx.Err()
	}
}

// expectedJobs names the jobs a complete flow-worker runs. A deployment
// missing one is misconfigured, and the up gauge says so.
//
// The list is written out rather than derived because the configuration
// offers nothing honest to derive it from: what decides whether the
// calendar job registers is the presence of NF_FLOW_API_SIGNAL_TOKEN, and
// reading the expectation off that same value would make the gauge agree
// with any configuration at all — an unset token would mean "no calendar
// job wanted" instead of "the calendar job cannot run", which is the
// failure this metric exists to report.
//
// Only conditionally-registered jobs belong here. business_metrics is
// deliberately absent: it registers unconditionally, so its presence is a
// property of the binary rather than of the deployment, and letting it
// satisfy the gauge is what would make a misconfigured worker look
// complete. Adding a job whose registration depends on configuration
// means adding its name here — this is the one place the expectation is
// written down.
//
//nolint:gochecknoglobals // the binary's fixed job expectation, read once at boot.
var expectedJobs = []string{calendar_event_day.JobName}

// ReportRunnerHealth publishes what the boot produced: how many jobs the
// runner was given, and whether this process is therefore up. registered
// is the runner's job names (Runner.Names).
//
// The up gauge flips on once MySQL is reachable, the metrics endpoint is
// bound, and every job in expectedJobs is registered. A worker missing one
// — which is what an unset NF_FLOW_API_SIGNAL_TOKEN produces — reports 0.
// It still boots and stays up, so the binary can be rolled out ahead of
// its configuration, but it does not claim to be working: the failure it
// used to produce was a permanently silent event feed with a green
// process standing in front of it, and the only trace was a tick counter
// that never appeared.
//
// Exported because the chain it closes — configuration decides what is
// registered, registration decides the gauge — runs across this package
// and cmd/worker, and is pinned from there.
func ReportRunnerHealth(logger *slog.Logger, registered []string) {
	obs.JobsRegisteredGauge.Set(float64(len(registered)))

	if len(registered) == 0 {
		obs.UpGauge.Set(0)
		logger.Error("flow-worker: no jobs registered; the process is running but will do nothing")
		return
	}
	if missing := missingJobs(registered, expectedJobs); len(missing) > 0 {
		obs.UpGauge.Set(0)
		logger.Error("flow-worker: a job this configuration calls for is not registered; "+
			"the process is running but part of what it was deployed to do will never happen",
			"missing", missing,
			"registered", registered,
		)
		return
	}
	obs.UpGauge.Set(1)
	logger.Info("flow-worker: jobs registered", "count", len(registered), "jobs", registered)
}

// missingJobs returns the expected names absent from registered.
func missingJobs(registered, expected []string) []string {
	have := make(map[string]struct{}, len(registered))
	for _, name := range registered {
		have[name] = struct{}{}
	}
	var missing []string
	for _, name := range expected {
		if _, ok := have[name]; !ok {
			missing = append(missing, name)
		}
	}
	return missing
}

// openAndPingDB opens the MySQL pool described by cfg and verifies the
// connection with a Ping. Returns a wrapped error on either failure so
// Run can surface it to cmd/worker (which exits non-zero) without
// losing the underlying driver message.
func openAndPingDB(cfg *config.Config, logger *slog.Logger) (*sql.DB, error) {
	// Pin the session timezone before opening: every stored DATETIME is a
	// UTC wall clock, and a session that answers NOW() in the host's zone
	// makes every comparison against them wrong by the offset, silently.
	db, err := sql.Open("mysql", dbtz.NormalizeDSN(cfg.DBDSN))
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
