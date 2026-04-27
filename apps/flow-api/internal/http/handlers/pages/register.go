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
		Description: "Creates a new page (optionally as a child of an existing page) with title and Markdown body. Returns the persisted page including its assigned id.",
	}, Create(deps))

	huma.Register(api, huma.Operation{
		OperationID: "pages-list",
		Method:      http.MethodGet,
		Path:        "/workspaces/{wsId}/pages",
		Summary:     "List root pages in a workspace",
		Description: "Returns top-level pages (no parent) in the workspace, sorted alphabetically. Used to render the page tree's first level.",
	}, List(deps))

	huma.Register(api, huma.Operation{
		OperationID: "pages-search",
		Method:      http.MethodGet,
		Path:        "/workspaces/{wsId}/pages/search",
		Summary:     "Search pages by title",
		Description: "Substring-matches page titles in the workspace. Backs the page picker and command palette.",
	}, Search(deps))

	huma.Register(api, huma.Operation{
		OperationID: "pages-generate",
		Method:      http.MethodPost,
		Path:        "/workspaces/{wsId}/pages/generate",
		Summary:     "Generate a page with AI",
		Description: "Asks the workspace's configured LLM to draft a page from the supplied prompt and persists the result. Records an ai_invocations row.",
	}, GenerateWithAI(deps))

	huma.Register(api, huma.Operation{
		OperationID: "pages-get",
		Method:      http.MethodGet,
		Path:        "/workspaces/{wsId}/pages/{pageId}",
		Summary:     "Fetch a page by id",
		Description: "Returns a single page with its full Markdown body and metadata. Used when opening a page from the tree.",
	}, Get(deps))

	huma.Register(api, huma.Operation{
		OperationID: "pages-update",
		Method:      http.MethodPatch,
		Path:        "/workspaces/{wsId}/pages/{pageId}",
		Summary:     "Update a page",
		Description: "Updates page title and Markdown body. Optionally re-parents the page within the tree.",
	}, Update(deps))

	huma.Register(api, huma.Operation{
		OperationID: "pages-delete",
		Method:      http.MethodDelete,
		Path:        "/workspaces/{wsId}/pages/{pageId}",
		Summary:     "Soft-delete a page",
		Description: "Marks the page as deleted so it disappears from the tree without losing content. Idempotent.",
	}, Delete(deps))

	huma.Register(api, huma.Operation{
		OperationID: "pages-list-children",
		Method:      http.MethodGet,
		Path:        "/workspaces/{wsId}/pages/{pageId}/children",
		Summary:     "List child pages",
		Description: "Returns the immediate children of the named page so the tree can be expanded one level at a time.",
	}, ListChildren(deps))
}
