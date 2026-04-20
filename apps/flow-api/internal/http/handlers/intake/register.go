package intake

import (
	"net/http"

	"github.com/danielgtaylor/huma/v2"
)

// Register wires the workspace-scoped /workspaces/{wsId}/intake routes.
// The caller must attach RequireAuth and RequireWorkspaceMember middleware
// to the underlying chi router.
func Register(api huma.API, deps Deps) {
	huma.Register(api, huma.Operation{
		OperationID: "intake-list",
		Method:      http.MethodGet,
		Path:        "/workspaces/{wsId}/intake",
		Summary:     "List intake items in the triage queue",
	}, List(deps))

	huma.Register(api, huma.Operation{
		OperationID: "intake-create",
		Method:      http.MethodPost,
		Path:        "/workspaces/{wsId}/intake",
		Summary:     "Create an intake item",
	}, Create(deps))

	huma.Register(api, huma.Operation{
		OperationID: "intake-get",
		Method:      http.MethodGet,
		Path:        "/workspaces/{wsId}/intake/{id}",
		Summary:     "Get a single intake item",
	}, Get(deps))

	huma.Register(api, huma.Operation{
		OperationID: "intake-triage",
		Method:      http.MethodPatch,
		Path:        "/workspaces/{wsId}/intake/{id}",
		Summary:     "Triage an intake item (accept, reject, snooze, or mark duplicate)",
	}, Triage(deps))

	huma.Register(api, huma.Operation{
		OperationID: "intake-convert",
		Method:      http.MethodPost,
		Path:        "/workspaces/{wsId}/intake/{id}/convert",
		Summary:     "Convert an intake item into a task",
	}, Convert(deps))
}
