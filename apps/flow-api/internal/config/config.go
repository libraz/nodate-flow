// Package config loads runtime configuration from NF_* environment variables.
package config

import (
	"fmt"
	"log/slog"
	"strconv"
	"time"

	"github.com/caarlos0/env/v11"
)

// Config holds runtime configuration for the api server.
type Config struct {
	Port     string `env:"NF_FLOW_PORT" envDefault:"8080"`
	LogLevel string `env:"NF_FLOW_LOG_LEVEL" envDefault:"info"`

	// DbDsn is the MySQL DSN used by the api process. Required for the
	// server to boot; handlers that touch the database panic on a nil
	// *sql.DB otherwise.
	DbDsn string `env:"NF_DB_DSN,required"`

	// GhWebhookSecret is the shared HMAC secret used to verify inbound
	// GitHub webhook deliveries (X-Hub-Signature-256).
	GhWebhookSecret string `env:"NF_FLOW_GH_WEBHOOK_SECRET" envDefault:""`
	// SlackSigningSecret is the v0 signing secret used to verify inbound
	// Slack event deliveries.
	SlackSigningSecret string `env:"NF_FLOW_SLACK_SIGNING_SECRET" envDefault:""`
	// GoogleChannelToken is the X-Goog-Channel-Token shared secret that
	// Google Drive push notifications must echo back on every delivery.
	GoogleChannelToken string `env:"NF_FLOW_GOOGLE_CHANNEL_TOKEN" envDefault:""`
	// WebhooksInsecure disables webhook signature verification. Set to
	// true only for local development and CI where webhook secrets are
	// not available.
	WebhooksInsecure bool `env:"NF_FLOW_WEBHOOKS_INSECURE" envDefault:"false"`
	// DefaultWorkspaceID is the workspace public id (UUID v7) that
	// inbound webhook signals are routed to as a fallback when the
	// repo_workspace_mappings table has no entry for the repository.
	DefaultWorkspaceID string `env:"NF_FLOW_DEFAULT_WORKSPACE_ID" envDefault:""`

	// CookieSecure toggles the Secure flag on the nd_rt refresh cookie.
	// Defaults to true; local http development should set
	// NF_COOKIE_SECURE=false explicitly.
	CookieSecure bool `env:"NF_COOKIE_SECURE" envDefault:"true"`

	// AiMock toggles the deterministic in-memory AI provider. When true,
	// every workspace.ai_providers row is ignored and ai.Orchestrator
	// routes to a fixture-backed Provider that loads JSON from
	// apps/flow-api/testdata/ai/. Used by development and tests.
	AiMock bool `env:"NF_FLOW_AI_MOCK" envDefault:"false"`

	// StreamEnabled toggles the realtime SSE fan-out (ADR 0005). When
	// false the /workspaces/{wsId}/stream route still mounts but uses
	// a [stream.NopNotifier] so eventbus.Append becomes a no-op for
	// subscribers. Defaults to true so dev + prod get realtime out of
	// the box; set NF_FLOW_STREAM=false to disable (useful for load tests
	// or when running without the SSE-aware web client).
	StreamEnabled bool `env:"NF_FLOW_STREAM" envDefault:"true"`

	// CorsAllowedOrigins is the comma-separated list of origins allowed to
	// call the API with credentials. Defaults cover the Vite dev server
	// on localhost. Set to a single "*" to allow any origin (credentials
	// will then be disabled by the CORS spec).
	CorsAllowedOrigins []string `env:"NF_FLOW_CORS" envSeparator:"," envDefault:"http://localhost:5173,http://127.0.0.1:5173"`

	// OutboundLlmRps is the steady-state per-provider egress rate cap
	// (requests per second) applied uniformly to every configured LLM
	// destination. 0 disables the limiter (fail-open). The burst size
	// defaults to max(1, rps).
	OutboundLlmRps float64 `env:"NF_FLOW_OUTBOUND_LLM_RPS" envDefault:"0"`
	// OutboundLlmBurst overrides the burst size for the per-provider
	// egress limiter. 0 → derived from OutboundLlmRps.
	OutboundLlmBurst int `env:"NF_FLOW_OUTBOUND_LLM_BURST" envDefault:"0"`

	// SessionStore selects the refresh-token session driver.
	//   mysql (default) — wraps sqlc queries against the sessions table.
	//   redis           — requires NF_REDIS_ADDR.
	// Other subsystems (stream notifier, outbound rate limiter) follow
	// the same env-switch pattern.
	SessionStore string `env:"NF_FLOW_SESSION_STORE" envDefault:"mysql"`
	// RedisAddr is the host:port for the Redis client shared by the
	// redis-tagged drivers (sessionstore, stream, outbound).
	RedisAddr string `env:"NF_REDIS_ADDR" envDefault:""`
	// StreamBackend selects the SSE fan-out driver: "memory" (default)
	// or "redis" (requires NF_REDIS_ADDR).
	StreamBackend string `env:"NF_FLOW_STREAM_BACKEND" envDefault:"memory"`
	// OutboundBackend selects the egress rate limiter driver: "memory"
	// (default) or "redis" (requires NF_REDIS_ADDR).
	OutboundBackend string `env:"NF_FLOW_OUTBOUND_BACKEND" envDefault:"memory"`

	// AgentTickInterval is the global period at which the agent
	// scheduler fires interval-scheduled agents. There is no per-agent
	// cron expression — see ai_agents.schedule_kind.
	AgentTickInterval time.Duration `env:"NF_FLOW_AGENT_TICK_INTERVAL" envDefault:"1m"`

	// AgentQueueBackend selects the agent runtime queue:
	//   memory (default) — scheduler calls Runner inline, single-process only.
	//   mysql           — scheduler enqueues into agent_runs; workers pull.
	// Use mysql when running more than one api replica so schedulers do
	// not double-fire and workers can scale independently.
	AgentQueueBackend string `env:"NF_FLOW_AGENT_QUEUE_BACKEND" envDefault:"memory"`
	// AgentWorkerCount is the number of in-process workers started
	// alongside the scheduler when AgentQueueBackend=mysql. 0 disables
	// the worker loop entirely, useful for deploying scheduler-only or
	// worker-only replicas.
	AgentWorkerCount int `env:"NF_FLOW_AGENT_WORKER_COUNT" envDefault:"1"`

	// AgentRunner selects the Runner implementation the scheduler /
	// worker pair dispatches to.
	//   orchestrator (default) — writes ai.agent.run.* events and
	//                            delegates the LLM call to the
	//                            AgentExecutor.
	//   log                    — structured-log dispatch, no events.
	//                            Useful for debugging or cost control.
	AgentRunner string `env:"NF_FLOW_AGENT_RUNNER" envDefault:"orchestrator"`

	// AgentRunsPurgeInterval is how often the purger wakes up to
	// delete completed agent_runs rows. Only active when
	// AgentQueueBackend=mysql. 0 disables the purger.
	AgentRunsPurgeInterval time.Duration `env:"NF_FLOW_AGENT_RUNS_PURGE_INTERVAL" envDefault:"1h"`
	// AgentRunsRetention is the minimum age a finished agent_runs
	// row must reach before the purger deletes it.
	AgentRunsRetention time.Duration `env:"NF_FLOW_AGENT_RUNS_RETENTION" envDefault:"168h"`

	// AutoActionInterval controls how often the autonomous auto-action
	// executor evaluates tasks and applies actions (escalate, close
	// stale reviews). Set to 0 to disable. Default: 5m.
	AutoActionInterval time.Duration `env:"NF_FLOW_AUTO_ACTION_INTERVAL" envDefault:"5m"`
	// AutoActionThreshold is the minimum confidence score for an
	// auto-action to be applied without human approval. Default: 0.80.
	AutoActionThreshold float64 `env:"NF_FLOW_AUTO_ACTION_THRESHOLD" envDefault:"0.80"`
	// AutoActionDryRun logs what auto-actions would be applied without
	// actually mutating the database. Useful for tuning thresholds.
	AutoActionDryRun bool `env:"NF_FLOW_AUTO_ACTION_DRY_RUN" envDefault:"false"`

	// ItemReconcilerInterval controls how often the item-consistency
	// reconciler scans tasks and calendar_events for drift. Set to 0
	// to disable. Default: 5m.
	ItemReconcilerInterval time.Duration `env:"NF_FLOW_ITEM_RECONCILER_INTERVAL" envDefault:"5m"`

	// MetricsPort is the port for the internal-only Prometheus metrics
	// HTTP server. Metrics are served on a separate listener so they are
	// never exposed through the public-facing API port.
	MetricsPort string `env:"NF_FLOW_METRICS_PORT" envDefault:"9090"`

	// OtelEndpoint is the OTLP HTTP collector endpoint (e.g.
	// "localhost:4318"). When empty, tracing is disabled and the server
	// registers a no-op TracerProvider.
	OtelEndpoint string `env:"NF_FLOW_OTEL_ENDPOINT" envDefault:""`
	// OtelInsecure disables TLS for the OTLP exporter connection.
	// Useful for local development against a sidecar collector.
	OtelInsecure bool `env:"NF_FLOW_OTEL_INSECURE" envDefault:"true"`

	// SmtpHost is the SMTP server hostname. When empty, email
	// notifications are disabled.
	SmtpHost string `env:"NF_FLOW_SMTP_HOST" envDefault:""`
	// SmtpPort is the SMTP server port (typically 587 for STARTTLS).
	SmtpPort int `env:"NF_FLOW_SMTP_PORT" envDefault:"587"`
	// SmtpUsername is the SASL login. Empty for unauthenticated relays.
	SmtpUsername string `env:"NF_FLOW_SMTP_USERNAME" envDefault:""`
	// SmtpPassword is the SASL secret.
	SmtpPassword string `env:"NF_FLOW_SMTP_PASSWORD" envDefault:""`
	// SmtpFrom is the default envelope sender address.
	SmtpFrom string `env:"NF_FLOW_SMTP_FROM" envDefault:"noreply@nodate-flow.local"`

	// DisableRateLimit turns off all per-IP rate limiters. Intended for
	// local development and E2E test runs where many requests happen
	// from the same loopback address.
	DisableRateLimit bool `env:"NF_FLOW_DISABLE_RATE_LIMIT" envDefault:"false"`

	// RegistrationOpen controls whether new user sign-up is allowed.
	// When false, the POST /auth/register endpoint returns 403.
	RegistrationOpen bool `env:"NF_REGISTRATION_OPEN" envDefault:"true"`

	// EmbedOpenAIKey is the plaintext OpenAI API key used by the
	// task-embedding pipeline (text-embedding-3-small, 768 dims). When
	// empty the embedding system falls back to the deterministic mock
	// provider.
	EmbedOpenAIKey string `env:"NF_FLOW_EMBED_OPENAI_KEY" envDefault:""`
	// EmbedModel overrides the default OpenAI embedding model
	// (text-embedding-3-small). Use text-embedding-3-large for higher
	// fidelity or an Azure/compatible endpoint's model name.
	EmbedModel string `env:"NF_FLOW_EMBED_MODEL" envDefault:""`
	// EmbedBaseURL overrides the OpenAI Embeddings API base URL. Set
	// this to use Azure OpenAI, LiteLLM, or any compatible endpoint.
	EmbedBaseURL string `env:"NF_FLOW_EMBED_BASE_URL" envDefault:""`

	// DbMaxOpenConns is the maximum number of open connections to the database.
	DbMaxOpenConns int `env:"NF_DB_MAX_OPEN_CONNS" envDefault:"32"`
	// DbMaxIdleConns is the maximum number of idle connections in the pool.
	DbMaxIdleConns int `env:"NF_DB_MAX_IDLE_CONNS" envDefault:"8"`
	// DbConnMaxLifetime is the maximum time a connection can be reused.
	DbConnMaxLifetime time.Duration `env:"NF_DB_CONN_MAX_LIFETIME" envDefault:"30m"`

	// S3Endpoint is the host:port of the S3-compatible object store
	// (e.g. "minio:9000" or "s3.amazonaws.com"). When empty, file
	// upload endpoints return INTERNAL.NOT_CONFIGURED.
	S3Endpoint string `env:"NF_S3_ENDPOINT" envDefault:""`
	// S3AccessKey is the access key for the S3-compatible store.
	S3AccessKey string `env:"NF_S3_ACCESS_KEY" envDefault:""`
	// S3SecretKey is the secret key for the S3-compatible store.
	S3SecretKey string `env:"NF_S3_SECRET_KEY" envDefault:""`
	// S3Bucket is the bucket name used for all uploads.
	S3Bucket string `env:"NF_S3_BUCKET" envDefault:"nodate"`
	// S3UseSSL enables TLS for the S3 connection. Defaults to true;
	// local MinIO development should set NF_S3_USE_SSL=false.
	S3UseSSL bool `env:"NF_S3_USE_SSL" envDefault:"true"`
}

