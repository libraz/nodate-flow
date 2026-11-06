package inbox

import (
	"net/http"

	"github.com/danielgtaylor/huma/v2"
)

// Register wires the /inbox routes. The caller must attach RequireAuth.
func Register(api huma.API, deps Deps) {
	huma.Register(api, huma.Operation{
		OperationID:   "inbox-capture-intake",
		Method:        http.MethodPost,
		Path:          "/workspaces/{wsId}/inbox/{signalId}/intake",
		Summary:       "Capture an inbox source as intake",
		Description:   "Creates or returns the workspace's unique intake item for an inbox signal. The source preview is derived from the authenticated signal payload; callers never submit replacement source data.",
		DefaultStatus: http.StatusCreated,
		Tags:          []string{"Tasks"},
	}, CaptureAsIntake(deps))

	huma.Register(api, huma.Operation{
		OperationID: "inbox-list",
		Method:      http.MethodGet,
		Path:        "/inbox",
		Summary:     "List inbox items for the caller",
		Description: "Returns the caller's inbox: signals, mentions, and assignments waiting to be triaged across every workspace they belong to. Backs the global Inbox view.",
		Tags:        []string{"Tasks"},
	}, List(deps))
	huma.Register(api, huma.Operation{
		OperationID: "inbox-archive",
		Method:      http.MethodPost,
		Path:        "/inbox/{id}/archive",
		Summary:     "Archive an inbox item",
		Description: "Removes the named inbox item from the active list. Returns 404 WS.INBOX.NOT_FOUND when the item is already archived or does not exist.",
		Tags:        []string{"Tasks"},
	}, Archive(deps))
	huma.Register(api, huma.Operation{
		OperationID: "inbox-snooze",
		Method:      http.MethodPost,
		Path:        "/inbox/{id}/snooze",
		Summary:     "Snooze an inbox item",
		Description: "Hides the named inbox item until a caller-supplied wake-up time, after which it reappears in the active list.",
		Tags:        []string{"Tasks"},
	}, Snooze(deps))
}
