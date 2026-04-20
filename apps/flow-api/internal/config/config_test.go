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
