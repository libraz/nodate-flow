package notifications

import (
	"net/http"

	"github.com/danielgtaylor/huma/v2"
)

// Register wires the user-scoped /me/notifications routes. The caller
// must attach RequireAuth middleware.
func Register(api huma.API, deps Deps) {
	huma.Register(api, huma.Operation{
		OperationID: "notifications-list",
		Method:      http.MethodGet,
		Path:        "/me/notifications",
		Summary:     "List notifications for the caller",
		Description: "Returns a cursor-paginated page of the caller's notifications across every workspace they belong to. Includes unread, read, and archived states; filterable by status.",
	}, List(deps))
	huma.Register(api, huma.Operation{
		OperationID: "notifications-unread-count",
		Method:      http.MethodGet,
		Path:        "/me/notifications/unread-count",
		Summary:     "Count unread notifications for the caller",
		Description: "Returns the number of unread notifications across every workspace. Cheap; safe to poll for the bell badge.",
	}, CountUnread(deps))
	huma.Register(api, huma.Operation{
		OperationID: "notifications-mark-read",
		Method:      http.MethodPost,
		Path:        "/notifications/{notifId}/read",
		Summary:     "Mark a notification as read",
		Description: "Flips the named notification to read. Idempotent: marking an already-read notification returns 200.",
	}, MarkRead(deps))
	huma.Register(api, huma.Operation{
		OperationID: "notifications-archive",
		Method:      http.MethodPost,
		Path:        "/notifications/{notifId}/archive",
		Summary:     "Archive a notification",
		Description: "Removes the notification from the active inbox. Archived notifications stay queryable via the list endpoint with the appropriate status filter.",
	}, Archive(deps))
}

// RegisterWorkspaceScoped wires the workspace-scoped notification routes.
// The caller must attach RequireAuth + RequireWorkspaceMember middleware.
func RegisterWorkspaceScoped(api huma.API, deps Deps) {
	huma.Register(api, huma.Operation{
		OperationID: "notifications-mark-all-read",
		Method:      http.MethodPost,
		Path:        "/workspaces/{wsId}/notifications/read-all",
		Summary:     "Mark all notifications as read in a workspace",
		Description: "Bulk-marks every unread notification for the caller within the workspace as read. Useful for the 'mark all read' affordance in the notifications panel.",
	}, MarkAllRead(deps))
}
