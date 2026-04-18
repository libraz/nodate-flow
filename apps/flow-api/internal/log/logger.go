// Package log wires the project's structured logger (log/slog) together
// with a redaction handler that scrubs secret-looking values before they
// reach the underlying writer.
package log

import (
	"io"
	"log/slog"
	"os"
	"runtime/debug"
	"strings"
)

// Config controls logger construction. Zero values select sensible
// defaults (info level, JSON format, os.Stdout).
type Config struct {
	// Level is the minimum level emitted. If zero, LevelFromEnv is used.
	Level slog.Level
	// Format is "json" or "text". Empty defaults to "json".
	Format string
	// Writer is the destination. nil defaults to os.Stdout.
	Writer io.Writer
	// Version is reported as the "version" attr. Empty falls back to
	// NF_VERSION env or build info.
	Version string
}

// LevelFromEnv parses ND_FLOW_LOG_LEVEL (debug/info/warn/error), defaulting
// to info.
func LevelFromEnv() slog.Level {
	switch strings.ToLower(strings.TrimSpace(os.Getenv("ND_FLOW_LOG_LEVEL"))) {
	case "debug":
		return slog.LevelDebug
	case "warn", "warning":
		return slog.LevelWarn
	case "error":
		return slog.LevelError
	default:
		return slog.LevelInfo
	}
}

// New builds a *slog.Logger wrapping a RedactHandler over a JSON or text
// handler. Base attrs service=api and version are attached automatically.
func New(cfg Config) *slog.Logger {
	if cfg.Writer == nil {
		cfg.Writer = os.Stdout
	}
	if cfg.Level == 0 {
		cfg.Level = LevelFromEnv()
	}
	opts := &slog.HandlerOptions{Level: cfg.Level}

	var base slog.Handler
	if strings.EqualFold(cfg.Format, "text") {
		base = slog.NewTextHandler(cfg.Writer, opts)
	} else {
		base = slog.NewJSONHandler(cfg.Writer, opts)
	}

	h := NewRedactHandler(base)
	version := cfg.Version
	if version == "" {
		version = resolveVersion()
	}
	return slog.New(h).With(
		slog.String("service", "api"),
		slog.String("version", version),
	)
}

// resolveVersion returns NF_VERSION, the Go build main module version, or
// "dev".
func resolveVersion() string {
	if v := strings.TrimSpace(os.Getenv("ND_VERSION")); v != "" {
		return v
	}
	if info, ok := debug.ReadBuildInfo(); ok && info.Main.Version != "" && info.Main.Version != "(devel)" {
		return info.Main.Version
	}
	return "dev"
}
