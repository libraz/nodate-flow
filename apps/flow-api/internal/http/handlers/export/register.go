package export

import (
	"net/http"

	"github.com/danielgtaylor/huma/v2"
	"github.com/go-chi/chi/v5"
)

// RegisterWorkspaceScoped wires the workspace-scoped export routes
// under /workspaces/{wsId}/export. The caller must attach
// RequireWorkspaceMember to the underlying chi router.
//
// Two routes are registered:
//   - GET /workspaces/{wsId}/export/tasks (Huma, JSON response)
//   - GET /workspaces/{wsId}/export/tasks.csv (chi raw handler, CSV download)
func RegisterWorkspaceScoped(api huma.API, router chi.Router, deps Deps) {
	// JSON export via Huma (validated input, structured response).
	huma.Register(api, huma.Operation{
		OperationID: "export-tasks",
		Method:      http.MethodGet,
		Path:        "/workspaces/{wsId}/export/tasks",
		Summary:     "Export tasks as JSON",
		Description: "Export workspace tasks as a JSON array, optionally scoped by a saved lens.",
	}, Export(deps))

	// CSV export as a raw chi handler (file download).
	router.Get("/workspaces/{wsId}/export/tasks.csv", CSV(deps))
}
