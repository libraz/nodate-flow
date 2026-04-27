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
		Description: "Returns the caller's inbox: signals, mentions, and assignments waiting to be triaged across every workspace they belong to. Backs the global Inbox view.",
	}, List(deps))
	huma.Register(api, huma.Operation{
		OperationID: "inbox-archive",
		Method:      http.MethodPost,
		Path:        "/inbox/{id}/archive",
		Summary:     "Archive an inbox item",
		Description: "Removes the named inbox item from the active list. Idempotent: archiving an already-archived item returns 200.",
	}, Archive(deps))
	huma.Register(api, huma.Operation{
		OperationID: "inbox-snooze",
		Method:      http.MethodPost,
		Path:        "/inbox/{id}/snooze",
		Summary:     "Snooze an inbox item",
		Description: "Hides the named inbox item until a caller-supplied wake-up time, after which it reappears in the active list.",
	}, Snooze(deps))
}
