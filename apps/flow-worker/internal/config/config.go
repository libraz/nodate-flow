// Package config loads runtime configuration for the flow-worker binary
// from NF_* environment variables. Mirrors the validation pattern used by
// apps/flow-api/internal/config so the two binaries stay consistent.
package config

import (
	"fmt"
	"strconv"
	"time"

	"github.com/caarlos0/env/v11"
)

// Config holds runtime configuration for the flow-worker binary.
//
// Worker-specific knobs use the NF_FLOW_WORKER_* prefix; shared
// infrastructure (DB DSN, OTel endpoint) uses the plain NF_* prefix so
// the worker can reuse flow-api's deployment env.
type Config struct {
	// DBDSN is the MySQL DSN. Required; the worker exits if unset.
	DBDSN string `env:"NF_DB_DSN,required"`

	// MetricsPort is the port for the internal-only Prometheus /metrics
	// HTTP server. Defaults to 9091 to avoid colliding with flow-api's
	// metrics server on 9090.
	MetricsPort string `env:"NF_FLOW_WORKER_PORT" envDefault:"9091"`

	// LogLevel selects the slog minimum level: debug / info / warn /
	// error. Defaults to info.
	LogLevel string `env:"NF_FLOW_WORKER_LOG_LEVEL" envDefault:"info"`

	// OTelEndpoint is the OTLP HTTP collector endpoint
	// (e.g. "localhost:4318"). When empty, tracing is disabled and the
	// worker registers a no-op TracerProvider.
	OTelEndpoint string `env:"NF_OTEL_EXPORTER_OTLP_ENDPOINT" envDefault:""`

	// OTelInsecure disables TLS for the OTLP exporter connection. Useful
	// for local development against a sidecar collector.
	OTelInsecure bool `env:"NF_OTEL_INSECURE" envDefault:"true"`

	// ServiceToken is the shared secret the worker presents to flow-api
	// when invoking POST /signals or any /internal/* route. Optional at
	// boot — an empty value disables every job that needs to reach
	// flow-api so unauthenticated requests are never issued. The same
	// env var is consumed by flow-api and presence-discord so a single
	// operator-set value wires the whole worker fleet.
	ServiceToken string `env:"NF_FLOW_API_SIGNAL_TOKEN" envDefault:""`

	// FlowAPIBaseURL is the base URL of flow-api the worker calls into.
	// Defaults to the local dev address; container deployments override
	// via env.
	FlowAPIBaseURL string `env:"NF_FLOW_API_BASE_URL" envDefault:"http://localhost:8080"`

	// JobTickInterval is the period between job runner ticks. The W1
	// scaffold uses this for the placeholder tick; W2 jobs reuse it as
	// the default cadence unless they declare their own.
	JobTickInterval time.Duration `env:"NF_FLOW_WORKER_TICK_INTERVAL" envDefault:"60s"`

	// JobShutdownTimeout caps how long the runner waits for an in-flight
	// tick to drain after the shutdown signal arrives. Mirrors flow-api's
	// 20s graceful HTTP drain but uses 30s to account for longer jobs.
	JobShutdownTimeout time.Duration `env:"NF_FLOW_WORKER_SHUTDOWN_TIMEOUT" envDefault:"30s"`

	// DBMaxOpenConns caps the worker's MySQL connection pool. Worker jobs
	// are sequential by default so a small pool is sufficient.
	DBMaxOpenConns int `env:"NF_DB_MAX_OPEN_CONNS" envDefault:"8"`
	// DBMaxIdleConns caps idle connections held in the pool.
	DBMaxIdleConns int `env:"NF_DB_MAX_IDLE_CONNS" envDefault:"4"`
	// DBConnMaxLifetime is the maximum time a connection can be reused.
	DBConnMaxLifetime time.Duration `env:"NF_DB_CONN_MAX_LIFETIME" envDefault:"30m"`
}

// Load parses NF_* environment variables into a Config and validates
// enum-like fields.
func Load() (*Config, error) {
	cfg := &Config{}
	if err := env.Parse(cfg); err != nil {
		return nil, fmt.Errorf("config: parse env: %w", err)
	}
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	return cfg, nil
}

// Validate checks the parsed configuration for invariants that env.Parse
// cannot enforce (port range, log level enum, positive durations).
func (c *Config) Validate() error {
	port, err := strconv.Atoi(c.MetricsPort)
	if err != nil || port < 1 || port > 65535 {
		return fmt.Errorf("config: NF_FLOW_WORKER_PORT must be an integer between 1 and 65535, got %q", c.MetricsPort)
	}

	switch c.LogLevel {
	case "debug", "info", "warn", "warning", "error":
	default:
		return fmt.Errorf("config: NF_FLOW_WORKER_LOG_LEVEL must be one of debug/info/warn/error, got %q", c.LogLevel)
	}

	if c.JobTickInterval <= 0 {
		return fmt.Errorf("config: NF_FLOW_WORKER_TICK_INTERVAL must be positive, got %s", c.JobTickInterval)
	}
	if c.JobShutdownTimeout <= 0 {
		return fmt.Errorf("config: NF_FLOW_WORKER_SHUTDOWN_TIMEOUT must be positive, got %s", c.JobShutdownTimeout)
	}
	if c.DBMaxOpenConns <= 0 {
		return fmt.Errorf("config: NF_DB_MAX_OPEN_CONNS must be positive, got %d", c.DBMaxOpenConns)
	}
	if c.DBMaxIdleConns < 0 {
		return fmt.Errorf("config: NF_DB_MAX_IDLE_CONNS must be non-negative, got %d", c.DBMaxIdleConns)
	}

	return nil
}
