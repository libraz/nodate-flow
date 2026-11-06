package config

import (
	"strings"
	"testing"
)

// baseProdConfig returns a config that passes production Validate, so each
// test can flip a single field to assert that guard.
func baseProdConfig() *Config {
	return &Config{
		Env:                "production",
		SecretKey:          "a-sufficiently-long-production-master-secret",
		CorsAllowedOrigins: []string{"https://accounts.example.com"},
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

// Which address the allowlist admits is not decided here. Config carries
// the environment half of the allowlist; the sign-in rule reads it
// together with the database rows, so it is exercised where it lives, in
// the auth handler package.

func TestNormalizeAllowlist(t *testing.T) {
	t.Parallel()
	t.Run("domains strip leading at and lowercase", func(t *testing.T) {
		t.Parallel()
		got := normalizeAllowlist([]string{"  @Example.COM ", "corp.example.org", "  ", ""}, true)
		want := []string{"example.com", "corp.example.org"}
		if len(got) != len(want) {
			t.Fatalf("got %v, want %v", got, want)
		}
		for i := range want {
			if got[i] != want[i] {
				t.Fatalf("got %v, want %v", got, want)
			}
		}
	})
	t.Run("all-blank yields nil", func(t *testing.T) {
		t.Parallel()
		if got := normalizeAllowlist([]string{"", "   ", "@"}, true); got != nil {
			t.Fatalf("expected nil, got %v", got)
		}
	})
	t.Run("emails do not strip at", func(t *testing.T) {
		t.Parallel()
		got := normalizeAllowlist([]string{" VIP@Vendor.Test "}, false)
		if len(got) != 1 || got[0] != "vip@vendor.test" {
			t.Fatalf("got %v", got)
		}
	})
}

// TestValidateEnumsPorts holds that both listening ports are refused
// before the process binds anything.
//
// A port that is not a number, or is outside the range the OS accepts,
// only surfaces at listen time — after the rest of startup has already
// run — and the metrics listener is the easier one to get wrong because
// nothing else in the service depends on it. Each message must name the
// variable it rejected, since that string is all an operator gets.
func TestValidateEnumsPorts(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		port    string
		metrics string
		wantVar string
	}{
		{"both valid", "8082", "9092", ""},
		{"boundary values accepted", "1", "65535", ""},
		{"service port not a number", "http", "9092", "NF_AUTH_PORT"},
		{"service port above range", "65536", "9092", "NF_AUTH_PORT"},
		{"service port zero", "0", "9092", "NF_AUTH_PORT"},
		{"metrics port not a number", "8082", "metrics", "NF_AUTH_METRICS_PORT"},
		{"metrics port above range", "8082", "65536", "NF_AUTH_METRICS_PORT"},
		{"metrics port zero", "8082", "0", "NF_AUTH_METRICS_PORT"},
		{"metrics port empty", "8082", "", "NF_AUTH_METRICS_PORT"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			err := validateEnums(&Config{
				Port:         tt.port,
				MetricsPort:  tt.metrics,
				SessionStore: "mysql",
			})
			if tt.wantVar == "" {
				if err != nil {
					t.Fatalf("validateEnums() error = %v, want nil", err)
				}
				return
			}
			if err == nil {
				t.Fatalf("validateEnums() accepted port=%q metrics=%q, want rejection naming %s", tt.port, tt.metrics, tt.wantVar)
			}
			if !strings.Contains(err.Error(), tt.wantVar) {
				t.Fatalf("validateEnums() error = %q, does not name %s", err, tt.wantVar)
			}
		})
	}
}

// TestValidateEnumsRefusesRedisSessionStore pins the startup refusal: the
// access-token validator on auth-api and flow-api reads the MySQL sessions
// table, so a session created in Redis would make every token fail.
func TestValidateEnumsRefusesRedisSessionStore(t *testing.T) {
	t.Parallel()
	err := validateEnums(&Config{Port: "8082", MetricsPort: "9092", SessionStore: "redis"})
	if err == nil {
		t.Fatal("validateEnums() accepted NF_AUTH_SESSION_STORE=redis, want a startup refusal")
	}
	if !strings.Contains(err.Error(), "NF_AUTH_SESSION_STORE") {
		t.Fatalf("validateEnums() error = %q, does not name NF_AUTH_SESSION_STORE", err)
	}
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

func TestPublicPathPrefix(t *testing.T) {
	t.Parallel()
	tests := []struct {
		raw     string
		want    string
		wantErr bool
	}{
		{"http://localhost:8082", "", false},
		{"http://localhost:8082/", "", false},
		{"https://h/auth-api", "/auth-api", false},
		{"https://h/auth-api/", "/auth-api", false},
		{"https://h/a/b", "/a/b", false},
		{"/auth-api", "", true},
		{"h/auth-api", "", true},
		{"ftp://h/auth-api", "", true},
		{"https://h/auth-api?x=1", "", true},
		{"https://h/auth-api?", "", true},
		{"https://h/auth-api#frag", "", true},
		{"https://h/auth-api#", "", true},
		{"", "", true},
	}
	for _, tt := range tests {
		t.Run(tt.raw, func(t *testing.T) {
			t.Parallel()
			got, err := publicPathPrefix(tt.raw)
			if (err != nil) != tt.wantErr {
				t.Fatalf("publicPathPrefix(%q) error = %v, wantErr = %v", tt.raw, err, tt.wantErr)
			}
			if got != tt.want {
				t.Fatalf("publicPathPrefix(%q) = %q, want %q", tt.raw, got, tt.want)
			}
		})
	}
}

func TestLoadRejectsInvalidAppName(t *testing.T) {
	t.Setenv("NF_DB_DSN", "user:pass@tcp(127.0.0.1:3306)/nodate_flow")

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
