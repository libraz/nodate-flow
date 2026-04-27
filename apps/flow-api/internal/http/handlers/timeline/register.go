package timeline

import (
	"net/http"

	"github.com/danielgtaylor/huma/v2"
)

// RegisterTaskScoped wires GET /tasks/{id}/timeline. The caller must attach
// RequireTaskAccess to the underlying chi router.
func RegisterTaskScoped(api huma.API, deps Deps) {
	huma.Register(api, huma.Operation{
		OperationID: "tasks-timeline-list",
		Method:      http.MethodGet,
		Path:        "/tasks/{id}/timeline",
		Summary:     "List events in a task's timeline",
		Description: "Returns the chronological event log for the task (state transitions, comments, link changes, AI suggestions). Backs the activity tab on the task detail view.",
	}, ListForTask(deps))
}

// RegisterProjectScoped wires GET /projects/{prjId}/timeline. The caller
// must attach RequireProjectMemberByGlobalID to the underlying chi router.
func RegisterProjectScoped(api huma.API, deps Deps) {
	huma.Register(api, huma.Operation{
		OperationID: "projects-timeline-list",
		Method:      http.MethodGet,
		Path:        "/projects/{prjId}/timeline",
		Summary:     "List events in a project's timeline",
		Description: "Returns the project-wide event stream (every task event in the project plus project-level events) so the project Activity tab can render a unified feed.",
	}, ListForProject(deps))
}

// RegisterWorkspaceScoped wires GET /workspaces/{wsId}/timeline. The caller
// must attach RequireWorkspaceMember to the underlying chi router.
func RegisterWorkspaceScoped(api huma.API, deps Deps) {
	huma.Register(api, huma.Operation{
		OperationID: "workspaces-timeline-list",
		Method:      http.MethodGet,
		Path:        "/workspaces/{wsId}/timeline",
		Summary:     "List events in a workspace's timeline",
		Description: "Returns the workspace-wide event stream (every task / project / signal event the caller can see). Cursor-paginated and intended for the workspace Activity view.",
	}, ListForWorkspace(deps))
}
