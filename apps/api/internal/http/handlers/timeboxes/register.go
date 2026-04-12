package timeboxes

import (
	"net/http"

	"github.com/danielgtaylor/huma/v2"
)

// RegisterWorkspaceScoped wires the workspace-scoped timebox CRUD and
// task-association routes under /workspaces/{wsId}/timeboxes. The caller
// must attach RequireWorkspaceMember to the underlying chi router.
func RegisterWorkspaceScoped(api huma.API, deps Deps) {
	huma.Register(api, huma.Operation{
		OperationID: "timeboxes-create",
		Method:      http.MethodPost,
		Path:        "/workspaces/{wsId}/timeboxes",
		Summary:     "Create a timebox",
	}, Create(deps))

	huma.Register(api, huma.Operation{
		OperationID: "timeboxes-list",
		Method:      http.MethodGet,
		Path:        "/workspaces/{wsId}/timeboxes",
		Summary:     "List timeboxes in a workspace",
	}, List(deps))

	huma.Register(api, huma.Operation{
		OperationID: "timeboxes-get",
		Method:      http.MethodGet,
		Path:        "/workspaces/{wsId}/timeboxes/{timeboxId}",
		Summary:     "Fetch a timebox by id",
	}, Get(deps))

	huma.Register(api, huma.Operation{
		OperationID: "timeboxes-update",
		Method:      http.MethodPatch,
		Path:        "/workspaces/{wsId}/timeboxes/{timeboxId}",
		Summary:     "Update a timebox",
	}, Update(deps))

	huma.Register(api, huma.Operation{
		OperationID: "timeboxes-update-status",
		Method:      http.MethodPost,
		Path:        "/workspaces/{wsId}/timeboxes/{timeboxId}/status",
		Summary:     "Transition timebox status",
	}, UpdateStatus(deps))

	huma.Register(api, huma.Operation{
		OperationID: "timeboxes-delete",
		Method:      http.MethodDelete,
		Path:        "/workspaces/{wsId}/timeboxes/{timeboxId}",
		Summary:     "Soft-delete a timebox",
	}, Delete(deps))

	huma.Register(api, huma.Operation{
		OperationID: "timeboxes-add-task",
		Method:      http.MethodPost,
		Path:        "/workspaces/{wsId}/timeboxes/{timeboxId}/tasks",
		Summary:     "Add a task to a timebox",
	}, AddTask(deps))

	huma.Register(api, huma.Operation{
		OperationID: "timeboxes-remove-task",
		Method:      http.MethodDelete,
		Path:        "/workspaces/{wsId}/timeboxes/{timeboxId}/tasks/{taskId}",
		Summary:     "Remove a task from a timebox",
	}, RemoveTask(deps))

	huma.Register(api, huma.Operation{
		OperationID: "timeboxes-list-tasks",
		Method:      http.MethodGet,
		Path:        "/workspaces/{wsId}/timeboxes/{timeboxId}/tasks",
		Summary:     "List tasks in a timebox with progress",
	}, ListTasks(deps))
}
