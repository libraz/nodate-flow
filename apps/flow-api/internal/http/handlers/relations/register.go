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
		Description: "Returns auto-detected task relation candidates (likely duplicates / dependencies) that the embedding pipeline has surfaced for the workspace and that no one has accepted or dismissed yet.",
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
		Description: "Returns the relation candidates the auto-detect pipeline has surfaced for the named task. Sorted by similarity score so the most likely match is first.",
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
		Description: "Records the human decision on the suggestion: accept (creates the actual relation row) or dismiss (suppresses it from future listings). Idempotent.",
	}, Resolve(deps))
}
