package projects

import (
	"net/http"

	"github.com/danielgtaylor/huma/v2"
)

// RegisterWorkspaceScoped wires the workspace-scoped project routes
// (POST/GET under /workspaces/{wsId}/projects). The caller must attach
// RequireWorkspaceMember (and any required role middleware) to the
// underlying chi router.
func RegisterWorkspaceScoped(api huma.API, deps Deps) {
	huma.Register(api, huma.Operation{
		OperationID: "projects-create",
		Method:      http.MethodPost,
		Path:        "/workspaces/{wsId}/projects",
		Summary:     "Create a project in a workspace",
		Description: "Creates a new project in the workspace. The caller becomes the first project member with admin role. Requires workspace admin role.",
		Tags:        []string{"Projects"},
	}, Create(deps))

	huma.Register(api, huma.Operation{
		OperationID: "projects-list",
		Method:      http.MethodGet,
		Path:        "/workspaces/{wsId}/projects",
		Summary:     "List projects in a workspace",
		Description: "Returns every project the caller can see in the workspace. Backs the project switcher and the project list panel.",
		Tags:        []string{"Projects"},
	}, List(deps))
}

// RegisterGlobal wires the global project routes (under /projects/{prjId}).
// The caller must attach RequireProjectMemberByGlobalID to the underlying
// chi router.
func RegisterGlobal(api huma.API, deps Deps) {
	huma.Register(api, huma.Operation{
		OperationID: "projects-get",
		Method:      http.MethodGet,
		Path:        "/projects/{prjId}",
		Summary:     "Fetch a project",
		Description: "Returns the project metadata (name, description, status, settings). Used by the project header and detail pages.",
		Tags:        []string{"Projects"},
	}, Get(deps))

	huma.Register(api, huma.Operation{
		OperationID: "projects-patch",
		Method:      http.MethodPatch,
		Path:        "/projects/{prjId}",
		Summary:     "Patch a project",
		Description: "Updates editable project fields (name, description, status, settings). Project admin role required.",
		Tags:        []string{"Projects"},
	}, Patch(deps))

	huma.Register(api, huma.Operation{
		OperationID: "projects-disable",
		Method:      http.MethodDelete,
		Path:        "/projects/{prjId}",
		Summary:     "Soft-disable a project",
		Description: "Marks the project as disabled so it disappears from listings and pickers. Tasks remain queryable for audit but reject new edits.",
		Tags:        []string{"Projects"},
	}, Disable(deps))

	huma.Register(api, huma.Operation{
		OperationID: "projects-dependencies-list",
		Method:      http.MethodGet,
		Path:        "/projects/{prjId}/dependencies",
		Summary:     "List every task dependency edge within a project",
		Description: "Returns every blocks/blocked-by edge between tasks inside the project so the dependency graph view can render the full network in one round trip.",
		Tags:        []string{"Projects"},
	}, ListDependencies(deps))

	huma.Register(api, huma.Operation{
		OperationID: "projects-members-list",
		Method:      http.MethodGet,
		Path:        "/projects/{prjId}/members",
		Summary:     "List members of a project",
		Description: "Lists every active member of the project with their role. Drives the project members panel and the assignee picker.",
		Tags:        []string{"Projects"},
	}, ListMembers(deps))

	huma.Register(api, huma.Operation{
		OperationID: "projects-members-add",
		Method:      http.MethodPost,
		Path:        "/projects/{prjId}/members",
		Summary:     "Add a member to a project",
		Description: "Adds an existing workspace member to the project at the requested role. Requires project admin role; the user must already belong to the parent workspace.",
		Tags:        []string{"Projects"},
	}, AddMember(deps))

	huma.Register(api, huma.Operation{
		OperationID: "projects-members-remove",
		Method:      http.MethodDelete,
		Path:        "/projects/{prjId}/members/{userId}",
		Summary:     "Remove a member from a project",
		Description: "Removes the named user from the project. Workspace membership is unaffected. Refuses to remove the last project admin.",
		Tags:        []string{"Projects"},
	}, RemoveMember(deps))
}
