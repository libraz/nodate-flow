package lenses

import (
	"net/http"

	"github.com/danielgtaylor/huma/v2"
)

// RegisterWorkspaceScoped wires the workspace-scoped lens (saved view)
// CRUD routes under /workspaces/{wsId}/lenses. The caller must attach
// RequireWorkspaceMember to the underlying chi router.
func RegisterWorkspaceScoped(api huma.API, deps Deps) {
	huma.Register(api, huma.Operation{
		OperationID: "lenses-create",
		Method:      http.MethodPost,
		Path:        "/workspaces/{wsId}/lenses",
		Summary:     "Create a saved view (lens)",
	}, Create(deps))

	huma.Register(api, huma.Operation{
		OperationID: "lenses-list",
		Method:      http.MethodGet,
		Path:        "/workspaces/{wsId}/lenses",
		Summary:     "List saved views for a workspace/project",
	}, List(deps))

	huma.Register(api, huma.Operation{
		OperationID: "lenses-get",
		Method:      http.MethodGet,
		Path:        "/workspaces/{wsId}/lenses/{lensId}",
		Summary:     "Fetch a saved view",
	}, Get(deps))

	huma.Register(api, huma.Operation{
		OperationID: "lenses-update",
		Method:      http.MethodPatch,
		Path:        "/workspaces/{wsId}/lenses/{lensId}",
		Summary:     "Update a saved view",
	}, Update(deps))

	huma.Register(api, huma.Operation{
		OperationID: "lenses-delete",
		Method:      http.MethodDelete,
		Path:        "/workspaces/{wsId}/lenses/{lensId}",
		Summary:     "Delete a saved view",
	}, Delete(deps))
}
