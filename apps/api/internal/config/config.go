// Package config loads runtime configuration from NF_* environment variables.
package config

import (
	"time"

	"github.com/caarlos0/env/v11"
)

// Config holds runtime configuration for the api server.
type Config struct {
	Port     string `env:"NF_PORT" envDefault:"8080"`
	LogLevel string `env:"NF_LOG_LEVEL" envDefault:"info"`

	// DbDsn is the MySQL DSN used by the api process. Required for the
	// server to boot; handlers that touch the database panic on a nil
	// *sql.DB otherwise.
	DbDsn string `env:"NF_DB_DSN"`

	// GhWebhookSecret is the shared HMAC secret used to verify inbound
	// GitHub webhook deliveries (X-Hub-Signature-256).
	GhWebhookSecret string `env:"NF_GH_WEBHOOK_SECRET" envDefault:""`
	// SlackSigningSecret is the v0 signing secret used to verify inbound
	// Slack event deliveries.
	SlackSigningSecret string `env:"NF_SLACK_SIGNING_SECRET" envDefault:""`
	// GoogleChannelToken is the X-Goog-Channel-Token shared secret that
	// Google Drive push notifications must echo back on every delivery.
	GoogleChannelToken string `env:"NF_GOOGLE_CHANNEL_TOKEN" envDefault:""`
	// DefaultWorkspaceID is the workspace public id (UUID v7) that
	// inbound webhook signals are routed to while there is no per-repo
	// workspace mapping yet.
	//
	// TODO: Replace with a real repo→workspace mapping table.
	DefaultWorkspaceID string `env:"NF_DEFAULT_WORKSPACE_ID" envDefault:""`

	// CookieSecure toggles the Secure flag on the nf_rt refresh cookie
	// and selects the paired SameSite mode (None when secure, Lax
	// otherwise; see auth.refreshCookieSameSite).
	//
	// It defaults to false so the out-of-the-box `make dev` flow on
	// http://localhost works without extra env wiring; every non-local
	// deployment must explicitly set NF_COOKIE_SECURE=true so the
	// cookie survives the cross-site fetch from the web origin to the
	// api origin over https.
	CookieSecure bool `env:"NF_COOKIE_SECURE" envDefault:"false"`

	// AiMock toggles the deterministic in-memory AI provider. When true,
	// every workspace.ai_providers row is ignored and ai.Orchestrator
	// routes to a fixture-backed Provider that loads JSON from
	// apps/api/testdata/ai/. Used by development and tests.
	AiMock bool `env:"NF_AI_MOCK" envDefault:"false"`

	// StreamEnabled toggles the realtime SSE fan-out (ADR 0005). When
	// false the /workspaces/{wsId}/stream route still mounts but uses
	// a [stream.NopNotifier] so eventbus.Append becomes a no-op for
	// subscribers. Defaults to true so dev + prod get realtime out of
	// the box; set NF_STREAM=false to disable (useful for load tests
	// or when running without the SSE-aware web client).
	StreamEnabled bool `env:"NF_STREAM" envDefault:"true"`

	// CorsAllowedOrigins is the comma-separated list of origins allowed to
	// call the API with credentials. Defaults cover the Vite dev server
	// on localhost. Set to a single "*" to allow any origin (credentials
	// will then be disabled by the CORS spec).
	CorsAllowedOrigins []string `env:"NF_CORS_ALLOWED_ORIGINS" envSeparator:"," envDefault:"http://localhost:5173,http://127.0.0.1:5173"`

	// OutboundLlmRps is the steady-state per-provider egress rate cap
	// (requests per second) applied uniformly to every configured LLM
	// destination. 0 disables the limiter (fail-open). The burst size
	// defaults to max(1, rps).
	OutboundLlmRps float64 `env:"NF_OUTBOUND_LLM_RPS" envDefault:"0"`
	// OutboundLlmBurst overrides the burst size for the per-provider
	// egress limiter. 0 → derived from OutboundLlmRps.
	OutboundLlmBurst int `env:"NF_OUTBOUND_LLM_BURST" envDefault:"0"`

	// SessionStore selects the refresh-token session driver.
	//   mysql (default) — wraps sqlc queries against the sessions table.
	//   redis           — requires -tags redis and NF_REDIS_ADDR.
	// Other subsystems (stream notifier, outbound rate limiter) follow
	// the same env-switch pattern once their Redis drivers are wired.
	SessionStore string `env:"NF_SESSION_STORE" envDefault:"mysql"`
	// RedisAddr is the host:port for the Redis client shared by the
	// redis-tagged drivers (sessionstore, stream, outbound).
	RedisAddr string `env:"NF_REDIS_ADDR" envDefault:""`
	// StreamBackend selects the SSE fan-out driver: "memory" (default)
	// or "redis" (requires -tags redis).
	StreamBackend string `env:"NF_STREAM_BACKEND" envDefault:"memory"`
	// OutboundBackend selects the egress rate limiter driver: "memory"
	// (default) or "redis" (requires -tags redis).
	OutboundBackend string `env:"NF_OUTBOUND_BACKEND" envDefault:"memory"`

	// AgentTickInterval is the global period at which the agent
	// scheduler fires interval-scheduled agents. There is no per-agent
	// cron expression — see ai_agents.schedule_kind.
	AgentTickInterval time.Duration `env:"NF_AGENT_TICK_INTERVAL" envDefault:"1m"`

	// AgentQueueBackend selects the agent runtime queue:
	//   memory (default) — scheduler calls Runner inline, single-process only.
	//   mysql           — scheduler enqueues into agent_runs; workers pull.
	// Use mysql when running more than one api replica so schedulers do
	// not double-fire and workers can scale independently.
	AgentQueueBackend string `env:"NF_AGENT_QUEUE_BACKEND" envDefault:"memory"`
	// AgentWorkerCount is the number of in-process workers started
	// alongside the scheduler when AgentQueueBackend=mysql. 0 disables
	// the worker loop entirely, useful for deploying scheduler-only or
	// worker-only replicas.
	AgentWorkerCount int `env:"NF_AGENT_WORKER_COUNT" envDefault:"1"`

	// AgentRunner selects the Runner implementation the scheduler /
	// worker pair dispatches to.
	//   log          (default) — structured-log dispatch, no events.
	//   orchestrator           — writes ai.agent.run.* events and
	//                            delegates the LLM call to an
	//                            AgentExecutor (nil until the ai
	//                            package exposes one).
	AgentRunner string `env:"NF_AGENT_RUNNER" envDefault:"log"`

	// AgentRunsPurgeInterval is how often the purger wakes up to
	// delete completed agent_runs rows. Only active when
	// AgentQueueBackend=mysql. 0 disables the purger.
	AgentRunsPurgeInterval time.Duration `env:"NF_AGENT_RUNS_PURGE_INTERVAL" envDefault:"1h"`
	// AgentRunsRetention is the minimum age a finished agent_runs
	// row must reach before the purger deletes it.
	AgentRunsRetention time.Duration `env:"NF_AGENT_RUNS_RETENTION" envDefault:"168h"`
}

// Load parses NF_* environment variables into a Config.
func Load() (*Config, error) {
	cfg := &Config{}
	if err := env.Parse(cfg); err != nil {
		return nil, err
	}
	return cfg, nil
}
