package favorites

import (
	"net/http"

	"github.com/danielgtaylor/huma/v2"
)

// Register wires the user-scoped /me/favorites routes. The caller
// must attach RequireAuth middleware to the underlying chi router.
func Register(api huma.API, deps Deps) {
	huma.Register(api, huma.Operation{
		OperationID: "favorites-list",
		Method:      http.MethodGet,
		Path:        "/me/favorites",
		Summary:     "List the caller's favorites",
	}, List(deps))

	huma.Register(api, huma.Operation{
		OperationID: "favorites-create",
		Method:      http.MethodPost,
		Path:        "/me/favorites",
		Summary:     "Add an item to favorites",
	}, Create(deps))

	huma.Register(api, huma.Operation{
		OperationID: "favorites-delete",
		Method:      http.MethodDelete,
		Path:        "/me/favorites/{id}",
		Summary:     "Remove an item from favorites",
	}, Delete(deps))
}
