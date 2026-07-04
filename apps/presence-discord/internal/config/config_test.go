package config

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func validConfig() *Config {
	return &Config{
		FlowAPIBaseURL:     "http://flow-api:8080",
		DebounceSeconds:    5,
		MetricsAddr:        ":9094",
		LogLevel:           "info",
		OTelInsecure:       true,
		ShutdownTimeout:    10 * time.Second,
		DiscordBotToken:    "",
		FlowAPISignalToken: "",
	}
}

func TestValidateRejectsNonPositiveDebounceSeconds(t *testing.T) {
	for _, seconds := range []int{0, -1} {
		cfg := validConfig()
		cfg.DebounceSeconds = seconds

		err := cfg.Validate()

		require.Error(t, err)
		require.Contains(t, err.Error(), "NF_PRESENCE_DEBOUNCE_SECONDS must be positive")
	}
}

func TestValidateAcceptsPositiveDebounceSeconds(t *testing.T) {
	cfg := validConfig()
	cfg.DebounceSeconds = 1

	require.NoError(t, cfg.Validate())
}
