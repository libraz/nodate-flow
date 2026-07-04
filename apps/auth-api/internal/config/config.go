// Package config loads runtime configuration from NF_* environment variables.
package config

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/caarlos0/env/v11"
)

// Config holds runtime configuration for the auth-api server.
type Config struct {
	Port     string `env:"NF_AUTH_PORT" envDefault:"8082"`
	LogLevel string `env:"NF_AUTH_LOG_LEVEL" envDefault:"info"`

	// Env selects the runtime environment. "development"/"dev" enables
	// developer conveniences (ephemeral JWT key fallback, permissive CORS).
	// Any other value (e.g. "production") is treated as production by
	// IsProd(), where Validate enforces hard safety guards.
	Env string `env:"NF_ENV" envDefault:"development"`

	// SecretKey is the master secret (32+ bytes as hex or base64) from
	// which the JWT signing key and TOTP cipher are derived. When empty in
	// development, an ephemeral signing key is generated per boot; Validate
	// rejects an empty value in production so tokens survive restarts.
	SecretKey string `env:"NF_SECRET_KEY" envDefault:""`

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

	// TrustedProxyHops is the fixed number of reverse-proxy hops in
	// front of the API. 0 ignores X-Forwarded-For / X-Real-Ip and uses
	// RemoteAddr, which is the safe default for direct exposure.
	TrustedProxyHops int `env:"NF_TRUSTED_PROXY_HOPS" envDefault:"0"`

	// RegistrationOpen controls whether new user sign-up is allowed.
	RegistrationOpen bool `env:"NF_REGISTRATION_OPEN" envDefault:"true"`

	// OAuthAllowedDomains is an opt-in sign-in allowlist of email
	// domains permitted to authenticate via OAuth/OIDC (e.g.
	// "example.com,corp.example.org"). Entries are normalized to lower
	// case, trimmed, and a leading "@" is stripped during Load. When
	// both this and OAuthAllowedEmails are empty the allowlist is
	// inactive and any verified email may sign in (today's open
	// default); set this only to lock an instance to specific
	// organizations.
	OAuthAllowedDomains []string `env:"NF_OAUTH_ALLOWED_DOMAINS" envSeparator:"," envDefault:""`

	// OAuthAllowedEmails is an opt-in exact-address sign-in allowlist
	// for OAuth/OIDC (e.g. "ceo@example.com,ops@vendor.example"). Entries
	// are normalized to lower case and trimmed during Load. Combined
	// with OAuthAllowedDomains via OR: a sign-in is permitted when the
	// email matches an exact entry here OR its domain is in
	// OAuthAllowedDomains. Empty (with an empty domain list) means no
	// restriction.
	OAuthAllowedEmails []string `env:"NF_OAUTH_ALLOWED_EMAILS" envSeparator:"," envDefault:""`

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

	// CorsDevLocalhost relaxes the CORS check so that any
	// http://localhost:<port> or http://127.0.0.1:<port> origin is
	// accepted on top of the explicit allowlist. Intended for local dev
	// where the Vite dev server may bind to a non-default port. MUST
	// remain false in production.
	CorsDevLocalhost bool `env:"NF_CORS_DEV_LOCALHOST" envDefault:"false"`

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
	MicrosoftOIDCClientID       string   `env:"NF_AUTH_MICROSOFT_OIDC_CLIENT_ID" envDefault:""`
	MicrosoftOIDCClientSecret   string   `env:"NF_AUTH_MICROSOFT_OIDC_CLIENT_SECRET" envDefault:""`
	MicrosoftOIDCAllowedTenants []string `env:"NF_AUTH_MICROSOFT_OIDC_ALLOWED_TENANTS" envSeparator:"," envDefault:""`

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

	// Discord personal connection (Phase 8 presence binding). The
	// redirect URI is read from env so deployments can keep the
	// Discord OAuth app's registered callback in lock-step with the
	// auth-api's public URL without re-deriving it server-side.
	IntDiscordClientID     string `env:"NF_AUTH_DISCORD_CLIENT_ID" envDefault:""`
	IntDiscordClientSecret string `env:"NF_AUTH_DISCORD_CLIENT_SECRET" envDefault:""`
	IntDiscordRedirectURI  string `env:"NF_AUTH_DISCORD_REDIRECT_URI" envDefault:""`

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
	// Normalize the opt-in sign-in allowlists: lower-case, trim, drop
	// blanks, and strip a leading "@" from domain entries so an operator
	// can write either "example.com" or "@example.com".
	cfg.OAuthAllowedDomains = normalizeAllowlist(cfg.OAuthAllowedDomains, true)
	cfg.OAuthAllowedEmails = normalizeAllowlist(cfg.OAuthAllowedEmails, false)
	cfg.MicrosoftOIDCAllowedTenants = normalizeAllowlist(cfg.MicrosoftOIDCAllowedTenants, false)
	if err := validateEnums(cfg); err != nil {
		return nil, err
	}
	return cfg, nil
}

