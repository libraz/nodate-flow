package config

import (
	"strings"
	"testing"
)

func TestValidateEnumsAcceptsValid(t *testing.T) {
	t.Parallel()
	cfg := &Config{
		Port:              "8080",
		SessionStore:      "mysql",
		AgentRunner:       "log",
		AgentQueueBackend: "memory",
		WebhooksInsecure:  true,
	}
	if err := validateEnums(cfg); err != nil {
		t.Fatalf("valid config rejected: %v", err)
	}
}

func TestValidateEnumsAcceptsRedis(t *testing.T) {
	t.Parallel()
	cfg := &Config{
		Port:              "3000",
		SessionStore:      "redis",
		AgentRunner:       "orchestrator",
		AgentQueueBackend: "mysql",
		WebhooksInsecure:  true,
	}
	if err := validateEnums(cfg); err != nil {
		t.Fatalf("valid config rejected: %v", err)
	}
}

func TestValidateEnumsRejectsInvalidPort(t *testing.T) {
	t.Parallel()
	cases := []string{"0", "99999", "abc", "-1", ""}
	for _, port := range cases {
		cfg := &Config{
			Port:              port,
			SessionStore:      "mysql",
			AgentRunner:       "log",
			AgentQueueBackend: "memory",
			WebhooksInsecure:  true,
		}
		err := validateEnums(cfg)
		if err == nil {
			t.Errorf("port %q should be rejected", port)
			continue
		}
		if !strings.Contains(err.Error(), "NF_FLOW_PORT") {
			t.Errorf("port %q error should mention NF_FLOW_PORT: %v", port, err)
		}
	}
}

func TestValidateEnumsRejectsInvalidSessionStore(t *testing.T) {
	t.Parallel()
	cfg := &Config{
		Port:              "8080",
		SessionStore:      "postgres",
		AgentRunner:       "log",
		AgentQueueBackend: "memory",
		WebhooksInsecure:  true,
	}
	if err := validateEnums(cfg); err == nil {
		t.Fatal("invalid SessionStore should be rejected")
	}
}

func TestValidateEnumsRejectsInvalidAgentRunner(t *testing.T) {
	t.Parallel()
	cfg := &Config{
		Port:              "8080",
		SessionStore:      "mysql",
		AgentRunner:       "invalid",
		AgentQueueBackend: "memory",
		WebhooksInsecure:  true,
	}
	if err := validateEnums(cfg); err == nil {
		t.Fatal("invalid AgentRunner should be rejected")
	}
}

func TestValidateEnumsRejectsInvalidQueueBackend(t *testing.T) {
	t.Parallel()
	cfg := &Config{
		Port:              "8080",
		SessionStore:      "mysql",
		AgentRunner:       "log",
		AgentQueueBackend: "redis",
		WebhooksInsecure:  true,
	}
	if err := validateEnums(cfg); err == nil {
		t.Fatal("invalid AgentQueueBackend should be rejected")
	}
}

func TestValidateEnumsRequiresWebhookSecrets(t *testing.T) {
	t.Parallel()
	base := Config{
		Port:              "8080",
		SessionStore:      "mysql",
		AgentRunner:       "log",
		AgentQueueBackend: "memory",
		WebhooksInsecure:  false,
	}

	t.Run("rejects empty GhWebhookSecret", func(t *testing.T) {
		cfg := base
		cfg.GhWebhookSecret = ""
		cfg.SlackSigningSecret = "s"
		cfg.GoogleChannelToken = "g"
		err := validateEnums(&cfg)
		if err == nil {
			t.Fatal("empty GhWebhookSecret should be rejected when WebhooksInsecure=false")
		}
		if !strings.Contains(err.Error(), "NF_FLOW_GH_WEBHOOK_SECRET") {
			t.Errorf("error should mention env var: %v", err)
		}
	})

	t.Run("rejects empty SlackSigningSecret", func(t *testing.T) {
		cfg := base
		cfg.GhWebhookSecret = "h"
		cfg.SlackSigningSecret = ""
		cfg.GoogleChannelToken = "g"
		err := validateEnums(&cfg)
		if err == nil {
			t.Fatal("empty SlackSigningSecret should be rejected when WebhooksInsecure=false")
		}
	})

	t.Run("rejects empty GoogleChannelToken", func(t *testing.T) {
		cfg := base
		cfg.GhWebhookSecret = "h"
		cfg.SlackSigningSecret = "s"
		cfg.GoogleChannelToken = ""
		err := validateEnums(&cfg)
		if err == nil {
			t.Fatal("empty GoogleChannelToken should be rejected when WebhooksInsecure=false")
		}
	})

	t.Run("accepts empty secrets when insecure", func(t *testing.T) {
		cfg := base
		cfg.WebhooksInsecure = true
		if err := validateEnums(&cfg); err != nil {
			t.Fatalf("should accept empty secrets when WebhooksInsecure=true: %v", err)
		}
	})

	t.Run("accepts all secrets set", func(t *testing.T) {
		cfg := base
		cfg.GhWebhookSecret = "h"
		cfg.SlackSigningSecret = "s"
		cfg.GoogleChannelToken = "g"
		if err := validateEnums(&cfg); err != nil {
			t.Fatalf("should accept all secrets set: %v", err)
		}
	})
}

