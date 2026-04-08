// Package config loads runtime configuration from NF_* environment variables.
package config

import "github.com/caarlos0/env/v11"

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
}

// Load parses NF_* environment variables into a Config.
func Load() (*Config, error) {
	cfg := &Config{}
	if err := env.Parse(cfg); err != nil {
		return nil, err
	}
	return cfg, nil
}
