package pages

import (
	"net/http"

	"github.com/danielgtaylor/huma/v2"
)

// RegisterWorkspaceScoped wires the workspace-scoped page CRUD, hierarchy,
// search, and AI generation routes under /workspaces/{wsId}/pages. The caller
// must attach RequireWorkspaceMember to the underlying chi router.
func RegisterWorkspaceScoped(api huma.API, deps Deps) {
	huma.Register(api, huma.Operation{
		OperationID: "pages-create",
		Method:      http.MethodPost,
		Path:        "/workspaces/{wsId}/pages",
		Summary:     "Create a page",
	}, Create(deps))

	huma.Register(api, huma.Operation{
		OperationID: "pages-list",
		Method:      http.MethodGet,
		Path:        "/workspaces/{wsId}/pages",
		Summary:     "List root pages in a workspace",
	}, List(deps))

	huma.Register(api, huma.Operation{
		OperationID: "pages-search",
		Method:      http.MethodGet,
		Path:        "/workspaces/{wsId}/pages/search",
		Summary:     "Search pages by title",
	}, Search(deps))

	huma.Register(api, huma.Operation{
		OperationID: "pages-generate",
		Method:      http.MethodPost,
		Path:        "/workspaces/{wsId}/pages/generate",
		Summary:     "Generate a page with AI",
	}, GenerateWithAI(deps))

	huma.Register(api, huma.Operation{
		OperationID: "pages-get",
		Method:      http.MethodGet,
		Path:        "/workspaces/{wsId}/pages/{pageId}",
		Summary:     "Fetch a page by id",
	}, Get(deps))

	huma.Register(api, huma.Operation{
		OperationID: "pages-update",
		Method:      http.MethodPatch,
		Path:        "/workspaces/{wsId}/pages/{pageId}",
		Summary:     "Update a page",
	}, Update(deps))

	huma.Register(api, huma.Operation{
		OperationID: "pages-delete",
		Method:      http.MethodDelete,
		Path:        "/workspaces/{wsId}/pages/{pageId}",
		Summary:     "Soft-delete a page",
	}, Delete(deps))

	huma.Register(api, huma.Operation{
		OperationID: "pages-list-children",
		Method:      http.MethodGet,
		Path:        "/workspaces/{wsId}/pages/{pageId}/children",
		Summary:     "List child pages",
	}, ListChildren(deps))
}
