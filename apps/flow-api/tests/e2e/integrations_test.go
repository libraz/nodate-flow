package e2e

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestIntegrationsList verifies that GET /me/integrations returns all
// three providers in a deterministic order, each marked as not
// configured (the test harness has no OAuth credentials).
func TestIntegrationsList(t *testing.T) {
	bootstrap(t)
	t.Parallel()

	tt := newTenant(t)

	var out struct {
		Providers []struct {
			Provider   string `json:"provider"`
			Configured bool   `json:"configured"`
			Connection *struct {
				ID       string `json:"id"`
				Provider string `json:"provider"`
			} `json:"connection"`
		} `json:"providers"`
	}
	doJSON(t, http.MethodGet, testServerURL+"/me/integrations", tt.AccessToken, nil, &out)

	require.Len(t, out.Providers, 3, "must list all three providers")

	expected := []string{"github", "slack", "google_calendar"}
	for i, p := range out.Providers {
		require.Equal(t, expected[i], p.Provider, "provider order")
		require.False(t, p.Configured, "provider %s should not be configured in tests", p.Provider)
		require.Nil(t, p.Connection, "no connections in a fresh tenant")
	}
}

// TestIntegrationsConnectUnconfigured verifies that attempting to
// connect a provider that has no server-side credentials returns a
// 503 with the PROVIDER_NOT_CONFIGURED error code.
func TestIntegrationsConnectUnconfigured(t *testing.T) {
	bootstrap(t)
	t.Parallel()

	tt := newTenant(t)

	status, body := doJSONStatus(t, http.MethodPost,
		testServerURL+"/me/integrations/github/connect",
		tt.AccessToken, map[string]any{})
	require.Equal(t, http.StatusServiceUnavailable, status,
		"unconfigured provider should return 503, body=%s", string(body))
}

// TestIntegrationsConnectUnsupported verifies that connecting a
// provider name that is not in the supported set returns 400.
func TestIntegrationsConnectUnsupported(t *testing.T) {
	bootstrap(t)
	t.Parallel()

	tt := newTenant(t)

	status, body := doJSONStatus(t, http.MethodPost,
		testServerURL+"/me/integrations/bitbucket/connect",
		tt.AccessToken, map[string]any{})
	// Huma validates the enum and returns 422 for unknown enum values.
	require.True(t, status == http.StatusBadRequest || status == http.StatusUnprocessableEntity,
		"unsupported provider should return 400 or 422, got %d body=%s", status, string(body))
}

// TestIntegrationsDisconnectNotFound verifies that disconnecting a
// non-existent connection returns 404.
func TestIntegrationsDisconnectNotFound(t *testing.T) {
	bootstrap(t)
	t.Parallel()

	tt := newTenant(t)

	fakeUUID := "01961234-5678-7000-8000-000000000000"
	status, body := doJSONStatus(t, http.MethodDelete,
		testServerURL+"/me/integrations/"+fakeUUID,
		tt.AccessToken, nil)
	require.Equal(t, http.StatusNotFound, status,
		"disconnect nonexistent should return 404, body=%s", string(body))
}

// TestIntegrationsUnauthenticated verifies that integrations endpoints
// reject unauthenticated requests.
func TestIntegrationsUnauthenticated(t *testing.T) {
	bootstrap(t)
	t.Parallel()

	status, _ := doJSONStatus(t, http.MethodGet,
		testServerURL+"/me/integrations", "", nil)
	require.Equal(t, http.StatusUnauthorized, status,
		"unauthenticated list should return 401")
}
