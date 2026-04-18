package e2e

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestDashboardWidgetCRUD verifies a workspace member can create, list,
// get, update, reposition, and soft-delete dashboard widgets.
func TestDashboardWidgetCRUD(t *testing.T) {
	bootstrap(t)
	t.Parallel()
	tt := newTenant(t)

	base := testServerURL + "/workspaces/" + tt.WorkspacePublicID + "/dashboard/widgets"

	// --- Create a widget ---
	var created struct {
		ID         string `json:"id"`
		WidgetType string `json:"widgetType"`
		Title      string `json:"title"`
		PositionX  int    `json:"positionX"`
		PositionY  int    `json:"positionY"`
		Width      int    `json:"width"`
		Height     int    `json:"height"`
	}
	doJSON(t, http.MethodPost, base, tt.AccessToken, map[string]any{
		"widgetType": "task_summary",
		"title":      "My Tasks",
		"positionX":  0,
		"positionY":  0,
		"width":      4,
		"height":     3,
	}, &created)
	require.NotEmpty(t, created.ID)
	require.Equal(t, "task_summary", created.WidgetType)
	require.Equal(t, "My Tasks", created.Title)
	require.Equal(t, 4, created.Width)

	// --- List widgets returns the created widget ---
	var list struct {
		Total   int64 `json:"total"`
		Widgets []struct {
			ID string `json:"id"`
		} `json:"widgets"`
	}
	doJSON(t, http.MethodGet, base, tt.AccessToken, nil, &list)
	require.GreaterOrEqual(t, list.Total, int64(1))

	// --- Get by ID ---
	var got struct {
		ID    string `json:"id"`
		Title string `json:"title"`
	}
	doJSON(t, http.MethodGet, base+"/"+created.ID, tt.AccessToken, nil, &got)
	require.Equal(t, created.ID, got.ID)

	// --- Update title ---
	var patched struct {
		Title string `json:"title"`
	}
	doJSON(t, http.MethodPatch, base+"/"+created.ID, tt.AccessToken,
		map[string]any{"title": "Updated Widget"}, &patched)
	require.Equal(t, "Updated Widget", patched.Title)

	// --- Reposition widget ---
	status, _ := doJSONStatus(t, http.MethodPut, base+"/"+created.ID+"/position",
		tt.AccessToken, map[string]any{
			"positionX":  2,
			"positionY":  1,
			"width":      6,
			"height":     4,
			"sortWeight": 10,
		})
	require.GreaterOrEqual(t, status, 200)
	require.Less(t, status, 300)

	// Verify new position stuck.
	var repositioned struct {
		PositionX int `json:"positionX"`
		PositionY int `json:"positionY"`
		Width     int `json:"width"`
		Height    int `json:"height"`
	}
	doJSON(t, http.MethodGet, base+"/"+created.ID, tt.AccessToken, nil, &repositioned)
	require.Equal(t, 2, repositioned.PositionX)
	require.Equal(t, 6, repositioned.Width)

	// --- Delete widget ---
	status, _ = doJSONStatus(t, http.MethodDelete, base+"/"+created.ID,
		tt.AccessToken, nil)
	require.Equal(t, http.StatusOK, status)

	// --- Deleted widget should not appear in list ---
	var listAfter struct {
		Total   int64 `json:"total"`
		Widgets []struct {
			ID string `json:"id"`
		} `json:"widgets"`
	}
	doJSON(t, http.MethodGet, base, tt.AccessToken, nil, &listAfter)
	for _, w := range listAfter.Widgets {
		require.NotEqual(t, created.ID, w.ID, "deleted widget must not appear in list")
	}
}
