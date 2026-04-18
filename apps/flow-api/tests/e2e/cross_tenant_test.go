package e2e

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestCrossTenantPageIsolation verifies that a member of workspace A
// cannot read, update, or delete pages belonging to workspace B.
func TestCrossTenantPageIsolation(t *testing.T) {
	bootstrap(t)
	t.Parallel()

	owner := newTenant(t)
	outsider := newTenant(t)

	// Owner creates a page in their workspace.
	var page struct {
		ID string `json:"id"`
	}
	doJSON(t, http.MethodPost,
		testServerURL+"/workspaces/"+owner.WorkspacePublicID+"/pages",
		owner.AccessToken, map[string]any{"title": "Secret Doc"}, &page)

	base := testServerURL + "/workspaces/" + owner.WorkspacePublicID + "/pages"

	// Outsider cannot list pages.
	status, _ := doJSONStatus(t, http.MethodGet, base, outsider.AccessToken, nil)
	require.GreaterOrEqual(t, status, 403, "outsider must not list pages")

	// Outsider cannot get the page.
	status, _ = doJSONStatus(t, http.MethodGet, base+"/"+page.ID, outsider.AccessToken, nil)
	require.GreaterOrEqual(t, status, 403, "outsider must not get page")

	// Outsider cannot update the page.
	status, _ = doJSONStatus(t, http.MethodPatch, base+"/"+page.ID, outsider.AccessToken,
		map[string]any{"title": "Hacked"})
	require.GreaterOrEqual(t, status, 403, "outsider must not update page")

	// Outsider cannot delete the page.
	status, _ = doJSONStatus(t, http.MethodDelete, base+"/"+page.ID, outsider.AccessToken, nil)
	require.GreaterOrEqual(t, status, 403, "outsider must not delete page")
}

// TestCrossTenantDashboardIsolation verifies that a member of workspace A
// cannot access dashboard widgets in workspace B.
func TestCrossTenantDashboardIsolation(t *testing.T) {
	bootstrap(t)
	t.Parallel()

	owner := newTenant(t)
	outsider := newTenant(t)

	var widget struct {
		ID string `json:"id"`
	}
	doJSON(t, http.MethodPost,
		testServerURL+"/workspaces/"+owner.WorkspacePublicID+"/dashboard/widgets",
		owner.AccessToken, map[string]any{
			"widgetType": "task_summary", "title": "My Widget",
			"positionX": 0, "positionY": 0, "width": 4, "height": 3,
		}, &widget)

	base := testServerURL + "/workspaces/" + owner.WorkspacePublicID + "/dashboard/widgets"

	status, _ := doJSONStatus(t, http.MethodGet, base, outsider.AccessToken, nil)
	require.GreaterOrEqual(t, status, 403)

	status, _ = doJSONStatus(t, http.MethodGet, base+"/"+widget.ID, outsider.AccessToken, nil)
	require.GreaterOrEqual(t, status, 403)
}

// TestCrossTenantLensIsolation verifies that a member of workspace A
// cannot access saved lenses in workspace B.
func TestCrossTenantLensIsolation(t *testing.T) {
	bootstrap(t)
	t.Parallel()

	owner := newTenant(t)
	outsider := newTenant(t)

	var lens struct {
		ID string `json:"id"`
	}
	doJSON(t, http.MethodPost,
		testServerURL+"/workspaces/"+owner.WorkspacePublicID+"/lenses",
		owner.AccessToken, map[string]any{
			"name": "Private View", "filter": json.RawMessage(`{}`),
			"sort": json.RawMessage(`[]`), "isDefault": false,
		}, &lens)

	base := testServerURL + "/workspaces/" + owner.WorkspacePublicID + "/lenses"

	status, _ := doJSONStatus(t, http.MethodGet, base, outsider.AccessToken, nil)
	require.GreaterOrEqual(t, status, 403)

	status, _ = doJSONStatus(t, http.MethodGet, base+"/"+lens.ID, outsider.AccessToken, nil)
	require.GreaterOrEqual(t, status, 403)

	status, _ = doJSONStatus(t, http.MethodDelete, base+"/"+lens.ID, outsider.AccessToken, nil)
	require.GreaterOrEqual(t, status, 403)
}

// TestCrossTenantTimeboxIsolation verifies that a member of workspace A
// cannot access timeboxes in workspace B.
func TestCrossTenantTimeboxIsolation(t *testing.T) {
	bootstrap(t)
	t.Parallel()

	owner := newTenant(t)
	outsider := newTenant(t)

	var tb struct {
		ID string `json:"id"`
	}
	doJSON(t, http.MethodPost,
		testServerURL+"/workspaces/"+owner.WorkspacePublicID+"/timeboxes",
		owner.AccessToken, map[string]any{
			"name": "Sprint X", "startsOn": "2025-06-01", "endsOn": "2025-06-14",
		}, &tb)

	base := testServerURL + "/workspaces/" + owner.WorkspacePublicID + "/timeboxes"

	status, _ := doJSONStatus(t, http.MethodGet, base, outsider.AccessToken, nil)
	require.GreaterOrEqual(t, status, 403)

	status, _ = doJSONStatus(t, http.MethodGet, base+"/"+tb.ID, outsider.AccessToken, nil)
	require.GreaterOrEqual(t, status, 403)

	status, _ = doJSONStatus(t, http.MethodPatch, base+"/"+tb.ID, outsider.AccessToken,
		map[string]any{"name": "Hacked Sprint"})
	require.GreaterOrEqual(t, status, 403)

	status, _ = doJSONStatus(t, http.MethodDelete, base+"/"+tb.ID, outsider.AccessToken, nil)
	require.GreaterOrEqual(t, status, 403)
}

// TestCrossTenantWebhookIsolation verifies that a member of workspace A
// cannot access webhooks in workspace B.
func TestCrossTenantWebhookIsolation(t *testing.T) {
	bootstrap(t)
	t.Parallel()

	owner := newTenant(t)
	outsider := newTenant(t)

	var created struct {
		Webhook struct {
			ID string `json:"id"`
		} `json:"webhook"`
	}
	doJSON(t, http.MethodPost,
		testServerURL+"/workspaces/"+owner.WorkspacePublicID+"/webhooks",
		owner.AccessToken, map[string]any{
			"url": "https://example.com/hook", "description": "test",
			"eventTypes": json.RawMessage(`["*"]`),
		}, &created)

	base := testServerURL + "/workspaces/" + owner.WorkspacePublicID + "/webhooks"

	status, _ := doJSONStatus(t, http.MethodGet, base, outsider.AccessToken, nil)
	require.GreaterOrEqual(t, status, 403)

	status, _ = doJSONStatus(t, http.MethodGet, base+"/"+created.Webhook.ID, outsider.AccessToken, nil)
	require.GreaterOrEqual(t, status, 403)

	status, _ = doJSONStatus(t, http.MethodDelete, base+"/"+created.Webhook.ID, outsider.AccessToken, nil)
	require.GreaterOrEqual(t, status, 403)
}

// TestCrossTenantExportIsolation verifies that a member of workspace A
// cannot export tasks from workspace B.
func TestCrossTenantExportIsolation(t *testing.T) {
	bootstrap(t)
	t.Parallel()

	owner := newTenant(t)
	outsider := newTenant(t)

	status, _ := doJSONStatus(t, http.MethodGet,
		testServerURL+"/workspaces/"+owner.WorkspacePublicID+"/export/tasks?format=json",
		outsider.AccessToken, nil)
	require.GreaterOrEqual(t, status, 403, "outsider must not export tasks")
}
