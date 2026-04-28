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
		Description: "Returns the emoji reactions left on the task grouped by glyph, with reactor identities. Used by the task header reaction strip.",
		Tags:        []string{"Tasks"},
	}, ListForTask(deps))

	huma.Register(api, huma.Operation{
		OperationID: "tasks-reactions-create",
		Method:      http.MethodPost,
		Path:        "/tasks/{id}/reactions",
		Summary:     "Add a reaction to a task",
		Description: "Records a single emoji reaction from the caller on the task. Idempotent per (task, user, glyph): a duplicate add returns the existing row.",
		Tags:        []string{"Tasks"},
	}, Create(deps))

	huma.Register(api, huma.Operation{
		OperationID: "tasks-reactions-delete",
		Method:      http.MethodDelete,
		Path:        "/tasks/{id}/reactions/{reactionId}",
		Summary:     "Remove a reaction from a task",
		Description: "Removes the caller's reaction from the task. Idempotent: deleting an already-removed reaction returns 200.",
		Tags:        []string{"Tasks"},
	}, Delete(deps))
}
