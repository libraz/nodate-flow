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
		Description: "Submits an import job (Asana / CSV / etc.) referencing previously uploaded source data. The job runs asynchronously; poll /imports/{importId} for progress.",
		Tags:        []string{"Admin"},
	}, Create(deps))

	huma.Register(api, huma.Operation{
		OperationID: "imports-list",
		Method:      http.MethodGet,
		Path:        "/workspaces/{wsId}/imports",
		Summary:     "List import jobs for the workspace",
		Description: "Returns recent import jobs in the workspace with their status (pending, running, succeeded, failed, cancelled). Backs the import history panel.",
		Tags:        []string{"Admin"},
	}, List(deps))

	huma.Register(api, huma.Operation{
		OperationID: "imports-get",
		Method:      http.MethodGet,
		Path:        "/workspaces/{wsId}/imports/{importId}",
		Summary:     "Get a single import job",
		Description: "Returns the full state of one import job: progress counters, error log, summary stats. Used by the in-progress poller and the post-run report.",
		Tags:        []string{"Admin"},
	}, Get(deps))

	huma.Register(api, huma.Operation{
		OperationID: "imports-cancel",
		Method:      http.MethodPost,
		Path:        "/workspaces/{wsId}/imports/{importId}/cancel",
		Summary:     "Cancel a pending or running import job",
		Description: "Marks the job as cancelled so the worker stops on its next checkpoint. Already-imported rows remain; partial state is documented in the import report.",
		Tags:        []string{"Admin"},
	}, Cancel(deps))
}
