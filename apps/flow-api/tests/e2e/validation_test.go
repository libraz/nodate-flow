package e2e

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestPageValidation verifies that the API rejects invalid page inputs.
func TestPageValidation(t *testing.T) {
	bootstrap(t)
	t.Parallel()
	tt := newTenant(t)

	base := testServerURL + "/workspaces/" + tt.WorkspacePublicID + "/pages"

	// Empty title must be rejected.
	status, _ := doJSONStatus(t, http.MethodPost, base, tt.AccessToken,
		map[string]any{"title": ""})
	require.GreaterOrEqual(t, status, 400, "empty title must be rejected")

	// Title exceeding max length (500 chars) must be rejected.
	longTitle := strings.Repeat("a", 501)
	status, _ = doJSONStatus(t, http.MethodPost, base, tt.AccessToken,
		map[string]any{"title": longTitle})
	require.GreaterOrEqual(t, status, 400, "title over 500 chars must be rejected")
}

// TestTimeboxValidation verifies that the API rejects invalid timebox inputs.
func TestTimeboxValidation(t *testing.T) {
	bootstrap(t)
	t.Parallel()
	tt := newTenant(t)

	base := testServerURL + "/workspaces/" + tt.WorkspacePublicID + "/timeboxes"

	// Missing required name.
	status, _ := doJSONStatus(t, http.MethodPost, base, tt.AccessToken,
		map[string]any{"startsOn": "2025-06-01", "endsOn": "2025-06-14"})
	require.GreaterOrEqual(t, status, 400, "missing name must be rejected")

	// Missing required dates.
	status, _ = doJSONStatus(t, http.MethodPost, base, tt.AccessToken,
		map[string]any{"name": "Sprint"})
	require.GreaterOrEqual(t, status, 400, "missing dates must be rejected")
}

// TestTimeboxInvalidTransition verifies that invalid status transitions
// are rejected (e.g. completed → active).
func TestTimeboxInvalidTransition(t *testing.T) {
	bootstrap(t)
	t.Parallel()
	tt := newTenant(t)

	base := testServerURL + "/workspaces/" + tt.WorkspacePublicID + "/timeboxes"

	// Create and complete a timebox.
	var tb struct {
		ID string `json:"id"`
	}
	doJSON(t, http.MethodPost, base, tt.AccessToken, map[string]any{
		"name": "Closed Sprint", "startsOn": "2025-07-01", "endsOn": "2025-07-14",
	}, &tb)

	doJSON(t, http.MethodPost, base+"/"+tb.ID+"/status", tt.AccessToken,
		map[string]any{"status": "active"}, nil)
	doJSON(t, http.MethodPost, base+"/"+tb.ID+"/status", tt.AccessToken,
		map[string]any{"status": "completed"}, nil)

	// completed → active should be rejected.
	status, _ := doJSONStatus(t, http.MethodPost, base+"/"+tb.ID+"/status",
		tt.AccessToken, map[string]any{"status": "active"})
	require.GreaterOrEqual(t, status, 400,
		"transitioning completed → active must be rejected")
}

// TestDashboardWidgetValidation verifies that the API rejects invalid
// widget inputs.
func TestDashboardWidgetValidation(t *testing.T) {
	bootstrap(t)
	t.Parallel()
	tt := newTenant(t)

	base := testServerURL + "/workspaces/" + tt.WorkspacePublicID + "/dashboard/widgets"

	// Zero-size widget must be rejected (width and height minimum is 1).
	status, _ := doJSONStatus(t, http.MethodPost, base, tt.AccessToken, map[string]any{
		"widgetType": "task_summary", "title": "Bad Widget",
		"positionX": 0, "positionY": 0, "width": 0, "height": 0,
	})
	require.GreaterOrEqual(t, status, 400, "zero-size widget must be rejected")
}

// TestLensValidation verifies that the API rejects invalid lens inputs.
func TestLensValidation(t *testing.T) {
	bootstrap(t)
	t.Parallel()
	tt := newTenant(t)

	base := testServerURL + "/workspaces/" + tt.WorkspacePublicID + "/lenses"

	// Empty name must be rejected.
	status, _ := doJSONStatus(t, http.MethodPost, base, tt.AccessToken, map[string]any{
		"name": "", "filter": json.RawMessage(`{}`), "sort": json.RawMessage(`[]`),
		"isDefault": false,
	})
	require.GreaterOrEqual(t, status, 400, "empty lens name must be rejected")

	// Name exceeding max length (100 chars) must be rejected.
	longName := strings.Repeat("x", 101)
	status, _ = doJSONStatus(t, http.MethodPost, base, tt.AccessToken, map[string]any{
		"name": longName, "filter": json.RawMessage(`{}`), "sort": json.RawMessage(`[]`),
		"isDefault": false,
	})
	require.GreaterOrEqual(t, status, 400, "lens name over 100 chars must be rejected")
}

// TestWebhookValidation verifies that the API rejects invalid webhook inputs.
func TestWebhookValidation(t *testing.T) {
	bootstrap(t)
	t.Parallel()
	tt := newTenant(t)

	base := testServerURL + "/workspaces/" + tt.WorkspacePublicID + "/webhooks"

	// Missing URL must be rejected.
	status, _ := doJSONStatus(t, http.MethodPost, base, tt.AccessToken, map[string]any{
		"description": "No URL", "eventTypes": json.RawMessage(`["*"]`),
	})
	require.GreaterOrEqual(t, status, 400, "missing URL must be rejected")
}

// TestExportValidation verifies that invalid export parameters are rejected.
func TestExportValidation(t *testing.T) {
	bootstrap(t)
	t.Parallel()
	tt := newTenant(t)

	base := testServerURL + "/workspaces/" + tt.WorkspacePublicID + "/export/tasks"

	// Invalid format value.
	status, _ := doJSONStatus(t, http.MethodGet, base+"?format=xml",
		tt.AccessToken, nil)
	require.GreaterOrEqual(t, status, 400, "invalid format must be rejected")
}
