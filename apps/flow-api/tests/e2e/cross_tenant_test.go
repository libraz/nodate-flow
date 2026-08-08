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

	// Every route below is mounted behind the workspace membership gate,
	// which refuses with 403 WS.WORKSPACE.ACCESS_DENIED. The workspace
	// id is in the path and already known to the caller, so there is
	// nothing for a 404 to conceal here — what must not happen is the
	// gate answering 5xx, which the old ">= 403" assertion accepted.
	status, body := doJSONStatus(t, http.MethodGet, base, outsider.AccessToken, nil)
	requireDenied(t, status, body, http.StatusForbidden, "WS.WORKSPACE.ACCESS_DENIED",
		"outsider listing pages")
	require.NotContains(t, string(body), "Secret Doc",
		"a refusal must not carry the page it refused")

	status, body = doJSONStatus(t, http.MethodGet, base+"/"+page.ID, outsider.AccessToken, nil)
	requireDenied(t, status, body, http.StatusForbidden, "WS.WORKSPACE.ACCESS_DENIED",
		"outsider reading a page")
	require.NotContains(t, string(body), "Secret Doc",
		"a refusal must not carry the page it refused")

	status, body = doJSONStatus(t, http.MethodPatch, base+"/"+page.ID, outsider.AccessToken,
		map[string]any{"title": "Hacked"})
	requireDenied(t, status, body, http.StatusForbidden, "WS.WORKSPACE.ACCESS_DENIED",
		"outsider updating a page")

	status, body = doJSONStatus(t, http.MethodDelete, base+"/"+page.ID, outsider.AccessToken, nil)
	requireDenied(t, status, body, http.StatusForbidden, "WS.WORKSPACE.ACCESS_DENIED",
		"outsider deleting a page")

	// The page survived every attempt above.
	var after struct {
		Title string `json:"title"`
	}
	doJSON(t, http.MethodGet, base+"/"+page.ID, owner.AccessToken, nil, &after)
	require.Equal(t, "Secret Doc", after.Title,
		"the page must be untouched after the refused writes")
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

	status, body := doJSONStatus(t, http.MethodGet, base, outsider.AccessToken, nil)
	requireDenied(t, status, body, http.StatusForbidden, "WS.WORKSPACE.ACCESS_DENIED",
		"outsider listing dashboard widgets")

	status, body = doJSONStatus(t, http.MethodGet, base+"/"+widget.ID, outsider.AccessToken, nil)
	requireDenied(t, status, body, http.StatusForbidden, "WS.WORKSPACE.ACCESS_DENIED",
		"outsider reading a dashboard widget")
	require.NotContains(t, string(body), "My Widget",
		"a refusal must not carry the widget it refused")
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

	status, body := doJSONStatus(t, http.MethodGet, base, outsider.AccessToken, nil)
	requireDenied(t, status, body, http.StatusForbidden, "WS.WORKSPACE.ACCESS_DENIED",
		"outsider listing lenses")

	status, body = doJSONStatus(t, http.MethodGet, base+"/"+lens.ID, outsider.AccessToken, nil)
	requireDenied(t, status, body, http.StatusForbidden, "WS.WORKSPACE.ACCESS_DENIED",
		"outsider reading a lens")
	require.NotContains(t, string(body), "Private View",
		"a refusal must not carry the lens it refused")

	status, body = doJSONStatus(t, http.MethodDelete, base+"/"+lens.ID, outsider.AccessToken, nil)
	requireDenied(t, status, body, http.StatusForbidden, "WS.WORKSPACE.ACCESS_DENIED",
		"outsider deleting a lens")

	// The lens survived the refused delete.
	status, _ = doJSONStatus(t, http.MethodGet, base+"/"+lens.ID, owner.AccessToken, nil)
	require.Equal(t, http.StatusOK, status, "the lens must still exist for its owner")
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

	status, body := doJSONStatus(t, http.MethodGet, base, outsider.AccessToken, nil)
	requireDenied(t, status, body, http.StatusForbidden, "WS.WORKSPACE.ACCESS_DENIED",
		"outsider listing timeboxes")

	status, body = doJSONStatus(t, http.MethodGet, base+"/"+tb.ID, outsider.AccessToken, nil)
	requireDenied(t, status, body, http.StatusForbidden, "WS.WORKSPACE.ACCESS_DENIED",
		"outsider reading a timebox")
	require.NotContains(t, string(body), "Sprint X",
		"a refusal must not carry the timebox it refused")

	status, body = doJSONStatus(t, http.MethodPatch, base+"/"+tb.ID, outsider.AccessToken,
		map[string]any{"name": "Hacked Sprint"})
	requireDenied(t, status, body, http.StatusForbidden, "WS.WORKSPACE.ACCESS_DENIED",
		"outsider updating a timebox")

	status, body = doJSONStatus(t, http.MethodDelete, base+"/"+tb.ID, outsider.AccessToken, nil)
	requireDenied(t, status, body, http.StatusForbidden, "WS.WORKSPACE.ACCESS_DENIED",
		"outsider deleting a timebox")

	// The timebox survived every attempt above, name included.
	var after struct {
		Name string `json:"name"`
	}
	doJSON(t, http.MethodGet, base+"/"+tb.ID, owner.AccessToken, nil, &after)
	require.Equal(t, "Sprint X", after.Name,
		"the timebox must be untouched after the refused writes")
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

	status, body := doJSONStatus(t, http.MethodGet, base, outsider.AccessToken, nil)
	requireDenied(t, status, body, http.StatusForbidden, "WS.WORKSPACE.ACCESS_DENIED",
		"outsider listing webhooks")

	status, body = doJSONStatus(t, http.MethodGet, base+"/"+created.Webhook.ID, outsider.AccessToken, nil)
	requireDenied(t, status, body, http.StatusForbidden, "WS.WORKSPACE.ACCESS_DENIED",
		"outsider reading a webhook")
	require.NotContains(t, string(body), "example.com/hook",
		"a refusal must not carry the endpoint it refused")

	status, body = doJSONStatus(t, http.MethodDelete, base+"/"+created.Webhook.ID, outsider.AccessToken, nil)
	requireDenied(t, status, body, http.StatusForbidden, "WS.WORKSPACE.ACCESS_DENIED",
		"outsider deleting a webhook")
}

