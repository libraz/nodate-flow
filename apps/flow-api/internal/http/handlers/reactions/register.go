package reactions

import (
	"net/http"

	"github.com/danielgtaylor/huma/v2"
)

// RegisterTaskScoped wires every task-scoped reaction route on one chi group.
// Preserved for callers that want uniform middleware; the production router
// calls the split variants ([RegisterTaskScopedReads] /
// [RegisterTaskScopedWrites]) so a project viewer cannot leave reactions. The
// caller must attach RequireTaskAccess.
func RegisterTaskScoped(api huma.API, deps Deps) {
	RegisterTaskScopedReads(api, deps)
	RegisterTaskScopedWrites(api, deps)
}

// RegisterTaskScopedReads wires the read-only reaction listing under
// /tasks/{id}/reactions. Gated only by RequireTaskAccess.
func RegisterTaskScopedReads(api huma.API, deps Deps) {
	huma.Register(api, huma.Operation{
		OperationID: "tasks-reactions-list",
		Method:      http.MethodGet,
		Path:        "/tasks/{id}/reactions",
		Summary:     "List reactions on a task",
		Description: "Returns the emoji reactions left on the task grouped by glyph, with reactor identities. Used by the task header reaction strip.",
		Tags:        []string{"Tasks"},
	}, ListForTask(deps))
}

// RegisterTaskScopedWrites wires the reaction add / remove routes under
// /tasks/{id}/reactions. Leaving a reaction is a conversational action, so
// the caller must attach RequireTaskAccess followed by
// RequireProjectRole(ProjectRoleCommenter).
func RegisterTaskScopedWrites(api huma.API, deps Deps) {
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
