package config

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestValidateEnumsAcceptsValid(t *testing.T) {
	t.Parallel()
	cfg := &Config{
		Port:              "8080",
		AgentRunner:       "log",
		AgentQueueBackend: "memory",
		AgentTickInterval: time.Minute,
		WebhooksInsecure:  true,
	}
	if err := validateEnums(cfg); err != nil {
		t.Fatalf("valid config rejected: %v", err)
	}
}

func TestValidateEnumsAcceptsOrchestratorAndMysqlQueue(t *testing.T) {
	t.Parallel()
	cfg := &Config{
		Port:              "3000",
		AgentRunner:       "orchestrator",
		AgentQueueBackend: "mysql",
		AgentTickInterval: time.Minute,
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

// TestNoSessionStoreKnob keeps the session-store selector from coming
// back to this service. It never had sessions to store — auth-api owns
// identity and the refresh-token store is chosen there by
// NF_AUTH_SESSION_STORE — so a knob here could only ever validate a
// value nothing read.
func TestNoSessionStoreKnob(t *testing.T) {
	t.Parallel()
	typ := reflect.TypeOf(Config{})
	for i := 0; i < typ.NumField(); i++ {
		f := typ.Field(i)
		if f.Name == "SessionStore" {
			t.Errorf("Config.SessionStore is back; this service holds no sessions")
		}
		if env := f.Tag.Get("env"); strings.Contains(env, "SESSION_STORE") {
			t.Errorf("Config.%s reads %s; the session store is auth-api's to select", f.Name, env)
		}
	}
}

// TestSessionStoreEnvIsGoneFromDeploymentFiles catches the other half of
// the same drift: a documented env var no code reads is exactly the
// wiring gap the knob was removed to close, so the sample env and the
// compose stack must not keep advertising it.
func TestSessionStoreEnvIsGoneFromDeploymentFiles(t *testing.T) {
	t.Parallel()
	// Tests run in the package directory, four levels below the root.
	root, err := filepath.Abs(filepath.Join("..", "..", "..", ".."))
	if err != nil {
		t.Fatalf("resolve repository root: %v", err)
	}
	if _, statErr := os.Stat(filepath.Join(root, "go.work")); statErr != nil {
		t.Fatalf("expected the repository root at %s: %v", root, statErr)
	}
	for _, name := range []string{".env.example", "compose.yml"} {
		raw, readErr := os.ReadFile(filepath.Join(root, name))
		if readErr != nil {
			t.Fatalf("read %s: %v", name, readErr)
		}
		if strings.Contains(string(raw), "NF_FLOW_SESSION_STORE") {
			t.Errorf("%s still sets NF_FLOW_SESSION_STORE, which no code reads", name)
		}
	}
}

func TestValidateEnumsRejectsInvalidAgentRunner(t *testing.T) {
	t.Parallel()
	cfg := &Config{
		Port:              "8080",
		AgentRunner:       "invalid",
		AgentQueueBackend: "memory",
		WebhooksInsecure:  true,
	}
	if err := validateEnums(cfg); err == nil {
		t.Fatal("invalid AgentRunner should be rejected")
	}
}

// A sub-second tick cannot be honoured: the interval also names the
// scheduler's dedupe bucket. It used to be accepted and then divided by
// as whole seconds, so 500ms meant "divide by zero" on the first tick
// that had a queue behind it.
func TestValidateEnumsRejectsSubSecondAgentTickInterval(t *testing.T) {
	t.Parallel()
	for _, interval := range []time.Duration{0, time.Millisecond, 500 * time.Millisecond, 999 * time.Millisecond} {
		cfg := &Config{
			Port:              "8080",
			AgentRunner:       "log",
			AgentQueueBackend: "memory",
			AgentTickInterval: interval,
			WebhooksInsecure:  true,
		}
		err := validateEnums(cfg)
		if err == nil {
			t.Fatalf("NF_FLOW_AGENT_TICK_INTERVAL=%s should be rejected", interval)
		}
		if !strings.Contains(err.Error(), "NF_FLOW_AGENT_TICK_INTERVAL") {
			t.Errorf("error should name the env var, got: %v", err)
		}
	}

	cfg := &Config{
		Port:              "8080",
		AgentRunner:       "log",
		AgentQueueBackend: "memory",
		AgentTickInterval: time.Second,
		WebhooksInsecure:  true,
	}
	if err := validateEnums(cfg); err != nil {
		t.Fatalf("1s is the floor and must be accepted: %v", err)
	}
}

func TestValidateEnumsRejectsInvalidQueueBackend(t *testing.T) {
	t.Parallel()
	cfg := &Config{
		Port:              "8080",
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
		AgentRunner:       "log",
		AgentQueueBackend: "memory",
		AgentTickInterval: time.Minute,
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

func TestLoadReadsEmbeddingKeyFile(t *testing.T) {
	keyFile := filepath.Join(t.TempDir(), "embedding-key")
	if err := os.WriteFile(keyFile, []byte("test-embedding-key\r\n"), 0o600); err != nil {
		t.Fatalf("write test key file: %v", err)
	}

	t.Setenv("NF_DB_DSN", "user:pass@tcp(127.0.0.1:3306)/nodate_flow")
	t.Setenv("NF_FLOW_WEBHOOKS_INSECURE", "true")
	t.Setenv("NF_FLOW_EMBED_OPENAI_KEY", "")
	t.Setenv("NF_FLOW_EMBED_OPENAI_KEY_FILE", keyFile)

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if cfg.EmbedOpenAIKey != "test-embedding-key" {
		t.Fatalf("EmbedOpenAIKey = %q, want file contents without the final line ending", cfg.EmbedOpenAIKey)
	}
}

func TestLoadEmbedOpenAIKeyRejectsUnsafeSources(t *testing.T) {
	keyFile := filepath.Join(t.TempDir(), "embedding-key")
	if err := os.WriteFile(keyFile, []byte("file-test-key\n"), 0o600); err != nil {
		t.Fatalf("write test key file: %v", err)
	}

	t.Run("both sources", func(t *testing.T) {
		_, err := loadEmbedOpenAIKey("env-test-key", keyFile)
		if err == nil {
			t.Fatal("loadEmbedOpenAIKey accepted both key sources")
		}
		for _, secret := range []string{"env-test-key", "file-test-key"} {
			if strings.Contains(err.Error(), secret) {
				t.Errorf("error exposes a key value: %v", err)
			}
		}
	})

	t.Run("unreadable file", func(t *testing.T) {
		_, err := loadEmbedOpenAIKey("", filepath.Join(t.TempDir(), "missing"))
		if err == nil {
			t.Fatal("loadEmbedOpenAIKey accepted a missing key file")
		}
		if strings.Contains(err.Error(), "missing") {
			t.Errorf("error exposes file details: %v", err)
		}
	})

	t.Run("empty file", func(t *testing.T) {
		emptyFile := filepath.Join(t.TempDir(), "empty")
		if err := os.WriteFile(emptyFile, nil, 0o600); err != nil {
			t.Fatalf("write empty key file: %v", err)
		}
		if _, err := loadEmbedOpenAIKey("", emptyFile); err == nil {
			t.Fatal("loadEmbedOpenAIKey accepted an empty key file")
		}
	})

	t.Run("whitespace in file", func(t *testing.T) {
		unsafeFile := filepath.Join(t.TempDir(), "unsafe")
		if err := os.WriteFile(unsafeFile, []byte("first-line\nsecond-line\n"), 0o600); err != nil {
			t.Fatalf("write unsafe key file: %v", err)
		}
		_, err := loadEmbedOpenAIKey("", unsafeFile)
		if err == nil {
			t.Fatal("loadEmbedOpenAIKey accepted a multiline key")
		}
		if strings.Contains(err.Error(), "first-line") || strings.Contains(err.Error(), "second-line") {
			t.Errorf("error exposes file contents: %v", err)
		}
	})

	t.Run("non-regular file", func(t *testing.T) {
		if _, err := loadEmbedOpenAIKey("", t.TempDir()); err == nil {
			t.Fatal("loadEmbedOpenAIKey accepted a directory as a key file")
		}
	})

	t.Run("oversized file", func(t *testing.T) {
		largeFile := filepath.Join(t.TempDir(), "large")
		if err := os.WriteFile(largeFile, []byte(strings.Repeat("k", maxEmbedOpenAIKeyFileBytes+1)), 0o600); err != nil {
			t.Fatalf("write oversized test key file: %v", err)
		}
		if _, err := loadEmbedOpenAIKey("", largeFile); err == nil {
			t.Fatal("loadEmbedOpenAIKey accepted an oversized key file")
		}
	})

	t.Run("whitespace in environment key", func(t *testing.T) {
		_, err := loadEmbedOpenAIKey("first-part second-part", "")
		if err == nil {
			t.Fatal("loadEmbedOpenAIKey accepted whitespace in an environment key")
		}
		if strings.Contains(err.Error(), "first-part") || strings.Contains(err.Error(), "second-part") {
			t.Errorf("error exposes the environment key: %v", err)
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

func TestLoadRejectsInvalidAppName(t *testing.T) {
	t.Setenv("NF_DB_DSN", "user:pass@tcp(127.0.0.1:3306)/nodate_flow")
	t.Setenv("NF_FLOW_WEBHOOKS_INSECURE", "true")

	for name, v := range map[string]string{
		"LF":       "a\nb",
		"CR":       "a\rb",
		"colon":    "a:b",
		"65 runes": strings.Repeat("a", 65),
	} {
		t.Run(name, func(t *testing.T) {
			t.Setenv("NF_APP_NAME", v)
			if _, err := Load(); err == nil {
				t.Fatalf("Load() accepted NF_APP_NAME %q", v)
			}
		})
	}

	t.Run("unset defaults", func(t *testing.T) {
		cfg, err := Load()
		if err != nil {
			t.Fatalf("Load() error = %v", err)
		}
		if cfg.AppName != "nodate-flow" {
			t.Fatalf("AppName = %q, want nodate-flow", cfg.AppName)
		}
	})

	t.Run("custom kept", func(t *testing.T) {
		t.Setenv("NF_APP_NAME", "Acme")
		cfg, err := Load()
		if err != nil {
			t.Fatalf("Load() error = %v", err)
		}
		if cfg.AppName != "Acme" {
			t.Fatalf("AppName = %q, want Acme", cfg.AppName)
		}
	})
}

func TestLoadBrandImages(t *testing.T) {
	t.Setenv("NF_DB_DSN", "user:pass@tcp(127.0.0.1:3306)/nodate_flow")
	t.Setenv("NF_FLOW_WEBHOOKS_INSECURE", "true")

	for _, name := range []string{"NF_APP_LOGO_URL", "NF_APP_ICON_URL"} {
		for label, v := range map[string]string{
			"http":      "http://cdn.example/l.svg",
			"space":     "https://cdn.example/a b.svg",
			"bad data":  "data:image/bmp;base64,AAAA",
			"too long":  "data:image/png;base64," + strings.Repeat("A", 32768),
			"no scheme": "cdn.example/l.svg",
		} {
			t.Run(name+" "+label, func(t *testing.T) {
				t.Setenv(name, v)
				_, err := Load()
				if err == nil || !strings.Contains(err.Error(), name) {
					t.Fatalf("Load() error = %v, want one naming %s", err, name)
				}
			})
		}
	}

	t.Run("trimmed", func(t *testing.T) {
		t.Setenv("NF_APP_LOGO_URL", " https://cdn.example/l.svg ")
		t.Setenv("NF_APP_ICON_URL", "   ")
		cfg, err := Load()
		if err != nil {
			t.Fatalf("Load() error = %v", err)
		}
		if cfg.AppLogoURL != "https://cdn.example/l.svg" || cfg.AppIconURL != "" {
			t.Fatalf("got logo %q icon %q", cfg.AppLogoURL, cfg.AppIconURL)
		}
	})
}