// baseProdConfig returns a config that passes production Validate, so each
// test can flip a single field to assert that guard.
func baseProdConfig() *Config {
	return &Config{
		Env:                "production",
		SecretKey:          "a-sufficiently-long-production-master-secret",
		CorsAllowedOrigins: []string{"https://app.example.com"},
		CorsDevLocalhost:   false,
	}
}

func TestValidateProductionGuards(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		mutate  func(*Config)
		wantErr bool
	}{
		{"valid prod config", func(*Config) {}, false},
		{"empty secret rejected", func(c *Config) { c.SecretKey = "" }, true},
		{"blank secret rejected", func(c *Config) { c.SecretKey = "   " }, true},
		{"wildcard cors rejected", func(c *Config) { c.CorsAllowedOrigins = []string{"*"} }, true},
		{"empty cors rejected", func(c *Config) { c.CorsAllowedOrigins = nil }, true},
		{"blank cors origin rejected", func(c *Config) { c.CorsAllowedOrigins = []string{"  "} }, true},
		{"dev localhost cors rejected", func(c *Config) { c.CorsDevLocalhost = true }, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			c := baseProdConfig()
			tt.mutate(c)
			err := c.Validate()
			if (err != nil) != tt.wantErr {
				t.Fatalf("Validate() error = %v, wantErr = %v", err, tt.wantErr)
			}
		})
	}
}

func TestLoadReadsAiDailyBudgetOverride(t *testing.T) {
	// t.Setenv forbids t.Parallel; webhook verification is disabled so
	// Load's enum/secret guards pass with only the budget override set.
	// NF_DB_DSN is the one required env Load() enforces before it maps the
	// optional budget override; a dummy value is enough since Load only
	// parses env and does not connect.
	t.Setenv("NF_DB_DSN", "user:pass@tcp(127.0.0.1:3306)/nodate_flow")
	t.Setenv("NF_FLOW_WEBHOOKS_INSECURE", "true")

	t.Run("override is parsed", func(t *testing.T) {
		t.Setenv("NF_FLOW_AI_DAILY_BUDGET_CENTS", "2500")
		cfg, err := Load()
		if err != nil {
			t.Fatalf("Load() error = %v", err)
		}
		if cfg.AiDailyBudgetCents != 2500 {
			t.Fatalf("AiDailyBudgetCents = %d, want 2500", cfg.AiDailyBudgetCents)
		}
	})

	t.Run("unset defaults to zero for guard fallback", func(t *testing.T) {
		cfg, err := Load()
		if err != nil {
			t.Fatalf("Load() error = %v", err)
		}
		if cfg.AiDailyBudgetCents != 0 {
			t.Fatalf("AiDailyBudgetCents = %d, want 0 (falls back to default in NewCostGuard)", cfg.AiDailyBudgetCents)
		}
	})
}

func TestValidateDevModeSkipsGuards(t *testing.T) {
	t.Parallel()
	// In development the same insecure settings are tolerated.
	for _, env := range []string{"development", "dev"} {
		c := &Config{
			Env:              env,
			SecretKey:        "",
			CorsDevLocalhost: true,
		}
		if err := c.Validate(); err != nil {
			t.Fatalf("dev config (%s) should pass validation, got %v", env, err)
		}
		if !c.IsDev() || c.IsProd() {
			t.Fatalf("env %q should be dev, not prod", env)
		}
	}
}
