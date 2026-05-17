// Package config loads runtime configuration for the presence-discord
// binary from NF_* environment variables. Mirrors the validation pattern
// used by apps/flow-worker/internal/config so the long-running worker
// processes stay consistent.
//
// presence-discord is the second long-running worker after flow-worker:
// flow-worker hosts cron-style jobs, presence-discord holds a single
// Discord gateway WebSocket connection and translates presence updates
// into signals POSTed back to flow-api. This split follows ADR 0008 D8.
package config

import (
	"fmt"
	"strconv"
	"time"

	"github.com/caarlos0/env/v11"
)

// Config holds runtime configuration for the presence-discord binary.
//
// Presence-specific knobs use the NF_PRESENCE_* prefix; shared
// infrastructure (OTel endpoint, log level) reuses the standard NF_*
// names so the binary can be deployed alongside flow-api / flow-worker
// without bespoke env wiring.
//
// The Discord bot token and the flow-api signal token are intentionally
// NOT marked required at load time: the gateway logs a clear error and
// refuses to Start when they are missing, matching flow-worker's pattern
// of validating tokens at use rather than at boot so the metrics
// endpoint can still come up for operators to scrape.
type Config struct {
	// DiscordBotToken is the Discord bot token used to open the gateway
	// connection. Required at runtime; gateway.Start surfaces a clear
	// error when empty. Never log this field — it grants full bot
	// privileges on every guild the bot is invited to.
	DiscordBotToken string `env:"NF_DISCORD_BOT_TOKEN" envDefault:""`

	// FlowAPIBaseURL is the base URL of flow-api the gateway POSTs
	// signals to. Defaults to the compose-internal DNS name so the
	// binary works out of the box inside docker compose; native dev
	// overrides via env.
	FlowAPIBaseURL string `env:"NF_FLOW_API_BASE_URL" envDefault:"http://flow-api:8080"`

	// FlowAPISignalToken is the shared secret presented to flow-api when
	// emitting signals via POST /signals. Optional at boot, required at
	// emit time; the gateway logs and skips emission until set.
	FlowAPISignalToken string `env:"NF_FLOW_API_SIGNAL_TOKEN" envDefault:""`

	// DebounceSeconds is the per-user debounce window in seconds. Any
	// duplicate presence transition for the same user within this window
	// is collapsed to the latest event, suppressing the event storms
	// Discord emits when users go online / offline rapidly.
	DebounceSeconds int `env:"NF_PRESENCE_DEBOUNCE_SECONDS" envDefault:"5"`

	// MetricsAddr is the bind address for the internal-only Prometheus
	// /metrics HTTP server. Defaults to :9094 to avoid colliding with
	// flow-api (:9090) and flow-worker (:9091); 9092 / 9093 are reserved
	// for future long-running workers.
	MetricsAddr string `env:"NF_PRESENCE_METRICS_ADDR" envDefault:":9094"`

	// LogLevel selects the slog minimum level: debug / info / warn /
	// error. Defaults to info. Mirrors flow-worker's
	// NF_FLOW_WORKER_LOG_LEVEL naming pattern with a presence-scoped
	// prefix.
	LogLevel string `env:"NF_PRESENCE_LOG_LEVEL" envDefault:"info"`

	// OTelEndpoint is the OTLP HTTP collector endpoint
	// (e.g. "localhost:4318"). When empty, tracing is disabled and the
	// gateway registers a no-op TracerProvider.
	OTelEndpoint string `env:"NF_OTEL_EXPORTER_OTLP_ENDPOINT" envDefault:""`

	// OTelInsecure disables TLS for the OTLP exporter connection.
	// Useful for local development against a sidecar collector.
	OTelInsecure bool `env:"NF_OTEL_INSECURE" envDefault:"true"`

	// ShutdownTimeout caps how long the lifecycle waits for the
	// gateway's Stop() to drain before forcing exit. Mirrors flow-api's
	// 20s graceful drain but uses 10s — there is no per-request work to
	// finish, just one WS close handshake.
	ShutdownTimeout time.Duration `env:"NF_PRESENCE_SHUTDOWN_TIMEOUT" envDefault:"10s"`
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
// cannot enforce (port range on MetricsAddr, log level enum, positive
// durations and intervals).
func (c *Config) Validate() error {
	port, err := portFromAddr(c.MetricsAddr)
	if err != nil {
		return fmt.Errorf("config: NF_PRESENCE_METRICS_ADDR invalid: %w", err)
	}
	if port < 1 || port > 65535 {
		return fmt.Errorf("config: NF_PRESENCE_METRICS_ADDR port must be between 1 and 65535, got %d", port)
	}

	switch c.LogLevel {
	case "debug", "info", "warn", "warning", "error":
	default:
		return fmt.Errorf("config: NF_PRESENCE_LOG_LEVEL must be one of debug/info/warn/error, got %q", c.LogLevel)
	}

	if c.DebounceSeconds < 0 {
		return fmt.Errorf("config: NF_PRESENCE_DEBOUNCE_SECONDS must be non-negative, got %d", c.DebounceSeconds)
	}
	if c.ShutdownTimeout <= 0 {
		return fmt.Errorf("config: NF_PRESENCE_SHUTDOWN_TIMEOUT must be positive, got %s", c.ShutdownTimeout)
	}

	return nil
}

// portFromAddr extracts the TCP port from an address of the form
// ":9094", "0.0.0.0:9094" or "127.0.0.1:9094". Bare ":port" and
// "host:port" are both accepted; anything else is rejected so a typo
// like "9094" (missing colon) becomes a fast boot failure.
func portFromAddr(addr string) (int, error) {
	if addr == "" {
		return 0, fmt.Errorf("address is empty")
	}
	// Find the last ":" so IPv6 literals are tolerated.
	colon := -1
	for i := len(addr) - 1; i >= 0; i-- {
		if addr[i] == ':' {
			colon = i
			break
		}
	}
	if colon < 0 {
		return 0, fmt.Errorf("expected host:port, got %q", addr)
	}
	port, err := strconv.Atoi(addr[colon+1:])
	if err != nil {
		return 0, fmt.Errorf("port not numeric: %w", err)
	}
	return port, nil
}
