package relations

import (
	"net/http"

	"github.com/danielgtaylor/huma/v2"
)

// RegisterWorkspaceScoped wires the workspace-scoped relation suggestion
// routes. The caller must attach RequireAuth + RequireWorkspaceMember
// middleware.
func RegisterWorkspaceScoped(api huma.API, deps Deps) {
	huma.Register(api, huma.Operation{
		OperationID: "relation-suggestions-list-workspace",
		Method:      http.MethodGet,
		Path:        "/workspaces/{wsId}/relation-suggestions",
		Summary:     "List pending relation suggestions for a workspace",
	}, ListForWorkspace(deps))
}

// RegisterTaskScoped wires the task-scoped relation suggestion routes.
// The caller must attach RequireAuth + RequireTaskAccess middleware.
func RegisterTaskScoped(api huma.API, deps Deps) {
	huma.Register(api, huma.Operation{
		OperationID: "relation-suggestions-list-task",
		Method:      http.MethodGet,
		Path:        "/tasks/{id}/relation-suggestions",
		Summary:     "List pending relation suggestions for a task",
	}, ListForTask(deps))
}

// RegisterAuthScoped wires the auth-only relation suggestion routes.
// The caller must attach RequireAuth middleware.
func RegisterAuthScoped(api huma.API, deps Deps) {
	huma.Register(api, huma.Operation{
		OperationID: "relation-suggestions-resolve",
		Method:      http.MethodPost,
		Path:        "/relation-suggestions/{suggestionId}/resolve",
		Summary:     "Accept or dismiss a relation suggestion",
	}, Resolve(deps))
}
