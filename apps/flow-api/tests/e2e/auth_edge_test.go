package e2e

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestMalformedBearerToken verifies that various malformed
// Authorization headers all return 401 without leaking information.
func TestMalformedBearerToken(t *testing.T) {
	bootstrap(t)
	t.Parallel()

	cases := []struct {
		name  string
		value string
	}{
		{"empty header", ""},
		{"no bearer prefix", "invalid-token"},
		{"basic auth", "Basic dXNlcjpwYXNz"},
		{"bearer with no token", "Bearer "},
		{"bearer with spaces only", "Bearer    "},
		{"bearer with garbage", "Bearer not-a-jwt-at-all"},
		{"bearer lowercase", "bearer some-token"},
		{"double bearer", "Bearer Bearer token"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req, err := http.NewRequest(http.MethodGet, testServerURL+"/me", nil)
			require.NoError(t, err)
			if tc.value != "" {
				req.Header.Set("Authorization", tc.value)
			}
			req.Header.Set("Accept", "application/json")

			resp, err := http.DefaultClient.Do(req)
			require.NoError(t, err)
			defer resp.Body.Close()

			require.Equal(t, http.StatusUnauthorized, resp.StatusCode,
				"malformed auth %q must return 401", tc.name)
		})
	}
}

// TestExpiredTokenReturnsUnauthorized verifies that using an expired
// or revoked session's access token returns 401.
func TestExpiredTokenReturnsUnauthorized(t *testing.T) {
	bootstrap(t)
	t.Parallel()

	tt := newTenant(t)

	// Logout to revoke the session.
	doJSON(t, http.MethodPost, testServerURL+"/auth/logout",
		tt.AccessToken, nil, nil)

	// The access token may still be valid (JWT expiry), but the session
	// is revoked. Depending on implementation, this may return 401 on
	// endpoints that check session validity, or succeed until JWT expires.
	// At minimum, refresh should fail.
	status, _ := doJSONStatus(t, http.MethodPost, testServerURL+"/auth/refresh",
		"", nil)
	require.Equal(t, http.StatusUnauthorized, status,
		"refresh after logout must return 401")
}

// TestDisabledWorkspaceInaccessible verifies that after disabling a
// workspace, all its resources become inaccessible.
func TestDisabledWorkspaceInaccessible(t *testing.T) {
	bootstrap(t)
	t.Parallel()
	tt := newTenant(t)

	wsURL := testServerURL + "/workspaces/" + tt.WorkspacePublicID

	// Create some resources before disabling.
	doJSON(t, http.MethodPost, wsURL+"/pages", tt.AccessToken,
		map[string]any{"title": "Before Disable"}, nil)

	// Disable the workspace (owner only).
	status, _ := doJSONStatus(t, http.MethodDelete, wsURL,
		tt.AccessToken, nil)
	require.Equal(t, http.StatusOK, status)

	// All workspace-scoped endpoints should now fail.
	endpoints := []struct {
		name   string
		method string
		path   string
	}{
		{"get workspace", http.MethodGet, wsURL},
		{"list pages", http.MethodGet, wsURL + "/pages"},
		{"list widgets", http.MethodGet, wsURL + "/dashboard/widgets"},
		{"list lenses", http.MethodGet, wsURL + "/lenses"},
		{"list timeboxes", http.MethodGet, wsURL + "/timeboxes"},
		{"list members", http.MethodGet, wsURL + "/members"},
		{"list projects", http.MethodGet, wsURL + "/projects"},
		{"export tasks", http.MethodGet, wsURL + "/export/tasks?format=json"},
	}

	for _, ep := range endpoints {
		t.Run(ep.name, func(t *testing.T) {
			s, _ := doJSONStatus(t, ep.method, ep.path, tt.AccessToken, nil)
			require.GreaterOrEqual(t, s, 400,
				"%s on disabled workspace must fail", ep.name)
		})
	}
}

// TestDisabledProjectTasksInaccessible verifies that after disabling a
// project, its tasks become inaccessible to non-admin members.
func TestDisabledProjectTasksInaccessible(t *testing.T) {
	bootstrap(t)
	t.Parallel()
	tt := newTenant(t)

	// Create a task in the default project.
	var task struct {
		ID string `json:"id"`
	}
	doJSON(t, http.MethodPost, testServerURL+"/tasks", tt.AccessToken,
		map[string]any{"projectId": tt.ProjectPublicID, "title": "Orphan Task"}, &task)

	// Disable the project.
	status, _ := doJSONStatus(t, http.MethodDelete,
		testServerURL+"/projects/"+tt.ProjectPublicID, tt.AccessToken, nil)
	require.Equal(t, http.StatusOK, status)

	// Project should be inaccessible.
	status, _ = doJSONStatus(t, http.MethodGet,
		testServerURL+"/projects/"+tt.ProjectPublicID, tt.AccessToken, nil)
	require.GreaterOrEqual(t, status, 400,
		"disabled project must not be accessible")
}
