package dashboard

import (
	"net/http"

	"github.com/danielgtaylor/huma/v2"
)

// RegisterWorkspaceScoped wires the workspace-scoped dashboard widget CRUD
// routes under /workspaces/{wsId}/dashboard/widgets. The caller must attach
// RequireWorkspaceMember to the underlying chi router.
func RegisterWorkspaceScoped(api huma.API, deps Deps) {
	huma.Register(api, huma.Operation{
		OperationID: "dashboard-widgets-create",
		Method:      http.MethodPost,
		Path:        "/workspaces/{wsId}/dashboard/widgets",
		Summary:     "Create a dashboard widget",
		Description: "Adds a new widget to the workspace dashboard with the provided kind and config blob. Returns the persisted widget including its assigned position so the client can render it immediately.",
		Tags:        []string{"Admin"},
	}, Create(deps))

	huma.Register(api, huma.Operation{
		OperationID: "dashboard-widgets-list",
		Method:      http.MethodGet,
		Path:        "/workspaces/{wsId}/dashboard/widgets",
		Summary:     "List dashboard widgets in a workspace",
		Description: "Returns every dashboard widget in the workspace ordered by position. Backs the dashboard rendering loop in flow-web.",
		Tags:        []string{"Admin"},
	}, List(deps))

	huma.Register(api, huma.Operation{
		OperationID: "dashboard-widgets-get",
		Method:      http.MethodGet,
		Path:        "/workspaces/{wsId}/dashboard/widgets/{widgetId}",
		Summary:     "Fetch a dashboard widget by id",
		Description: "Returns a single widget with its kind, config, and computed payload. Used when opening a widget's detail or edit panel.",
		Tags:        []string{"Admin"},
	}, Get(deps))

	huma.Register(api, huma.Operation{
		OperationID: "dashboard-widgets-update",
		Method:      http.MethodPatch,
		Path:        "/workspaces/{wsId}/dashboard/widgets/{widgetId}",
		Summary:     "Update a dashboard widget",
		Description: "Updates a widget's title and config blob. Position changes go through the dedicated /position endpoint to avoid heavy reflows on drag.",
		Tags:        []string{"Admin"},
	}, Update(deps))

	huma.Register(api, huma.Operation{
		OperationID: "dashboard-widgets-update-position",
		Method:      http.MethodPut,
		Path:        "/workspaces/{wsId}/dashboard/widgets/{widgetId}/position",
		Summary:     "Update widget position and size",
		Description: "Persists a widget's grid x/y coordinates and width/height after a drag-and-resize gesture. Idempotent and lightweight so it can fire on every drop.",
		Tags:        []string{"Admin"},
	}, UpdatePosition(deps))

	huma.Register(api, huma.Operation{
		OperationID: "dashboard-widgets-delete",
		Method:      http.MethodDelete,
		Path:        "/workspaces/{wsId}/dashboard/widgets/{widgetId}",
		Summary:     "Soft-delete a dashboard widget",
		Description: "Marks the widget as removed without erasing config so an undo affordance can restore it. Idempotent.",
		Tags:        []string{"Admin"},
	}, Delete(deps))
}
