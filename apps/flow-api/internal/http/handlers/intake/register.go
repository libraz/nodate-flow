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
		Description: "Returns intake items pending triage (signals + manual submissions) ordered by priority. Backs the workspace Intake board.",
	}, List(deps))

	huma.Register(api, huma.Operation{
		OperationID: "intake-create",
		Method:      http.MethodPost,
		Path:        "/workspaces/{wsId}/intake",
		Summary:     "Create an intake item",
		Description: "Pushes a new intake row onto the workspace triage queue. Used by the manual 'capture' affordance and by integrations that want a human to review before a task is created.",
	}, Create(deps))

	huma.Register(api, huma.Operation{
		OperationID: "intake-get",
		Method:      http.MethodGet,
		Path:        "/workspaces/{wsId}/intake/{id}",
		Summary:     "Get a single intake item",
		Description: "Returns the named intake item with its source signal payload and current triage state. Used by the intake detail panel.",
	}, Get(deps))

	huma.Register(api, huma.Operation{
		OperationID: "intake-triage",
		Method:      http.MethodPatch,
		Path:        "/workspaces/{wsId}/intake/{id}",
		Summary:     "Triage an intake item (accept, reject, snooze, or mark duplicate)",
		Description: "Records the human triage decision on the named intake item: accept (forward to /convert), reject (drop with reason), snooze (revisit later), or mark duplicate of an existing task.",
	}, Triage(deps))

	huma.Register(api, huma.Operation{
		OperationID: "intake-convert",
		Method:      http.MethodPost,
		Path:        "/workspaces/{wsId}/intake/{id}/convert",
		Summary:     "Convert an intake item into a task",
		Description: "Creates a task from the intake row and links the two so the source signal is preserved on the task timeline. Closes the intake item as accepted.",
	}, Convert(deps))
}
