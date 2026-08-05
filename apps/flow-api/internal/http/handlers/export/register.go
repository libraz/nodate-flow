package export

import (
	"net/http"

	"github.com/danielgtaylor/huma/v2"
)

// RegisterWorkspaceScoped wires the workspace-scoped export routes
// under /workspaces/{wsId}/export. The caller must attach
// RequireWorkspaceMember to the underlying chi router.
//
// Both routes are Huma operations, so both appear in the OpenAPI
// document and the SDK generated from it. The CSV download used to be
// registered straight onto chi, which served it correctly and
// described it nowhere.
func RegisterWorkspaceScoped(api huma.API, deps Deps) {
	// JSON export via Huma (validated input, structured response).
	huma.Register(api, huma.Operation{
		OperationID: "export-tasks",
		Method:      http.MethodGet,
		Path:        "/workspaces/{wsId}/export/tasks",
		Summary:     "Export tasks as JSON",
		Description: "Export workspace tasks as a JSON array, optionally scoped by a saved lens.",
		Tags:        []string{"Admin"},
	}, Export(deps))

	// CSV download. The response is declared as text/csv so the spec
	// describes what the route sends; the handler streams the body.
	huma.Register(api, huma.Operation{
		OperationID: "export-tasks-csv",
		Method:      http.MethodGet,
		Path:        "/workspaces/{wsId}/export/tasks.csv",
		Summary:     "Export tasks as CSV",
		Description: "Download workspace tasks as a CSV file, optionally scoped by a saved lens.",
		Tags:        []string{"Admin"},
		Responses: map[string]*huma.Response{
			"200": {
				Description: "CSV file download",
				Content:     map[string]*huma.MediaType{"text/csv": {}},
				Headers: map[string]*huma.Param{
					RowCountHeader: {
						Description: "Number of task rows in the file. Equal to the requested limit means the export stopped at the ceiling and the workspace holds more.",
						Schema:      &huma.Schema{Type: "integer"},
					},
				},
			},
		},
	}, CSVOperation(deps))
}
