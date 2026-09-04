package reactions

import (
	"net/http"

	"github.com/danielgtaylor/huma/v2"
)

// RegisterTaskScopedReads wires the read-only reaction listing under
// /tasks/{id}/reactions. Gated only by RequireTaskAccess, so any role that
// can see the task may call it.
//
// The read belongs on its own chi group, separate from
// [RegisterTaskScopedWrites]: leaving or withdrawing a reaction takes a
// project commenter role, and mounting both on one group with uniform
// middleware would let a project viewer react.
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
