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
