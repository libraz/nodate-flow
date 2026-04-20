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
	}, List(deps))

	huma.Register(api, huma.Operation{
		OperationID: "labels-create",
		Method:      http.MethodPost,
		Path:        "/workspaces/{wsId}/labels",
		Summary:     "Create a label",
	}, Create(deps))

	huma.Register(api, huma.Operation{
		OperationID: "labels-get",
		Method:      http.MethodGet,
		Path:        "/workspaces/{wsId}/labels/{id}",
		Summary:     "Fetch a label",
	}, Get(deps))

	huma.Register(api, huma.Operation{
		OperationID: "labels-patch",
		Method:      http.MethodPatch,
		Path:        "/workspaces/{wsId}/labels/{id}",
		Summary:     "Update a label",
	}, Patch(deps))

	huma.Register(api, huma.Operation{
		OperationID: "labels-disable",
		Method:      http.MethodDelete,
		Path:        "/workspaces/{wsId}/labels/{id}",
		Summary:     "Soft-disable a label",
	}, Disable(deps))
}

// RegisterTaskScoped wires the task-scoped label routes under
// /tasks/{id}/labels. The caller must attach RequireTaskAccess to the
// underlying chi router.
func RegisterTaskScoped(api huma.API, deps Deps) {
	huma.Register(api, huma.Operation{
		OperationID: "tasks-labels-list",
		Method:      http.MethodGet,
		Path:        "/tasks/{id}/labels",
		Summary:     "List labels on a task",
	}, ListTaskLabels(deps))

	huma.Register(api, huma.Operation{
		OperationID: "tasks-labels-add",
		Method:      http.MethodPost,
		Path:        "/tasks/{id}/labels",
		Summary:     "Attach a label to a task",
	}, AddTaskLabel(deps))

	huma.Register(api, huma.Operation{
		OperationID: "tasks-labels-remove",
		Method:      http.MethodDelete,
		Path:        "/tasks/{id}/labels/{labelId}",
		Summary:     "Remove a label from a task",
	}, RemoveTaskLabel(deps))
}
