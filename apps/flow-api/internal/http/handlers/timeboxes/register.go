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
		Description: "Creates a new timebox (sprint / focus block) with a date range, owner, and goal. Tasks are attached separately via /timeboxes/{id}/tasks.",
		Tags:        []string{"Tasks"},
	}, Create(deps))

	huma.Register(api, huma.Operation{
		OperationID: "timeboxes-list",
		Method:      http.MethodGet,
		Path:        "/workspaces/{wsId}/timeboxes",
		Summary:     "List timeboxes in a workspace",
		Description: "Returns timeboxes in the workspace ordered by start date. Used by the timebox planner to render past, current, and upcoming periods.",
		Tags:        []string{"Tasks"},
	}, List(deps))

	huma.Register(api, huma.Operation{
		OperationID: "timeboxes-get",
		Method:      http.MethodGet,
		Path:        "/workspaces/{wsId}/timeboxes/{timeboxId}",
		Summary:     "Fetch a timebox by id",
		Description: "Returns a single timebox with metadata and aggregate progress (counts only). Use /tasks for the task list.",
		Tags:        []string{"Tasks"},
	}, Get(deps))

	huma.Register(api, huma.Operation{
		OperationID: "timeboxes-update",
		Method:      http.MethodPatch,
		Path:        "/workspaces/{wsId}/timeboxes/{timeboxId}",
		Summary:     "Update a timebox",
		Description: "Updates timebox name, goal, owner, or date range. Status changes use the dedicated /status endpoint.",
		Tags:        []string{"Tasks"},
	}, Update(deps))

	huma.Register(api, huma.Operation{
		OperationID: "timeboxes-update-status",
		Method:      http.MethodPost,
		Path:        "/workspaces/{wsId}/timeboxes/{timeboxId}/status",
		Summary:     "Transition timebox status",
		Description: "Moves the timebox between planned / active / completed / cancelled. Status transitions enforce a state machine and emit timeline events.",
		Tags:        []string{"Tasks"},
	}, UpdateStatus(deps))

	huma.Register(api, huma.Operation{
		OperationID: "timeboxes-delete",
		Method:      http.MethodDelete,
		Path:        "/workspaces/{wsId}/timeboxes/{timeboxId}",
		Summary:     "Soft-delete a timebox",
		Description: "Marks the timebox as removed. Tasks previously associated stay queryable; the link rows are tombstoned. Idempotent.",
		Tags:        []string{"Tasks"},
	}, Delete(deps))

	huma.Register(api, huma.Operation{
		OperationID: "timeboxes-add-task",
		Method:      http.MethodPost,
		Path:        "/workspaces/{wsId}/timeboxes/{timeboxId}/tasks",
		Summary:     "Add a task to a timebox",
		Description: "Associates an existing workspace task with the timebox so it counts toward the timebox's progress and burndown.",
		Tags:        []string{"Tasks"},
	}, AddTask(deps))

	huma.Register(api, huma.Operation{
		OperationID: "timeboxes-remove-task",
		Method:      http.MethodDelete,
		Path:        "/workspaces/{wsId}/timeboxes/{timeboxId}/tasks/{taskId}",
		Summary:     "Remove a task from a timebox",
		Description: "Detaches the task from the timebox without affecting the task itself. Idempotent.",
		Tags:        []string{"Tasks"},
	}, RemoveTask(deps))

	huma.Register(api, huma.Operation{
		OperationID: "timeboxes-list-tasks",
		Method:      http.MethodGet,
		Path:        "/workspaces/{wsId}/timeboxes/{timeboxId}/tasks",
		Summary:     "List tasks in a timebox with progress",
		Description: "Returns the tasks associated with the timebox plus per-task progress and the aggregate completion percentage.",
		Tags:        []string{"Tasks"},
	}, ListTasks(deps))
}