// TestCrossTenantExportIsolation verifies that a member of workspace A
// cannot export tasks from workspace B.
func TestCrossTenantExportIsolation(t *testing.T) {
	bootstrap(t)
	t.Parallel()

	owner := newTenant(t)
	outsider := newTenant(t)

	status, body := doJSONStatus(t, http.MethodGet,
		testServerURL+"/workspaces/"+owner.WorkspacePublicID+"/export/tasks?format=json",
		outsider.AccessToken, nil)
	requireDenied(t, status, body, http.StatusForbidden, "WS.WORKSPACE.ACCESS_DENIED",
		"outsider exporting tasks")
}

// TestCrossTenantCSVExportIsolation is the authorization baseline for
// the CSV download, taken before the route moves from a raw chi
// registration to a Huma operation.
//
// The route hands back every task in a workspace, so what matters when
// it moves is not that it still works but that it still refuses. The
// test is written against the route as it stands so the same assertions
// can be run after the move: a check written afterwards cannot tell
// "the move preserved this" from "this was never enforced".
func TestCrossTenantCSVExportIsolation(t *testing.T) {
	bootstrap(t)
	t.Parallel()

	owner := newTenant(t)
	outsider := newTenant(t)

	// The owner has something worth stealing.
	doJSON(t, http.MethodPost, testServerURL+"/tasks", owner.AccessToken,
		map[string]any{"projectId": owner.ProjectPublicID, "title": "CONFIDENTIAL-CSV-" + randomHex(8)}, nil)

	csvURL := testServerURL + "/workspaces/" + owner.WorkspacePublicID + "/export/tasks.csv"

	status, body := doJSONStatus(t, http.MethodGet, csvURL, outsider.AccessToken, nil)
	requireDenied(t, status, body, http.StatusForbidden, "WS.WORKSPACE.ACCESS_DENIED",
		"a member of another workspace downloading this workspace's CSV")
	require.NotContains(t, string(body), "CONFIDENTIAL-CSV-",
		"a refusal must not carry any of the rows it refused")

	// Unauthenticated is refused too, and one step earlier: the bearer
	// token never gets as far as the membership gate, so this is 401
	// from the authn layer rather than 403 from the ACL.
	status, body = doJSONStatus(t, http.MethodGet, csvURL, "", nil)
	requireDenied(t, status, body, http.StatusUnauthorized, "AUTH.TOKEN.MISSING_OR_MALFORMED",
		"an unauthenticated CSV download")
	require.NotContains(t, string(body), "CONFIDENTIAL-CSV-")

	// And the owner can still get it, so the assertions above are about
	// authorization rather than a route that answers nobody.
	status, body = doJSONStatus(t, http.MethodGet, csvURL, owner.AccessToken, nil)
	require.Equal(t, http.StatusOK, status, "the owner must be able to download the CSV")
	require.Contains(t, string(body), "CONFIDENTIAL-CSV-",
		"the owner's download must contain the owner's tasks")
}
