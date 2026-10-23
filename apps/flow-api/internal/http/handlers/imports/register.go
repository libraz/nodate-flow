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
		Description: "Queues an import job. Only the csv source runs today, with the file content in configJson.csv; github, jira and linear jobs are accepted and finish as failed because they have no connector yet. A job targeting a project needs the project editor role, and a project holds at most one pending or running job (WS.IMPORT.ALREADY_RUNNING otherwise). The job runs asynchronously; poll /imports/{importId} for progress. configJson is stored in plaintext and returned by the read endpoints, so it rejects any key that names a credential; connect an external service through its integration settings instead.",
		Tags:        []string{"Admin"},
	}, Create(deps))

	huma.Register(api, huma.Operation{
		OperationID: "imports-list",
		Method:      http.MethodGet,
		Path:        "/workspaces/{wsId}/imports",
		Summary:     "List import jobs for the workspace",
		Description: "Returns recent import jobs in the workspace with their status (pending, running, completed, failed, cancelled). Backs the import history panel.",
		Tags:        []string{"Admin"},
	}, List(deps))

	huma.Register(api, huma.Operation{
		OperationID: "imports-get",
		Method:      http.MethodGet,
		Path:        "/workspaces/{wsId}/imports/{importId}",
		Summary:     "Get a single import job",
		Description: "Returns the full state of one import job: status, progress counters, error log and start / completion times. Used by the in-progress poller and the post-run report.",
		Tags:        []string{"Admin"},
	}, Get(deps))

	huma.Register(api, huma.Operation{
		OperationID: "imports-cancel",
		Method:      http.MethodPost,
		Path:        "/workspaces/{wsId}/imports/{importId}/cancel",
		Summary:     "Cancel a pending or running import job",
		Description: "Marks a pending or running job as cancelled so the worker stops at its next checkpoint. Tasks already imported remain; the counters show how far the job got.",
		Tags:        []string{"Admin"},
	}, Cancel(deps))
}
