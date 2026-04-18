package e2e

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestWebhookToggleDeactivateAndReactivate verifies that the
// toggle endpoint can deactivate and reactivate a webhook.
func TestWebhookToggleDeactivateAndReactivate(t *testing.T) {
	bootstrap(t)
	t.Parallel()
	tt := newTenant(t)

	wsURL := testServerURL + "/workspaces/" + tt.WorkspacePublicID

	// Create a webhook.
	var created struct {
		Webhook struct {
			ID       string `json:"id"`
			IsActive bool   `json:"isActive"`
		} `json:"webhook"`
	}
	doJSON(t, http.MethodPost, wsURL+"/webhooks", tt.AccessToken,
		map[string]any{
			"url":         "https://example.com/hook-toggle",
			"description": "Toggle test",
			"eventTypes":  json.RawMessage(`["task.created"]`),
		}, &created)
	require.True(t, created.Webhook.IsActive, "new webhook must be active")

	whURL := wsURL + "/webhooks/" + created.Webhook.ID

	// Deactivate via toggle.
	var toggled struct {
		Ok bool `json:"ok"`
	}
	doJSON(t, http.MethodPatch, whURL+"/toggle", tt.AccessToken,
		map[string]any{"isActive": false}, &toggled)
	require.True(t, toggled.Ok)

	// Verify deactivated.
	var detail struct {
		Webhook struct {
			IsActive bool `json:"isActive"`
		} `json:"webhook"`
	}
	doJSON(t, http.MethodGet, whURL, tt.AccessToken, nil, &detail)
	require.False(t, detail.Webhook.IsActive, "webhook must be deactivated")

	// Reactivate via toggle.
	doJSON(t, http.MethodPatch, whURL+"/toggle", tt.AccessToken,
		map[string]any{"isActive": true}, &toggled)
	require.True(t, toggled.Ok)

	// Verify reactivated.
	doJSON(t, http.MethodGet, whURL, tt.AccessToken, nil, &detail)
	require.True(t, detail.Webhook.IsActive, "webhook must be reactivated")
}
