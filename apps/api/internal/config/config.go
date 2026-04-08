// Package config loads runtime configuration from NF_* environment variables.
package config

import "github.com/caarlos0/env/v11"

// Config holds runtime configuration for the api server.
type Config struct {
	Port     string `env:"NF_PORT" envDefault:"8080"`
	LogLevel string `env:"NF_LOG_LEVEL" envDefault:"info"`

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

	// CookieSecure toggles the Secure flag on the nf_rt refresh cookie.
	// It defaults to true so production deployments over https are safe;
	// local http dev can set NF_COOKIE_SECURE=false to allow the browser
	// to accept the cookie over plaintext.
	CookieSecure bool `env:"NF_COOKIE_SECURE" envDefault:"true"`

	// AiMock toggles the deterministic in-memory AI provider. When true,
	// every workspace.ai_providers row is ignored and ai.Orchestrator
	// routes to a fixture-backed Provider that loads JSON from
	// apps/api/testdata/ai/. Used by development and tests.
	AiMock bool `env:"NF_AI_MOCK" envDefault:"false"`
}

// Load parses NF_* environment variables into a Config.
func Load() (*Config, error) {
	cfg := &Config{}
	if err := env.Parse(cfg); err != nil {
		return nil, err
	}
	return cfg, nil
}