// normalizeAllowlist lower-cases and trims each entry, drops blanks, and
// (when stripAt is true) removes a single leading "@" so domain entries
// accept both "example.com" and "@example.com". It returns nil for an
// empty result so callers can treat "no entries" as nil.
func normalizeAllowlist(in []string, stripAt bool) []string {
	out := make([]string, 0, len(in))
	for _, raw := range in {
		v := strings.ToLower(strings.TrimSpace(raw))
		if stripAt {
			v = strings.TrimPrefix(v, "@")
		}
		if v == "" {
			continue
		}
		out = append(out, v)
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// IsSignInEmailAllowed reports whether the given email may sign in under
// the instance's opt-in OAuth/OIDC allowlist.
//
// When both OAuthAllowedDomains and OAuthAllowedEmails are empty the
// allowlist is inactive and every email is allowed, preserving the
// default open sign-in behavior. Otherwise the email is allowed when its
// lower-cased form matches an entry in OAuthAllowedEmails, OR its domain
// (the part after the final "@") matches an entry in OAuthAllowedDomains.
// A malformed address with no "@" is rejected once any allowlist is set.
func (c *Config) IsSignInEmailAllowed(email string) bool {
	if len(c.OAuthAllowedDomains) == 0 && len(c.OAuthAllowedEmails) == 0 {
		return true
	}
	normalized := strings.ToLower(strings.TrimSpace(email))
	for _, allowed := range c.OAuthAllowedEmails {
		if normalized == allowed {
			return true
		}
	}
	at := strings.LastIndex(normalized, "@")
	if at < 0 || at == len(normalized)-1 {
		return false
	}
	domain := normalized[at+1:]
	for _, allowed := range c.OAuthAllowedDomains {
		if domain == allowed {
			return true
		}
	}
	return false
}

// IsDev reports whether the auth-api is running in a development
// environment. Developer conveniences (ephemeral JWT key, permissive CORS)
// are tolerated only when this is true.
func (c *Config) IsDev() bool {
	return c.Env == "development" || c.Env == "dev"
}

// IsProd reports whether the auth-api is running outside a development
// environment, where Validate enforces hard safety guards.
func (c *Config) IsProd() bool {
	return !c.IsDev()
}

// Validate enforces production safety guards. Development conveniences
// (ephemeral JWT signing key, wildcard CORS, dev-localhost CORS relaxation)
// are tolerated only when IsDev() is true; in production they are hard
// errors so the process fails fast rather than booting insecure. Optional
// integrations (S3, SMTP) are intentionally not required here — those
// features degrade gracefully when unconfigured.
func (c *Config) Validate() error {
	if c.IsDev() {
		return nil
	}

	// Master secret must be set so the JWT signing key is stable across
	// restarts. Without it, NewJWTIssuer would fall back to an ephemeral
	// key and every issued token would die on the next restart.
	if strings.TrimSpace(c.SecretKey) == "" {
		return fmt.Errorf("config: NF_SECRET_KEY must be set in production (an unset secret yields an ephemeral JWT key that does not survive restarts)")
	}

	// CORS with credentials cannot use a wildcard or empty origin.
	if len(c.CorsAllowedOrigins) == 0 {
		return fmt.Errorf("config: NF_AUTH_CORS must list explicit origins in production")
	}
	for _, o := range c.CorsAllowedOrigins {
		if strings.TrimSpace(o) == "" || strings.TrimSpace(o) == "*" {
			return fmt.Errorf("config: NF_AUTH_CORS must list explicit origins (no '*') in production")
		}
	}

	// The dev-localhost CORS relaxation must never be enabled in production.
	if c.CorsDevLocalhost {
		return fmt.Errorf("config: NF_CORS_DEV_LOCALHOST must be false in production")
	}

	return nil
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
