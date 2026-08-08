package e2e

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestWebhookCRUD verifies an admin can create, list, get (with secret),
// toggle, and delete webhook subscriptions.
func TestWebhookCRUD(t *testing.T) {
	bootstrap(t)
	t.Parallel()
	tt := newTenant(t) // tenant creator is owner (admin-level)

	base := testServerURL + "/workspaces/" + tt.WorkspacePublicID + "/webhooks"

	// --- Create a webhook ---
	var created struct {
		Webhook struct {
			ID     string `json:"id"`
			URL    string `json:"url"`
			Secret string `json:"secret"`
		} `json:"webhook"`
	}
	doJSON(t, http.MethodPost, base, tt.AccessToken, map[string]any{
		"url":         "https://example.com/hook",
		"description": "CI notifications",
		"eventTypes":  json.RawMessage(`["task.created","task.updated"]`),
	}, &created)
	require.NotEmpty(t, created.Webhook.ID)
	require.Equal(t, "https://example.com/hook", created.Webhook.URL)
	require.NotEmpty(t, created.Webhook.Secret, "secret must be returned on creation")
	webhookID := created.Webhook.ID
	webhookSecret := created.Webhook.Secret

	// --- List webhooks ---
	var list struct {
		Total    int64 `json:"total"`
		Webhooks []struct {
			ID       string `json:"id"`
			IsActive bool   `json:"isActive"`
		} `json:"webhooks"`
	}
	doJSON(t, http.MethodGet, base, tt.AccessToken, nil, &list)
	require.GreaterOrEqual(t, list.Total, int64(1))

	// --- Get returns secret ---
	var got struct {
		Webhook struct {
			ID     string `json:"id"`
			Secret string `json:"secret"`
		} `json:"webhook"`
	}
	doJSON(t, http.MethodGet, base+"/"+webhookID, tt.AccessToken, nil, &got)
	require.Equal(t, webhookSecret, got.Webhook.Secret)

	// --- Toggle deactivate ---
	doJSON(t, http.MethodPatch, base+"/"+webhookID+"/toggle", tt.AccessToken,
		map[string]any{"isActive": false}, nil)

	var afterDeactivate struct {
		Webhook struct {
			IsActive bool `json:"isActive"`
		} `json:"webhook"`
	}
	doJSON(t, http.MethodGet, base+"/"+webhookID, tt.AccessToken, nil, &afterDeactivate)
	require.False(t, afterDeactivate.Webhook.IsActive, "webhook should be inactive after toggle")

	// --- Toggle reactivate ---
	doJSON(t, http.MethodPatch, base+"/"+webhookID+"/toggle", tt.AccessToken,
		map[string]any{"isActive": true}, nil)

	var afterReactivate struct {
		Webhook struct {
			IsActive bool `json:"isActive"`
		} `json:"webhook"`
	}
	doJSON(t, http.MethodGet, base+"/"+webhookID, tt.AccessToken, nil, &afterReactivate)
	require.True(t, afterReactivate.Webhook.IsActive, "webhook should be active again")

	// --- Delivery log starts empty ---
	var deliveries struct {
		Total      int64 `json:"total"`
		Deliveries []struct {
			ID string `json:"id"`
		} `json:"deliveries"`
	}
	doJSON(t, http.MethodGet, base+"/"+webhookID+"/deliveries",
		tt.AccessToken, nil, &deliveries)
	require.Equal(t, int64(0), deliveries.Total)

	// --- Delete webhook ---
	status, _ := doJSONStatus(t, http.MethodDelete, base+"/"+webhookID,
		tt.AccessToken, nil)
	require.Equal(t, http.StatusOK, status)

	// Deleted webhook gone from list.
	var listAfter struct {
		Total    int64 `json:"total"`
		Webhooks []struct {
			ID string `json:"id"`
		} `json:"webhooks"`
	}
	doJSON(t, http.MethodGet, base, tt.AccessToken, nil, &listAfter)
	for _, w := range listAfter.Webhooks {
		require.NotEqual(t, webhookID, w.ID, "deleted webhook must not appear")
	}
}

// TestWebhookRequiresAdmin verifies that a non-admin member cannot
// manage webhook subscriptions.
func TestWebhookRequiresAdmin(t *testing.T) {
	bootstrap(t)
	t.Parallel()
	tt := newTenant(t)

	// Add a second user as a regular member.
	memberTT := newTenant(t)
	// Invite the member to the workspace.
	var invite struct {
		Token string `json:"token"`
	}
	doJSON(t, http.MethodPost,
		testServerURL+"/workspaces/"+tt.WorkspacePublicID+"/invites",
		tt.AccessToken, map[string]any{"role": "member"}, &invite)

	doJSON(t, http.MethodPost,
		testServerURL+"/invites/"+invite.Token+"/accept",
		memberTT.AccessToken, nil, nil)

	base := testServerURL + "/workspaces/" + tt.WorkspacePublicID + "/webhooks"

	// Member cannot create a webhook: they are inside the workspace, so
	// the role gate refuses rather than conceals.
	status, body := doJSONStatus(t, http.MethodPost, base, memberTT.AccessToken,
		map[string]any{
			"url":         "https://example.com/hook",
			"description": "Should fail",
			"eventTypes":  json.RawMessage(`["task.created"]`),
		})
	requireDenied(t, status, body, http.StatusForbidden, "WS.MEMBER.ROLE_DENIED",
		"a non-admin creating a webhook")
}
