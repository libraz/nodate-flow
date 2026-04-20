package reactions

import (
	"net/http"

	"github.com/danielgtaylor/huma/v2"
)

// RegisterTaskScoped wires the task-scoped reaction routes under
// /tasks/{id}/reactions. The caller must attach RequireTaskAccess to
// the underlying chi router so the task / workspace contexts are
// populated.
func RegisterTaskScoped(api huma.API, deps Deps) {
	huma.Register(api, huma.Operation{
		OperationID: "tasks-reactions-list",
		Method:      http.MethodGet,
		Path:        "/tasks/{id}/reactions",
		Summary:     "List reactions on a task",
	}, ListForTask(deps))

	huma.Register(api, huma.Operation{
		OperationID: "tasks-reactions-create",
		Method:      http.MethodPost,
		Path:        "/tasks/{id}/reactions",
		Summary:     "Add a reaction to a task",
	}, Create(deps))

	huma.Register(api, huma.Operation{
		OperationID: "tasks-reactions-delete",
		Method:      http.MethodDelete,
		Path:        "/tasks/{id}/reactions/{reactionId}",
		Summary:     "Remove a reaction from a task",
	}, Delete(deps))
}
