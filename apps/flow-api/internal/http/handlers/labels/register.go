package labels

import (
	"net/http"

	"github.com/danielgtaylor/huma/v2"
)

// RegisterWorkspaceScoped wires the workspace-scoped label CRUD routes
// under /workspaces/{wsId}/labels. The caller must attach
// RequireWorkspaceMember to the underlying chi router.
func RegisterWorkspaceScoped(api huma.API, deps Deps) {
	huma.Register(api, huma.Operation{
		OperationID: "labels-list",
		Method:      http.MethodGet,
		Path:        "/workspaces/{wsId}/labels",
		Summary:     "List labels in a workspace",
		Description: "Returns every active label in the workspace ordered by name. Used to populate label pickers and filter chips.",
		Tags:        []string{"Labels"},
	}, List(deps))

	huma.Register(api, huma.Operation{
		OperationID: "labels-create",
		Method:      http.MethodPost,
		Path:        "/workspaces/{wsId}/labels",
		Summary:     "Create a label",
		Description: "Creates a workspace label with name and color. Names are unique within the workspace.",
		Tags:        []string{"Labels"},
	}, Create(deps))

	huma.Register(api, huma.Operation{
		OperationID: "labels-get",
		Method:      http.MethodGet,
		Path:        "/workspaces/{wsId}/labels/{id}",
		Summary:     "Fetch a label",
		Description: "Returns one label including its name, color, and disabled state. Used by the label edit panel.",
		Tags:        []string{"Labels"},
	}, Get(deps))

	huma.Register(api, huma.Operation{
		OperationID: "labels-patch",
		Method:      http.MethodPatch,
		Path:        "/workspaces/{wsId}/labels/{id}",
		Summary:     "Update a label",
		Description: "Renames or recolors the label. Existing task assignments keep working; the new name and color propagate immediately.",
		Tags:        []string{"Labels"},
	}, Patch(deps))

	huma.Register(api, huma.Operation{
		OperationID: "labels-disable",
		Method:      http.MethodDelete,
		Path:        "/workspaces/{wsId}/labels/{id}",
		Summary:     "Soft-disable a label",
		Description: "Marks the label as disabled so it disappears from pickers without breaking historical task assignments. Idempotent.",
		Tags:        []string{"Labels"},
	}, Disable(deps))
}

// RegisterTaskScoped wires every task-scoped label route on one chi group.
// Preserved for callers that want uniform middleware; the production router
// calls the split variants ([RegisterTaskScopedReads] /
// [RegisterTaskScopedWrites]) so a read-only project role cannot attach or
// remove labels. The caller must attach RequireTaskAccess.
func RegisterTaskScoped(api huma.API, deps Deps) {
	RegisterTaskScopedReads(api, deps)
	RegisterTaskScopedWrites(api, deps)
}

// RegisterTaskScopedReads wires the read-only task-scoped label route under
// /tasks/{id}/labels. Gated only by RequireTaskAccess.
func RegisterTaskScopedReads(api huma.API, deps Deps) {
	huma.Register(api, huma.Operation{
		OperationID: "tasks-labels-list",
		Method:      http.MethodGet,
		Path:        "/tasks/{id}/labels",
		Summary:     "List labels on a task",
		Description: "Returns the labels currently attached to the task, in the order they were added. Used by the task detail view's label chips.",
		Tags:        []string{"Labels"},
	}, ListTaskLabels(deps))
}

// RegisterTaskScopedWrites wires the label attach / remove routes under
// /tasks/{id}/labels. Attaching and removing labels is a structural edit, so
// the caller must attach RequireTaskAccess followed by
// RequireProjectRole(ProjectRoleEditor).
func RegisterTaskScopedWrites(api huma.API, deps Deps) {
	huma.Register(api, huma.Operation{
		OperationID: "tasks-labels-add",
		Method:      http.MethodPost,
		Path:        "/tasks/{id}/labels",
		Summary:     "Attach a label to a task",
		Description: "Attaches an existing workspace label to the task. Idempotent: re-attaching a label is a no-op.",
		Tags:        []string{"Labels"},
	}, AddTaskLabel(deps))

	huma.Register(api, huma.Operation{
		OperationID: "tasks-labels-remove",
		Method:      http.MethodDelete,
		Path:        "/tasks/{id}/labels/{labelId}",
		Summary:     "Remove a label from a task",
		Description: "Detaches the named label from the task. Idempotent: returns 200 even when the label was not attached.",
		Tags:        []string{"Labels"},
	}, RemoveTaskLabel(deps))
}
