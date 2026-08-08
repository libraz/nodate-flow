package importer

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestConfigRejectsCredentials covers the reason the validator exists.
// config_json is stored and read back verbatim, so a token that reaches
// the column has already been written down in the clear — the check has
// to happen before the insert, not after.
func TestConfigRejectsCredentials(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name   string
		source string
		cfg    map[string]any
	}{
		{"github pat", "github", map[string]any{"token": "ghp_live"}},
		{"jira api key", "jira", map[string]any{"apiKey": "k"}},
		{"snake case", "linear", map[string]any{"api_key": "k"}},
		{"screaming case", "github", map[string]any{"ACCESS_TOKEN": "k"}},
		{"bare short name", "github", map[string]any{"pat": "k"}},
		{"nested under an allowed key", "csv", map[string]any{
			"csv": "a,b\n1,2",
			"auth": map[string]any{
				"password": "hunter2",
			},
		}},
		{"nested inside a list", "csv", map[string]any{
			"csv":      "a,b\n1,2",
			"settings": []any{map[string]any{"clientSecret": "s"}},
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			err := ValidateConfig(tc.source, tc.cfg)
			require.Error(t, err, "a credential must never reach the plaintext column")
			require.ErrorIs(t, err, ErrConfigKeySecret,
				"the caller has to be able to say \"do not put a token here\" rather than \"unknown key\"")
		})
	}
}

// TestConfigRejectsUnknownKeys covers the sources that have declared
// their keys. A typo on the one key csv reads would otherwise be stored
// and only surface as "no CSV payload" once the worker picked the job
// up.
func TestConfigRejectsUnknownKeys(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name   string
		source string
		cfg    map[string]any
	}{
		{"key from another source", "csv", map[string]any{"csv": "a\n1", "project": "ENG"}},
		{"typo on the one real key", "csv", map[string]any{"csvData": "a\n1"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			err := ValidateConfig(tc.source, tc.cfg)
			require.Error(t, err)
			require.ErrorIs(t, err, ErrConfigKeyUnknown)
			var cerr *ConfigError
			require.True(t, errors.As(err, &cerr), "the offending key has to be nameable")
			require.NotEmpty(t, cerr.Key)
		})
	}
}

// TestConfigAcceptsTheOnlyImplementedShape is the other half: the check
// must not break the one import that works.
func TestConfigAcceptsTheOnlyImplementedShape(t *testing.T) {
	t.Parallel()

	require.NoError(t, ValidateConfig("csv", map[string]any{"csv": "title,priority\nShip it,2"}))
	require.NoError(t, ValidateConfig("csv", nil))
	require.NoError(t, ValidateConfig("github", map[string]any{}))

	// A source with no connector yet has not declared its keys, and a
	// job for one is supposed to reach the worker and fail there with
	// "not implemented" rather than be refused at create.
	require.NoError(t, ValidateConfig("github", map[string]any{"repo": "owner/name"}))
	require.NoError(t, ValidateConfig("jira", map[string]any{"project": "ENG"}))
}

// TestCredentialMatchDoesNotOverreach pins the false-positive side. A
// validator that rejects "path" because it contains "pat" is as broken
// as one that lets a token through, and it fails at the moment a
// connector needs the key.
func TestCredentialMatchDoesNotOverreach(t *testing.T) {
	t.Parallel()

	for _, key := range []string{"path", "keyword", "passthrough", "keys", "authors", "patch"} {
		require.False(t, isCredentialKey(key), "%q does not name a credential", key)
	}
	for _, key := range []string{"token", "apiKey", "api-key", "PAT", "privateKey", "sessionKey"} {
		require.True(t, isCredentialKey(key), "%q names a credential", key)
	}
}
