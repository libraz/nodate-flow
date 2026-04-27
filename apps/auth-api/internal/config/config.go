// Package config loads runtime configuration from NF_* environment variables.
package config

import (
	"fmt"
	"strconv"
	"time"

	"github.com/caarlos0/env/v11"
)

// Config holds runtime configuration for the auth-api server.
type Config struct {
	Port     string `env:"NF_AUTH_PORT" envDefault:"8082"`
	LogLevel string `env:"NF_AUTH_LOG_LEVEL" envDefault:"info"`

	// DbDsn is the MySQL DSN shared with flow-api.
	DbDsn string `env:"NF_DB_DSN,required"`

	// DbMaxOpenConns is the maximum number of open connections to the database.
	DbMaxOpenConns int `env:"NF_DB_MAX_OPEN_CONNS" envDefault:"16"`
	// DbMaxIdleConns is the maximum number of idle connections in the pool.
	DbMaxIdleConns int `env:"NF_DB_MAX_IDLE_CONNS" envDefault:"4"`
	// DbConnMaxLifetime is the maximum time a connection can be reused.
	DbConnMaxLifetime time.Duration `env:"NF_DB_CONN_MAX_LIFETIME" envDefault:"30m"`

	// CookieSecure toggles the Secure flag on the nd_rt refresh cookie.
	// Defaults to true; local http development should set
	// NF_COOKIE_SECURE=false explicitly.
	CookieSecure bool `env:"NF_COOKIE_SECURE" envDefault:"true"`

	// RegistrationOpen controls whether new user sign-up is allowed.
	RegistrationOpen bool `env:"NF_REGISTRATION_OPEN" envDefault:"true"`

	// MinPasswordLength is the minimum accepted password length for
	// registration and password changes. Defaults to 8.
	MinPasswordLength int `env:"NF_AUTH_MIN_PASSWORD_LENGTH" envDefault:"8"`

	// DisableRateLimit turns off all per-IP rate limiters. Intended for
	// local development and E2E test runs where many registrations happen
	// from the same loopback address.
	DisableRateLimit bool `env:"NF_AUTH_DISABLE_RATE_LIMIT" envDefault:"false"`

	// RateLimitGlobalMax is the global per-IP request cap (all endpoints).
	RateLimitGlobalMax int `env:"NF_AUTH_RATE_LIMIT_GLOBAL_MAX" envDefault:"200"`
	// RateLimitGlobalWindowSec is the global rate-limit window in seconds.
	RateLimitGlobalWindowSec int `env:"NF_AUTH_RATE_LIMIT_GLOBAL_WINDOW" envDefault:"60"`

	// RateLimitAuthMax is the per-IP cap for public auth endpoints
	// (login, register, magic-link). Shared-NAT offices need headroom.
	RateLimitAuthMax int `env:"NF_AUTH_RATE_LIMIT_AUTH_MAX" envDefault:"20"`
	// RateLimitAuthWindowSec is the auth rate-limit window in seconds.
	RateLimitAuthWindowSec int `env:"NF_AUTH_RATE_LIMIT_AUTH_WINDOW" envDefault:"900"`

	// RateLimitSessionMax is the per-IP cap for cookie-auth endpoints
	// (refresh, logout, TOTP).
	RateLimitSessionMax int `env:"NF_AUTH_RATE_LIMIT_SESSION_MAX" envDefault:"60"`
	// RateLimitSessionWindowSec is the session rate-limit window in seconds.
	RateLimitSessionWindowSec int `env:"NF_AUTH_RATE_LIMIT_SESSION_WINDOW" envDefault:"900"`

	// CorsAllowedOrigins is the comma-separated list of origins allowed to
	// call the auth API with credentials. Must include accounts-web
	// (:5175) and flow-web (:5173) origins.
	CorsAllowedOrigins []string `env:"NF_AUTH_CORS" envSeparator:"," envDefault:"http://localhost:5173,http://localhost:5175,http://127.0.0.1:5173,http://127.0.0.1:5175"`

	// SessionStore selects the refresh-token session driver: mysql or redis.
	SessionStore string `env:"NF_AUTH_SESSION_STORE" envDefault:"mysql"`
	// RedisAddr is the host:port for the Redis session store.
	RedisAddr string `env:"NF_REDIS_ADDR" envDefault:""`

	// OIDC Google configuration.
	GoogleClientID     string `env:"NF_AUTH_GOOGLE_CLIENT_ID" envDefault:""`
	GoogleClientSecret string `env:"NF_AUTH_GOOGLE_CLIENT_SECRET" envDefault:""`

	// OIDC GitHub configuration (login via GitHub).
	GithubOIDCClientID     string `env:"NF_AUTH_GITHUB_OIDC_CLIENT_ID" envDefault:""`
	GithubOIDCClientSecret string `env:"NF_AUTH_GITHUB_OIDC_CLIENT_SECRET" envDefault:""`

	// OIDC Microsoft configuration (login via Microsoft).
	MicrosoftOIDCClientID     string `env:"NF_AUTH_MICROSOFT_OIDC_CLIENT_ID" envDefault:""`
	MicrosoftOIDCClientSecret string `env:"NF_AUTH_MICROSOFT_OIDC_CLIENT_SECRET" envDefault:""`

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

	// SMTPHost is the SMTP server hostname. When empty, email
	// sending is disabled (invite links are returned without delivery).
	SMTPHost string `env:"NF_AUTH_SMTP_HOST" envDefault:""`
	// SMTPPort is the SMTP server port (typically 587 for STARTTLS).
	SMTPPort int `env:"NF_AUTH_SMTP_PORT" envDefault:"587"`
	// SMTPUsername is the SASL login. Empty to skip authentication.
	SMTPUsername string `env:"NF_AUTH_SMTP_USERNAME" envDefault:""`
	// SMTPPassword is the SASL secret.
	SMTPPassword string `env:"NF_AUTH_SMTP_PASSWORD" envDefault:""`
	// SMTPFrom is the envelope sender address.
	SMTPFrom string `env:"NF_AUTH_SMTP_FROM" envDefault:"noreply@nodate-flow.local"`

	// S3Endpoint is the host:port of the S3-compatible object store
	// (e.g. "minio:9000" or "s3.amazonaws.com") used for avatar
	// uploads. When empty, avatar upload/proxy endpoints return
	// AUTH.AVATAR.STORAGE_UNAVAILABLE but the rest of the API still
	// boots normally.
	S3Endpoint string `env:"NF_S3_ENDPOINT" envDefault:""`
	// S3AccessKey is the access key for the S3-compatible store.
	S3AccessKey string `env:"NF_S3_ACCESS_KEY" envDefault:""`
	// S3SecretKey is the secret key for the S3-compatible store.
	S3SecretKey string `env:"NF_S3_SECRET_KEY" envDefault:""`
	// S3Bucket is the bucket name used for all uploads. Shared with
	// flow-api so storage keys are resolvable by either service.
	S3Bucket string `env:"NF_S3_BUCKET" envDefault:"nodate"`
	// S3UseSSL enables TLS for the S3 connection. Defaults to false
	// to match local MinIO development; production deployments should
	// set NF_S3_USE_SSL=true.
	S3UseSSL bool `env:"NF_S3_USE_SSL" envDefault:"false"`
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
