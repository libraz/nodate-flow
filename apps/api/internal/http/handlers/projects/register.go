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
	}, Create(deps))

	huma.Register(api, huma.Operation{
		OperationID: "projects-list",
		Method:      http.MethodGet,
		Path:        "/workspaces/{wsId}/projects",
		Summary:     "List projects in a workspace",
	}, List(deps))
}

// RegisterGlobal wires the global project routes (under /projects/{prjId}).
// The caller must attach RequireProjectMemberByGlobalId to the underlying
// chi router.
func RegisterGlobal(api huma.API, deps Deps) {
	huma.Register(api, huma.Operation{
		OperationID: "projects-get",
		Method:      http.MethodGet,
		Path:        "/projects/{prjId}",
		Summary:     "Fetch a project",
	}, Get(deps))

	huma.Register(api, huma.Operation{
		OperationID: "projects-patch",
		Method:      http.MethodPatch,
		Path:        "/projects/{prjId}",
		Summary:     "Patch a project",
	}, Patch(deps))

	huma.Register(api, huma.Operation{
		OperationID: "projects-disable",
		Method:      http.MethodDelete,
		Path:        "/projects/{prjId}",
		Summary:     "Soft-disable a project",
	}, Disable(deps))

	huma.Register(api, huma.Operation{
		OperationID: "projects-dependencies-list",
		Method:      http.MethodGet,
		Path:        "/projects/{prjId}/dependencies",
		Summary:     "List every task dependency edge within a project",
	}, ListDependencies(deps))

	huma.Register(api, huma.Operation{
		OperationID: "projects-members-list",
		Method:      http.MethodGet,
		Path:        "/projects/{prjId}/members",
		Summary:     "List members of a project",
	}, ListMembers(deps))

	huma.Register(api, huma.Operation{
		OperationID: "projects-members-add",
		Method:      http.MethodPost,
		Path:        "/projects/{prjId}/members",
		Summary:     "Add a member to a project",
	}, AddMember(deps))

	huma.Register(api, huma.Operation{
		OperationID: "projects-members-remove",
		Method:      http.MethodDelete,
		Path:        "/projects/{prjId}/members/{userId}",
		Summary:     "Remove a member from a project",
	}, RemoveMember(deps))
}
