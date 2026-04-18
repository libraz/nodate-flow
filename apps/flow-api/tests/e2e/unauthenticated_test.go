package e2e

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestUnauthenticatedAccessDenied verifies that all protected endpoints
// reject requests without a valid bearer token.
func TestUnauthenticatedAccessDenied(t *testing.T) {
	bootstrap(t)
	t.Parallel()

	// We need a real workspace ID to form valid URLs, but send no token.
	tt := newTenant(t)
	wsBase := testServerURL + "/workspaces/" + tt.WorkspacePublicID

	endpoints := []struct {
		method string
		path   string
	}{
		// Workspaces
		{http.MethodGet, testServerURL + "/workspaces"},
		{http.MethodGet, wsBase},

		// Projects
		{http.MethodGet, wsBase + "/projects"},

		// Tasks
		{http.MethodGet, testServerURL + "/tasks"},

		// Pages
		{http.MethodGet, wsBase + "/pages"},
		{http.MethodPost, wsBase + "/pages"},

		// Dashboard
		{http.MethodGet, wsBase + "/dashboard/widgets"},
		{http.MethodPost, wsBase + "/dashboard/widgets"},

		// Lenses
		{http.MethodGet, wsBase + "/lenses"},
		{http.MethodPost, wsBase + "/lenses"},

		// Timeboxes
		{http.MethodGet, wsBase + "/timeboxes"},
		{http.MethodPost, wsBase + "/timeboxes"},

		// Webhooks
		{http.MethodGet, wsBase + "/webhooks"},

		// Export
		{http.MethodGet, wsBase + "/export/tasks?format=json"},

		// Invites (management, not public info)
		{http.MethodGet, wsBase + "/invites"},
		{http.MethodPost, wsBase + "/invites"},

		// Members
		{http.MethodGet, wsBase + "/members"},

		// Notifications
		{http.MethodGet, testServerURL + "/me/notifications"},
		{http.MethodGet, testServerURL + "/me/notifications/unread-count"},
	}

	for _, ep := range endpoints {
		t.Run(ep.method+" "+ep.path, func(t *testing.T) {
			status, _ := doJSONStatus(t, ep.method, ep.path, "", nil) // no bearer
			require.Equal(t, http.StatusUnauthorized, status,
				"%s %s should return 401 without auth", ep.method, ep.path)
		})
	}
}
