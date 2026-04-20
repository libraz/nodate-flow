package imports

import (
	"net/http"

	"github.com/danielgtaylor/huma/v2"
)

// Register wires the workspace-scoped /workspaces/{wsId}/imports routes.
// The caller must attach RequireAuth and RequireWorkspaceMember middleware
// to the underlying chi router.
func Register(api huma.API, deps Deps) {
	huma.Register(api, huma.Operation{
		OperationID: "imports-create",
		Method:      http.MethodPost,
		Path:        "/workspaces/{wsId}/imports",
		Summary:     "Create an import job",
	}, Create(deps))

	huma.Register(api, huma.Operation{
		OperationID: "imports-list",
		Method:      http.MethodGet,
		Path:        "/workspaces/{wsId}/imports",
		Summary:     "List import jobs for the workspace",
	}, List(deps))

	huma.Register(api, huma.Operation{
		OperationID: "imports-get",
		Method:      http.MethodGet,
		Path:        "/workspaces/{wsId}/imports/{importId}",
		Summary:     "Get a single import job",
	}, Get(deps))

	huma.Register(api, huma.Operation{
		OperationID: "imports-cancel",
		Method:      http.MethodPost,
		Path:        "/workspaces/{wsId}/imports/{importId}/cancel",
		Summary:     "Cancel a pending or running import job",
	}, Cancel(deps))
}
