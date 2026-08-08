package mcp

import (
	stderrors "errors"
	"testing"

	"github.com/stretchr/testify/require"

	apierrors "github.com/libraz/nodate-flow/apps/flow-api/internal/errors"
)

// The rule that keeps credentials out of import_jobs.config_json is
// worth exactly as much as its weakest entry point. REST validates; if
// MCP does not, an agent handed a personal access token and told to
// "import from GitHub" writes it into the plaintext column through the
// surface this product deliberately points agents at.
//
// These cover the seam directly rather than the tool, because the tool
// authenticates against the database before it looks at any argument.
// TestMCPCreateImportJobRejectsCredentials drives the tool end to end
// and proves the seam is actually reached.

func TestParseImportConfigRejectsCredentials(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name   string
		source string
		raw    string
	}{
		{"github pat", "github", `{"token":"ghp_live"}`},
		{"jira api key", "jira", `{"apiKey":"k"}`},
		{"snake case", "linear", `{"api_key":"k"}`},
		{"nested", "csv", `{"csv":"a\n1","auth":{"password":"hunter2"}}`},
		{"inside a list", "csv", `{"csv":"a\n1","settings":[{"clientSecret":"s"}]}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			_, err := parseImportConfig(tc.source, tc.raw)
			require.Error(t, err, "a credential must never reach the plaintext column, whichever transport wrote it")
			requireErrorSpec(t, err, apierrors.WsImportConfigSecretRejected)
		})
	}
}

func TestParseImportConfigRejectsUnknownKeysForDeclaredSources(t *testing.T) {
	t.Parallel()

	_, err := parseImportConfig("csv", `{"csvData":"a\n1"}`)
	require.Error(t, err)
	requireErrorSpec(t, err, apierrors.WsImportConfigKeyUnknown)
}

func TestParseImportConfigAcceptsWhatTheWorkerCanRun(t *testing.T) {
	t.Parallel()

	blob, err := parseImportConfig("csv", `{"csv":"title\nShip it\n"}`)
	require.NoError(t, err)
	require.JSONEq(t, `{"csv":"title\nShip it\n"}`, string(blob))

	// A source with no connector has not declared its keys, so its
	// settings pass through and the job fails honestly in the worker.
	_, err = parseImportConfig("github", `{"repo":"owner/name"}`)
	require.NoError(t, err)

	// Absent and null configurations both become an empty object; the
	// column is NOT NULL and the worker reads it as an object.
	for _, raw := range []string{"", "null"} {
		blob, err := parseImportConfig("csv", raw)
		require.NoError(t, err)
		require.JSONEq(t, `{}`, string(blob))
	}
}

// TestParseImportConfigRequiresAnObject closes the gap json.Valid left:
// a bare array satisfied "is this JSON" and then failed in the worker,
// which reads config_json as an object.
func TestParseImportConfigRequiresAnObject(t *testing.T) {
	t.Parallel()

	for _, raw := range []string{`[1,2]`, `"csv"`, `7`, `{`} {
		_, err := parseImportConfig("csv", raw)
		require.Error(t, err, "config %q is not an object", raw)
		requireErrorSpec(t, err, apierrors.McpToolArgumentsInvalid)
	}
}

func requireErrorSpec(t *testing.T, err error, want *apierrors.Spec) {
	t.Helper()

	require.Error(t, err)
	var ae *apierrors.APIError
	require.Truef(t, stderrors.As(err, &ae), "want *apierrors.APIError, got %T: %v", err, err)
	require.NotNil(t, ae.Spec)
	require.Equalf(t, want.Code, ae.Spec.Code, "want %s, got %s", want.Code, ae.Spec.Code)
}
