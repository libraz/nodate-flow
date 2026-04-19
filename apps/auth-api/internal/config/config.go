// Package config loads runtime configuration from ND_* environment variables.
package config

import (
	"fmt"
	"strconv"

	"github.com/caarlos0/env/v11"
)

// Config holds runtime configuration for the auth-api server.
type Config struct {
	Port     string `env:"ND_AUTH_PORT" envDefault:"8082"`
	LogLevel string `env:"ND_AUTH_LOG_LEVEL" envDefault:"info"`

	// DbDsn is the MySQL DSN shared with flow-api and time-api.
	DbDsn string `env:"ND_DB_DSN"`

	// CookieSecure toggles the Secure flag on the nd_rt refresh cookie.
	CookieSecure bool `env:"ND_COOKIE_SECURE" envDefault:"false"`

	// RegistrationOpen controls whether new user sign-up is allowed.
	RegistrationOpen bool `env:"ND_REGISTRATION_OPEN" envDefault:"true"`

	// CorsAllowedOrigins is the comma-separated list of origins allowed to
	// call the auth API with credentials. Must include accounts-web,
	// flow-web, and time-web origins.
	CorsAllowedOrigins []string `env:"ND_AUTH_CORS" envSeparator:"," envDefault:"http://localhost:5173,http://localhost:5174,http://localhost:5175,http://127.0.0.1:5173,http://127.0.0.1:5174,http://127.0.0.1:5175"`

	// SessionStore selects the refresh-token session driver: mysql or redis.
	SessionStore string `env:"ND_AUTH_SESSION_STORE" envDefault:"mysql"`
	// RedisAddr is the host:port for the Redis session store.
	RedisAddr string `env:"ND_REDIS_ADDR" envDefault:""`

	// OIDC Google configuration.
	GoogleClientID     string `env:"ND_AUTH_GOOGLE_CLIENT_ID" envDefault:""`
	GoogleClientSecret string `env:"ND_AUTH_GOOGLE_CLIENT_SECRET" envDefault:""`

	// PublicBaseURL is the externally-visible origin of the auth-api,
	// used to build OIDC callback URLs.
	PublicBaseURL string `env:"ND_AUTH_PUBLIC_URL" envDefault:"http://localhost:8082"`

	// AccountsWebURL is the origin of the accounts-web frontend.
	AccountsWebURL string `env:"ND_AUTH_WEB_URL" envDefault:"http://localhost:5175"`
}

// Load parses ND_* environment variables into a Config and validates
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

func validateEnums(cfg *Config) error {
	port, err := strconv.Atoi(cfg.Port)
	if err != nil || port < 1 || port > 65535 {
		return fmt.Errorf("config: ND_AUTH_PORT must be an integer between 1 and 65535, got %q", cfg.Port)
	}
	switch cfg.SessionStore {
	case "mysql", "redis":
	default:
		return fmt.Errorf("config: ND_AUTH_SESSION_STORE must be \"mysql\" or \"redis\", got %q", cfg.SessionStore)
	}
	return nil
}
