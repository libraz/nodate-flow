package lenses

import (
	"net/http"

	"github.com/danielgtaylor/huma/v2"
)

// RegisterWorkspaceScoped wires the workspace-scoped lens (saved view)
// CRUD routes under /workspaces/{wsId}/lenses. The caller must attach
// RequireWorkspaceMember to the underlying chi router.
func RegisterWorkspaceScoped(api huma.API, deps Deps) {
	huma.Register(api, huma.Operation{
		OperationID: "lenses-create",
		Method:      http.MethodPost,
		Path:        "/workspaces/{wsId}/lenses",
		Summary:     "Create a saved view (lens)",
		Description: "Persists a new lens (filter + grouping + sort) so the caller can re-open the same view across sessions and share it with teammates. The Lens JSON is validated against the schema before insert.",
	}, Create(deps))

	huma.Register(api, huma.Operation{
		OperationID: "lenses-list",
		Method:      http.MethodGet,
		Path:        "/workspaces/{wsId}/lenses",
		Summary:     "List saved views for a workspace/project",
		Description: "Lists lenses visible to the caller within the workspace, optionally scoped to a project. Backs the saved-views menu.",
	}, List(deps))

	huma.Register(api, huma.Operation{
		OperationID: "lenses-get",
		Method:      http.MethodGet,
		Path:        "/workspaces/{wsId}/lenses/{lensId}",
		Summary:     "Fetch a saved view",
		Description: "Returns one lens including its full Lens JSON and ownership metadata. Used when opening a saved view directly from a deep link.",
	}, Get(deps))

	huma.Register(api, huma.Operation{
		OperationID: "lenses-update",
		Method:      http.MethodPatch,
		Path:        "/workspaces/{wsId}/lenses/{lensId}",
		Summary:     "Update a saved view",
		Description: "Updates a lens's name, scope, or Lens JSON. Re-validates the JSON before persistence so an invalid update cannot wedge the view.",
	}, Update(deps))

	huma.Register(api, huma.Operation{
		OperationID: "lenses-delete",
		Method:      http.MethodDelete,
		Path:        "/workspaces/{wsId}/lenses/{lensId}",
		Summary:     "Delete a saved view",
		Description: "Removes the lens. If it is currently published the public token is also revoked so the unauthenticated mirror stops resolving.",
	}, Delete(deps))

	huma.Register(api, huma.Operation{
		OperationID: "lenses-publish",
		Method:      http.MethodPost,
		Path:        "/workspaces/{wsId}/lenses/{lensId}/publish",
		Summary:     "Publish a lens publicly with a shareable token URL",
		Description: "Mints an opaque public token for the lens and returns the canonical /public/lenses/{token} URL. Pages rendered against this token are unauthenticated.",
	}, Publish(deps))

	huma.Register(api, huma.Operation{
		OperationID: "lenses-unpublish",
		Method:      http.MethodPost,
		Path:        "/workspaces/{wsId}/lenses/{lensId}/unpublish",
		Summary:     "Revoke public access to a lens",
		Description: "Invalidates the lens's public token. Future fetches against the old URL return 404.",
	}, Unpublish(deps))
}

// RegisterPublic wires the unauthenticated public lens route. The
// caller must attach per-IP rate limiting but NOT auth middleware.
func RegisterPublic(api huma.API, deps Deps) {
	huma.Register(api, huma.Operation{
		OperationID: "lenses-get-public",
		Method:      http.MethodGet,
		Path:        "/public/lenses/{token}",
		Summary:     "Fetch a publicly shared lens (no auth required)",
		Description: "Renders the published lens with its current task results. The opaque token is the only capability; revoke via /lenses/{lensId}/unpublish stops resolving.",
	}, GetPublic(deps))
}
