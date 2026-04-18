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
	}, Create(deps))

	huma.Register(api, huma.Operation{
		OperationID: "dashboard-widgets-list",
		Method:      http.MethodGet,
		Path:        "/workspaces/{wsId}/dashboard/widgets",
		Summary:     "List dashboard widgets in a workspace",
	}, List(deps))

	huma.Register(api, huma.Operation{
		OperationID: "dashboard-widgets-get",
		Method:      http.MethodGet,
		Path:        "/workspaces/{wsId}/dashboard/widgets/{widgetId}",
		Summary:     "Fetch a dashboard widget by id",
	}, Get(deps))

	huma.Register(api, huma.Operation{
		OperationID: "dashboard-widgets-update",
		Method:      http.MethodPatch,
		Path:        "/workspaces/{wsId}/dashboard/widgets/{widgetId}",
		Summary:     "Update a dashboard widget",
	}, Update(deps))

	huma.Register(api, huma.Operation{
		OperationID: "dashboard-widgets-update-position",
		Method:      http.MethodPut,
		Path:        "/workspaces/{wsId}/dashboard/widgets/{widgetId}/position",
		Summary:     "Update widget position and size",
	}, UpdatePosition(deps))

	huma.Register(api, huma.Operation{
		OperationID: "dashboard-widgets-delete",
		Method:      http.MethodDelete,
		Path:        "/workspaces/{wsId}/dashboard/widgets/{widgetId}",
		Summary:     "Soft-delete a dashboard widget",
	}, Delete(deps))
}
