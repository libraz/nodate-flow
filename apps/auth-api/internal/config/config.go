// Package config loads runtime configuration from NF_* environment variables.
package config

import (
	"fmt"
	"strconv"

	"github.com/caarlos0/env/v11"
)

// Config holds runtime configuration for the auth-api server.
type Config struct {
	Port     string `env:"NF_AUTH_PORT" envDefault:"8082"`
	LogLevel string `env:"NF_AUTH_LOG_LEVEL" envDefault:"info"`

	// DbDsn is the MySQL DSN shared with flow-api and time-api.
	DbDsn string `env:"NF_DB_DSN"`

	// CookieSecure toggles the Secure flag on the nd_rt refresh cookie.
	CookieSecure bool `env:"NF_COOKIE_SECURE" envDefault:"false"`

	// RegistrationOpen controls whether new user sign-up is allowed.
	RegistrationOpen bool `env:"NF_REGISTRATION_OPEN" envDefault:"true"`

	// CorsAllowedOrigins is the comma-separated list of origins allowed to
	// call the auth API with credentials. Must include accounts-web,
	// flow-web, and time-web origins.
	CorsAllowedOrigins []string `env:"NF_AUTH_CORS" envSeparator:"," envDefault:"http://localhost:5173,http://localhost:5174,http://localhost:5175,http://127.0.0.1:5173,http://127.0.0.1:5174,http://127.0.0.1:5175"`

	// SessionStore selects the refresh-token session driver: mysql or redis.
	SessionStore string `env:"NF_AUTH_SESSION_STORE" envDefault:"mysql"`
	// RedisAddr is the host:port for the Redis session store.
	RedisAddr string `env:"NF_REDIS_ADDR" envDefault:""`

	// OIDC Google configuration.
	GoogleClientID     string `env:"NF_AUTH_GOOGLE_CLIENT_ID" envDefault:""`
	GoogleClientSecret string `env:"NF_AUTH_GOOGLE_CLIENT_SECRET" envDefault:""`

	// PublicBaseURL is the externally-visible origin of the auth-api,
	// used to build OIDC callback URLs.
	PublicBaseURL string `env:"NF_AUTH_PUBLIC_URL" envDefault:"http://localhost:8082"`

	// AccountsWebURL is the origin of the accounts-web frontend.
	AccountsWebURL string `env:"NF_AUTH_WEB_URL" envDefault:"http://localhost:5175"`

	// FlowWebURL is the origin of the flow-web frontend, used in
	// workspace invite links.
	FlowWebURL string `env:"NF_AUTH_FLOW_WEB_URL" envDefault:"http://localhost:5173"`

	// OAuth integration provider credentials (personal connections).
	// These are separate from the OIDC login providers above; they
	// power /me/integrations. When missing, the provider card is
	// rendered as "not configured".
	IntGithubClientID     string `env:"NF_AUTH_GITHUB_CLIENT_ID" envDefault:""`
	IntGithubClientSecret string `env:"NF_AUTH_GITHUB_CLIENT_SECRET" envDefault:""`
	IntSlackClientID      string `env:"NF_AUTH_SLACK_CLIENT_ID" envDefault:""`
	IntSlackClientSecret  string `env:"NF_AUTH_SLACK_CLIENT_SECRET" envDefault:""`
	IntGoogleClientID     string `env:"NF_AUTH_GOOGLE_INTEGRATION_CLIENT_ID" envDefault:""`
	IntGoogleClientSecret string `env:"NF_AUTH_GOOGLE_INTEGRATION_CLIENT_SECRET" envDefault:""`

	// SmtpHost is the SMTP server hostname. When empty, email
	// sending is disabled (invite links are returned without delivery).
	SmtpHost string `env:"NF_AUTH_SMTP_HOST" envDefault:""`
	// SmtpPort is the SMTP server port (typically 587 for STARTTLS).
	SmtpPort int `env:"NF_AUTH_SMTP_PORT" envDefault:"587"`
	// SmtpUsername is the SASL login. Empty to skip authentication.
	SmtpUsername string `env:"NF_AUTH_SMTP_USERNAME" envDefault:""`
	// SmtpPassword is the SASL secret.
	SmtpPassword string `env:"NF_AUTH_SMTP_PASSWORD" envDefault:""`
	// SmtpFrom is the envelope sender address.
	SmtpFrom string `env:"NF_AUTH_SMTP_FROM" envDefault:"noreply@nodate-flow.local"`
}

// Load parses NF_* environment variables into a Config and validates
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
		return fmt.Errorf("config: NF_AUTH_PORT must be an integer between 1 and 65535, got %q", cfg.Port)
	}
	switch cfg.SessionStore {
	case "mysql", "redis":
	default:
		return fmt.Errorf("config: NF_AUTH_SESSION_STORE must be \"mysql\" or \"redis\", got %q", cfg.SessionStore)
	}
	return nil
}
