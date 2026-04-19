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
	}
	if err := validateEnums(cfg); err == nil {
		t.Fatal("invalid AgentQueueBackend should be rejected")
	}
}
