// Package config loads runtime configuration from ND_* environment variables.
package config

import "github.com/caarlos0/env/v11"

// Config holds runtime configuration for the nodate-time API server.
type Config struct {
	Port     string `env:"ND_TIME_PORT" envDefault:"8081"`
	LogLevel string `env:"ND_TIME_LOG_LEVEL" envDefault:"info"`

	// DbDsn is the MySQL DSN. The time-api shares the same database as
	// flow-api; only the env prefix differs.
	DbDsn string `env:"ND_DB_DSN"`

	// CookieSecure toggles the Secure flag on the nd_rt refresh cookie.
	CookieSecure bool `env:"ND_COOKIE_SECURE" envDefault:"false"`

	// CorsAllowedOrigins is the comma-separated list of origins allowed
	// to call the API with credentials.
	CorsAllowedOrigins []string `env:"ND_TIME_CORS" envSeparator:"," envDefault:"http://localhost:5174,http://127.0.0.1:5174"`

	// S3-compatible object storage for calendar attachments.
	S3Endpoint  string `env:"ND_S3_ENDPOINT" envDefault:""`
	S3AccessKey string `env:"ND_S3_ACCESS_KEY" envDefault:""`
	S3SecretKey string `env:"ND_S3_SECRET_KEY" envDefault:""`
	S3Bucket    string `env:"ND_S3_BUCKET" envDefault:"nodate"`
	S3UseSSL    bool   `env:"ND_S3_USE_SSL" envDefault:"false"`
}

// Load parses ND_* environment variables into a Config.
func Load() (*Config, error) {
	cfg := &Config{}
	if err := env.Parse(cfg); err != nil {
		return nil, err
	}
	return cfg, nil
}
