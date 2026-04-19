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
	// to call the API with credentials.
	CorsAllowedOrigins []string `env:"NF_TIME_CORS" envSeparator:"," envDefault:"http://localhost:5174,http://127.0.0.1:5174"`

	// S3-compatible object storage for calendar attachments.
	S3Endpoint  string `env:"NF_S3_ENDPOINT" envDefault:""`
	S3AccessKey string `env:"NF_S3_ACCESS_KEY" envDefault:""`
	S3SecretKey string `env:"NF_S3_SECRET_KEY" envDefault:""`
	S3Bucket    string `env:"NF_S3_BUCKET" envDefault:"nodate"`
	// S3UseSSL enables TLS for the S3 connection. Defaults to true;
	// local MinIO development should set NF_S3_USE_SSL=false.
	S3UseSSL bool `env:"NF_S3_USE_SSL" envDefault:"true"`
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
