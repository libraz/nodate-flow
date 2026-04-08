package inbox

import (
	"net/http"

	"github.com/danielgtaylor/huma/v2"
)

// Register wires the /inbox routes. The caller must attach RequireAuth.
func Register(api huma.API, deps Deps) {
	huma.Register(api, huma.Operation{
		OperationID: "inbox-list",
		Method:      http.MethodGet,
		Path:        "/inbox",
		Summary:     "List inbox items for the caller",
	}, List(deps))
	huma.Register(api, huma.Operation{
		OperationID: "inbox-archive",
		Method:      http.MethodPost,
		Path:        "/inbox/{id}/archive",
		Summary:     "Archive an inbox item",
	}, Archive(deps))
	huma.Register(api, huma.Operation{
		OperationID: "inbox-snooze",
		Method:      http.MethodPost,
		Path:        "/inbox/{id}/snooze",
		Summary:     "Snooze an inbox item",
	}, Snooze(deps))
}
