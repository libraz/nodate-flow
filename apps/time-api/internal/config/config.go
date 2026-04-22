// Package config loads runtime configuration from NF_* environment variables.
package config

import (
	"fmt"
	"strconv"
	"time"

	"github.com/caarlos0/env/v11"
)

// Config holds runtime configuration for the nodate-time API server.
type Config struct {
	Port     string `env:"NF_TIME_PORT" envDefault:"8081"`
	LogLevel string `env:"NF_TIME_LOG_LEVEL" envDefault:"info"`

	// Env names the deployment environment — one of "dev", "test",
	// "staging", "prod". It drives behaviour that should only run
	// outside production (for example, the schema-presence probe that
	// warns developers their MySQL volume is out of date). Defaults to
	// "dev" so local developers get the extra diagnostics without
	// having to opt in; production deploys should set NF_ENV=prod.
	Env string `env:"NF_ENV" envDefault:"dev"`

	// DbDsn is the MySQL DSN. The time-api shares the same database as
	// flow-api; only the env prefix differs.
	DbDsn string `env:"NF_DB_DSN,required"`

	// DbMaxOpenConns is the maximum number of open connections to the database.
	DbMaxOpenConns int `env:"NF_DB_MAX_OPEN_CONNS" envDefault:"32"`
	// DbMaxIdleConns is the maximum number of idle connections in the pool.
	DbMaxIdleConns int `env:"NF_DB_MAX_IDLE_CONNS" envDefault:"8"`
	// DbConnMaxLifetime is the maximum time a connection can be reused.
	DbConnMaxLifetime time.Duration `env:"NF_DB_CONN_MAX_LIFETIME" envDefault:"30m"`

	// CookieSecure toggles the Secure flag on the nd_rt refresh cookie.
	// Defaults to true; local http development should set
	// NF_COOKIE_SECURE=false explicitly.
	CookieSecure bool `env:"NF_COOKIE_SECURE" envDefault:"true"`

	// CorsAllowedOrigins is the comma-separated list of origins allowed
	// to call the API with credentials. Both time-web (:5174) and
	// flow-web (:5173) are first-class consumers of time-api — the
	// latter hits the API from its /calendar route — so both Vite dev
	// origins are allow-listed by default.
	CorsAllowedOrigins []string `env:"NF_TIME_CORS" envSeparator:"," envDefault:"http://localhost:5173,http://127.0.0.1:5173,http://localhost:5174,http://127.0.0.1:5174"`

	// S3-compatible object storage for calendar attachments.
	S3Endpoint  string `env:"NF_S3_ENDPOINT" envDefault:""`
	S3AccessKey string `env:"NF_S3_ACCESS_KEY" envDefault:""`
	S3SecretKey string `env:"NF_S3_SECRET_KEY" envDefault:""`
	S3Bucket    string `env:"NF_S3_BUCKET" envDefault:"nodate"`
	// S3UseSSL enables TLS for the S3 connection. Defaults to true;
	// local MinIO development should set NF_S3_USE_SSL=false.
	S3UseSSL bool `env:"NF_S3_USE_SSL" envDefault:"true"`

	// SmtpHost is the SMTP server hostname used to dispatch
	// transactional emails (event-invite magic links). When empty,
	// email sending is disabled and invite rows are still created but
	// never marked as sent.
	SmtpHost string `env:"NF_TIME_SMTP_HOST" envDefault:""`
	// SmtpPort is the SMTP server port (typically 587 for STARTTLS).
	SmtpPort int `env:"NF_TIME_SMTP_PORT" envDefault:"587"`
	// SmtpUsername is the SASL login. Empty to skip authentication.
	SmtpUsername string `env:"NF_TIME_SMTP_USERNAME" envDefault:""`
	// SmtpPassword is the SASL secret.
	SmtpPassword string `env:"NF_TIME_SMTP_PASSWORD" envDefault:""`
	// SmtpFrom is the envelope sender address used on outbound mail.
	SmtpFrom string `env:"NF_TIME_SMTP_FROM" envDefault:"noreply@nodate-flow.local"`

	// WebBaseURL is the origin of the time-web (calendar) frontend.
	// Used to build /invites/accept magic-link URLs embedded in
	// outbound invite emails. NF_WEB_BASE_URL is preferred; the legacy
	// NF_TIME_WEB_URL also works via envExpand elsewhere. Defaults to
	// http://localhost:5174 for local development.
	WebBaseURL string `env:"NF_WEB_BASE_URL" envDefault:"http://localhost:5174"`
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

// validateEnums checks that enum-like configuration fields contain one of
// the allowed values.
func validateEnums(cfg *Config) error {
	// Port must be a valid integer in the 1-65535 range.
	port, err := strconv.Atoi(cfg.Port)
	if err != nil || port < 1 || port > 65535 {
		return fmt.Errorf("config: NF_TIME_PORT must be an integer between 1 and 65535, got %q", cfg.Port)
	}
	return nil
}
