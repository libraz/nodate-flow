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
	}, ListForTask(deps))
}

// RegisterWorkspaceScoped wires GET /workspaces/{wsId}/timeline. The caller
// must attach RequireWorkspaceMember to the underlying chi router.
func RegisterWorkspaceScoped(api huma.API, deps Deps) {
	huma.Register(api, huma.Operation{
		OperationID: "workspaces-timeline-list",
		Method:      http.MethodGet,
		Path:        "/workspaces/{wsId}/timeline",
		Summary:     "List events in a workspace's timeline",
	}, ListForWorkspace(deps))
}
