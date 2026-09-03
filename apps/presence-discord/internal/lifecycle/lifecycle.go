// Package lifecycle wires the presence-discord process boot sequence so
// the main binary (cmd/gateway) and the lifecycle tests (tests/) can
// share a single source of truth.
//
// Boot order mirrors apps/flow-worker/internal/lifecycle:
//
//	config → logger → tracer → metrics server → gateway →
//	(block on ctx cancel or fatal subsystem error) → graceful shutdown
//
// presence-discord has no MySQL dependency: every lookup it needs
// (user_integrations.metadata_json.external_user_id resolution, signal
// emission) goes through flow-api over HTTP. That keeps the binary's
// blast radius small and lets the gateway be deployed without a DB
// credential.
//
// All failures return wrapped errors; the package never calls os.Exit
// so tests can assert on the error path without forking a subprocess.
package lifecycle

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"runtime/debug"
	"strings"
	"time"

	"github.com/libraz/nodate-flow/apps/presence-discord/internal/config"
	"github.com/libraz/nodate-flow/apps/presence-discord/internal/gateway"
	"github.com/libraz/nodate-flow/apps/presence-discord/internal/obs"
	"github.com/libraz/nodate-flow/packages/go-shared/logutil"
)

// ServiceName is reported as the slog "service" field and the OTel
// service.name attribute. Exported so cmd/gateway (and any future
// admin tool) sees the same constant.
const ServiceName = "presence-discord"

// gatewayRunner is the minimum surface lifecycle.Run needs from the
// gateway package. Tests inject a fake to drive the boot path without
// opening a real Discord WS.
type gatewayRunner interface {
	Start(ctx context.Context) error
	Stop(ctx context.Context) error
}

// RunOptions tunes Run for tests and production. Production callers
// (cmd/gateway) pass nil; tests use Gateway to inject a fake and
// MetricsReady to synchronise on listener bind.
type RunOptions struct {
	// Gateway, when non-nil, replaces the production gateway.Gateway
	// constructed from cfg. Tests use this to swap in a fake that does
	// not require a real Discord bot token or network connectivity.
	Gateway gatewayRunner
	// MetricsReady, when non-nil, is closed once the metrics HTTP server
	// goroutine has been launched. Tests use this to avoid racing the
	// goroutine that calls ListenAndServe; production passes nil.
	MetricsReady chan<- struct{}
}

// NewLogger builds a JSON slog.Logger with the redact handler from
// packages/go-shared/logutil and a default service=presence-discord
// attr so all gateway logs are filterable in aggregation. Exposed for
// cmd/gateway and as a convenience for tests that want production-shaped
// logs.
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
// "version" base attribute. Mirrors flow-worker's resolver so the two
// binaries report identical version strings in the same deployment.
func ResolveVersion() string {
	if v := strings.TrimSpace(os.Getenv("NF_FLOW_VERSION")); v != "" {
		return v
	}
	if info, ok := debug.ReadBuildInfo(); ok && info.Main.Version != "" && info.Main.Version != "(devel)" {
		return info.Main.Version
	}
	return "dev"
}

// Run wires the gateway process and blocks until ctx is cancelled or a
// fatal subsystem error (metrics server bind failure, gateway Start
// error) occurs. Returns nil on graceful shutdown, a wrapped error
// otherwise.
//
// Run is the single source of truth for the boot sequence; cmd/gateway
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
	// provider is a no-op, matching flow-api / flow-worker behaviour.
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

	// Internal-only Prometheus listener. presence-discord has no public
	// HTTP surface, so this is the only network port we open.
	metricsMux := http.NewServeMux()
	metricsMux.Handle("/metrics", obs.Handler())
	metricsSrv := &http.Server{
		Addr:              cfg.MetricsAddr,
		Handler:           metricsMux,
		ReadHeaderTimeout: 5 * time.Second,
	}
	metricsErr := make(chan error, 1)
	go func() {
		logger.Info("metrics server listening", "addr", cfg.MetricsAddr)
		if err := metricsSrv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			metricsErr <- err
		}
		close(metricsErr)
	}()

	if opts.MetricsReady != nil {
		close(opts.MetricsReady)
	}

	// Construct the gateway. Tests inject a fake via opts.Gateway;
	// production constructs the real one from cfg.
	gw := opts.Gateway
	if gw == nil {
		gw = gateway.New(cfg, logger)
	}

	gatewayCtx, gatewayCancel := context.WithCancel(context.Background())
	defer gatewayCancel()
	gatewayErr := make(chan error, 1)
	go func() {
		gatewayErr <- gw.Start(gatewayCtx)
	}()
	logger.Info("gateway started")

	// Block on whichever happens first: a fatal metrics-server error, a
	// fatal gateway error, or ctx cancel (driven by SIGINT/SIGTERM in
	// cmd/gateway, by the test in lifecycle_test).
	var runErr error
	select {
	case err := <-metricsErr:
		if err != nil {
			obs.GatewayUp.Set(0)
			runErr = fmt.Errorf("metrics server exited: %w", err)
		}
	case err := <-gatewayErr:
		// gw.Start returned before ctx cancel; treat any non-nil error
		// as fatal and surface it.
		if err != nil {
			runErr = fmt.Errorf("gateway exited: %w", err)
		}
	case <-ctx.Done():
		logger.Info("shutdown signal received")
	}

	// Graceful shutdown. Cancel the gateway's context so its Start loop
	// observes ctx.Done(), invoke Stop for the WS close handshake,
	// then drain the metrics server. Order mirrors flow-worker.
	gatewayCancel()
	stopCtx, stopCancel := context.WithTimeout(context.Background(), cfg.ShutdownTimeout)
	defer stopCancel()
	if err := gw.Stop(stopCtx); err != nil {
		logger.Error("gateway stop failed", "err", err)
	}

	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), cfg.ShutdownTimeout)
	defer shutdownCancel()
	if err := metricsSrv.Shutdown(shutdownCtx); err != nil {
		logger.Error("metrics server shutdown failed", "err", err)
	}

	obs.GatewayUp.Set(0)
	logger.Info("shutdown complete")
	return runErr
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
