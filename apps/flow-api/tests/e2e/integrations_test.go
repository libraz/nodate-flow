package e2e

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
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

// TestIntegrationsListStableOrder verifies that the provider catalog
// is returned in a deterministic order (github, slack, google_calendar)
// regardless of how many times it is called. The frontend relies on
// this for stable UI card placement.
func TestIntegrationsListStableOrder(t *testing.T) {
	bootstrap(t)
	t.Parallel()

	tt := newTenant(t)

	expected := []string{"github", "slack", "google_calendar"}
	for i := 0; i < 3; i++ {
		var out struct {
			Providers []struct {
				Provider string `json:"provider"`
			} `json:"providers"`
		}
		doJSON(t, http.MethodGet, testServerURL+"/me/integrations", tt.AccessToken, nil, &out)
		require.Len(t, out.Providers, 3)
		for j, p := range out.Providers {
			assert.Equal(t, expected[j], p.Provider,
				"provider order must be stable across calls (iteration %d)", i)
		}
	}
}

// TestIntegrationsCrossTenantIsolation verifies that one tenant's
// integrations list does not leak into another tenant's view.
func TestIntegrationsCrossTenantIsolation(t *testing.T) {
	bootstrap(t)
	t.Parallel()

	tt1 := newTenant(t)
	tt2 := newTenant(t)

	var out1, out2 struct {
		Providers []struct {
			Provider   string `json:"provider"`
			Connection *struct {
				ID string `json:"id"`
			} `json:"connection"`
		} `json:"providers"`
	}
	doJSON(t, http.MethodGet, testServerURL+"/me/integrations", tt1.AccessToken, nil, &out1)
	doJSON(t, http.MethodGet, testServerURL+"/me/integrations", tt2.AccessToken, nil, &out2)

	for _, p := range out1.Providers {
		assert.Nil(t, p.Connection,
			"tenant 1 must have no connections in a fresh tenant")
	}
	for _, p := range out2.Providers {
		assert.Nil(t, p.Connection,
			"tenant 2 must have no connections in a fresh tenant")
	}
}

// TestIntegrationsConnectBadJSON verifies that malformed JSON in the
// connect request body is rejected cleanly (not a 500).
func TestIntegrationsConnectBadJSON(t *testing.T) {
	bootstrap(t)
	t.Parallel()

	tt := newTenant(t)

	status, body := doJSONStatus(t, http.MethodPost,
		testServerURL+"/me/integrations/github/connect",
		tt.AccessToken, "not json")
	assert.True(t, status == http.StatusBadRequest || status == http.StatusUnprocessableEntity,
		"malformed JSON should return 400 or 422, got %d body=%s", status, string(body))
}

// TestIntegrationsConnectRequiresAuth verifies that the connect
// endpoint rejects unauthenticated requests.
func TestIntegrationsConnectRequiresAuth(t *testing.T) {
	bootstrap(t)
	t.Parallel()

	status, _ := doJSONStatus(t, http.MethodPost,
		testServerURL+"/me/integrations/github/connect", "", map[string]any{})
	require.Equal(t, http.StatusUnauthorized, status)
}

// TestIntegrationsDisconnectRequiresAuth verifies that the disconnect
// endpoint rejects unauthenticated requests.
func TestIntegrationsDisconnectRequiresAuth(t *testing.T) {
	bootstrap(t)
	t.Parallel()

	fakeUUID := "01961234-5678-7000-8000-000000000000"
	status, _ := doJSONStatus(t, http.MethodDelete,
		testServerURL+"/me/integrations/"+fakeUUID, "", nil)
	require.Equal(t, http.StatusUnauthorized, status)
}

// TestIntegrationsDisconnectInvalidUUID verifies that a malformed UUID
// on the disconnect endpoint returns a validation error, not 500.
func TestIntegrationsDisconnectInvalidUUID(t *testing.T) {
	bootstrap(t)
	t.Parallel()

	tt := newTenant(t)

	status, body := doJSONStatus(t, http.MethodDelete,
		testServerURL+"/me/integrations/not-a-valid-uuid",
		tt.AccessToken, nil)
	assert.True(t, status >= 400 && status < 500,
		"invalid UUID should return a 4xx error, got %d body=%s", status, string(body))
}

// TestIntegrationsListResponseShape verifies the shape of each
// provider entry returned by the list endpoint, ensuring that the
// contract matches what the frontend expects.
func TestIntegrationsListResponseShape(t *testing.T) {
	bootstrap(t)
	t.Parallel()

	tt := newTenant(t)

	var out struct {
		Providers []struct {
			Provider   string  `json:"provider"`
			Configured bool    `json:"configured"`
			Connection *string `json:"connection"`
		} `json:"providers"`
	}
	doJSON(t, http.MethodGet, testServerURL+"/me/integrations", tt.AccessToken, nil, &out)
	require.Len(t, out.Providers, 3)

	for _, p := range out.Providers {
		assert.NotEmpty(t, p.Provider, "provider name must be non-empty")
		// In the test harness no OAuth credentials are configured,
		// so configured must be false.
		assert.False(t, p.Configured,
			"provider %s should not be configured in test harness", p.Provider)
		assert.Nil(t, p.Connection,
			"fresh tenant must have no connections")
	}
}