// Load parses NF_* environment variables into a Config and validates
// enum-like fields.
func Load() (*Config, error) {
	cfg := &Config{}
	if err := env.Parse(cfg); err != nil {
		return nil, err
	}
	if err := validateEnums(cfg); err != nil {
		return nil, err
	}
	return cfg, nil
}

// validateEnums checks that enum-like configuration fields contain one of
// the allowed values.
func validateEnums(cfg *Config) error {
	// Port must be a valid integer in the 1-65535 range.
	port, err := strconv.Atoi(cfg.Port)
	if err != nil || port < 1 || port > 65535 {
		return fmt.Errorf("config: NF_FLOW_PORT must be an integer between 1 and 65535, got %q", cfg.Port)
	}

	// SessionStore must be "mysql" or "redis".
	switch cfg.SessionStore {
	case "mysql", "redis":
	default:
		return fmt.Errorf("config: NF_FLOW_SESSION_STORE must be \"mysql\" or \"redis\", got %q", cfg.SessionStore)
	}

	// AgentRunner must be "orchestrator" or "log".
	switch cfg.AgentRunner {
	case "orchestrator", "log":
	default:
		return fmt.Errorf("config: NF_FLOW_AGENT_RUNNER must be \"orchestrator\" or \"log\", got %q", cfg.AgentRunner)
	}

	// AgentQueueBackend must be "memory" or "mysql".
	switch cfg.AgentQueueBackend {
	case "memory", "mysql":
	default:
		return fmt.Errorf("config: NF_FLOW_AGENT_QUEUE_BACKEND must be \"memory\" or \"mysql\", got %q", cfg.AgentQueueBackend)
	}

	// Webhook signature verification secrets must be non-empty when the
	// corresponding webhook feature is reachable. Accept empty only when
	// NF_FLOW_WEBHOOKS_INSECURE=true (local dev / CI).
	if !cfg.WebhooksInsecure {
		if cfg.GhWebhookSecret == "" {
			return fmt.Errorf("config: NF_FLOW_GH_WEBHOOK_SECRET is required (set NF_FLOW_WEBHOOKS_INSECURE=true to disable)")
		}
		if cfg.SlackSigningSecret == "" {
			return fmt.Errorf("config: NF_FLOW_SLACK_SIGNING_SECRET is required (set NF_FLOW_WEBHOOKS_INSECURE=true to disable)")
		}
		if cfg.GoogleChannelToken == "" {
			return fmt.Errorf("config: NF_FLOW_GOOGLE_CHANNEL_TOKEN is required (set NF_FLOW_WEBHOOKS_INSECURE=true to disable)")
		}
	} else {
		slog.Warn("config: NF_FLOW_WEBHOOKS_INSECURE=true; webhook signature verification is disabled")
	}

	return nil
}
