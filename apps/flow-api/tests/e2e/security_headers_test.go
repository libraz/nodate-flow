package e2e

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestSecurityHeadersPresent verifies that all responses include the
// expected security headers to prevent clickjacking, MIME sniffing,
// and enforce transport security.
func TestSecurityHeadersPresent(t *testing.T) {
	bootstrap(t)
	t.Parallel()
	tt := newTenant(t)

	// Check both authenticated and unauthenticated responses.
	endpoints := []struct {
		name   string
		url    string
		bearer string
	}{
		{"authenticated", testServerURL + "/me", tt.AccessToken},
		{"unauthenticated", testServerURL + "/health", ""},
	}

	for _, ep := range endpoints {
		t.Run(ep.name, func(t *testing.T) {
			req, err := http.NewRequest(http.MethodGet, ep.url, nil)
			require.NoError(t, err)
			if ep.bearer != "" {
				req.Header.Set("Authorization", "Bearer "+ep.bearer)
			}
			resp, err := http.DefaultClient.Do(req)
			require.NoError(t, err)
			defer resp.Body.Close()

			// X-Content-Type-Options prevents MIME sniffing.
			require.Equal(t, "nosniff",
				resp.Header.Get("X-Content-Type-Options"),
				"X-Content-Type-Options must be nosniff")

			// X-Frame-Options prevents clickjacking.
			require.Equal(t, "DENY",
				resp.Header.Get("X-Frame-Options"),
				"X-Frame-Options must be DENY")

			// Referrer-Policy limits referrer leakage.
			require.NotEmpty(t, resp.Header.Get("Referrer-Policy"),
				"Referrer-Policy must be set")

			// Content-Security-Policy should be set.
			require.NotEmpty(t, resp.Header.Get("Content-Security-Policy"),
				"Content-Security-Policy must be set")
		})
	}
}

// TestRefreshCookieAttributes verifies that the refresh token cookie
// has HttpOnly set and is scoped to the /auth path.
func TestRefreshCookieAttributes(t *testing.T) {
	bootstrap(t)
	t.Parallel()

	// Register a new user and inspect the Set-Cookie header.
	email := "cookie-test-" + randomHex(4) + "@example.test"
	status, body, resp := doRaw(t, http.MethodPost, testServerURL+"/auth/register", "",
		nil, map[string]any{
			"email":       email,
			"password":    "Test1234!@#",
			"displayName": "Cookie Test",
		})
	require.Equal(t, http.StatusOK, status, "register body=%s", string(body))

	cookie := pickCookie(resp, "nd_rt")
	require.NotNil(t, cookie, "nd_rt cookie must be set on register")

	// HttpOnly prevents JavaScript access (XSS mitigation).
	require.True(t, cookie.HttpOnly, "nd_rt must be HttpOnly")

	// Path must be /auth (not /) to limit cookie scope.
	require.Equal(t, "/auth", cookie.Path,
		"nd_rt must be scoped to /auth path")

	// SameSite must be Lax or None (not Strict, for cross-origin API).
	require.True(t,
		cookie.SameSite == http.SameSiteLaxMode || cookie.SameSite == http.SameSiteNoneMode,
		"nd_rt SameSite must be Lax or None, got %v", cookie.SameSite)
}

// TestNoServerVersionHeader verifies that the server does not expose
// its technology stack via Server or X-Powered-By headers.
func TestNoServerVersionHeader(t *testing.T) {
	bootstrap(t)
	t.Parallel()

	req, err := http.NewRequest(http.MethodGet, testServerURL+"/health", nil)
	require.NoError(t, err)
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()

	// Server header should not reveal implementation details.
	server := resp.Header.Get("Server")
	require.NotContains(t, server, "Go", "Server header must not reveal Go")
	require.NotContains(t, server, "go", "Server header must not reveal go")

	// X-Powered-By must not be present.
	require.Empty(t, resp.Header.Get("X-Powered-By"),
		"X-Powered-By must not be set")
}
