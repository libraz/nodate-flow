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
	}, List(deps))
	huma.Register(api, huma.Operation{
		OperationID: "notifications-unread-count",
		Method:      http.MethodGet,
		Path:        "/me/notifications/unread-count",
		Summary:     "Count unread notifications for the caller",
	}, CountUnread(deps))
	huma.Register(api, huma.Operation{
		OperationID: "notifications-mark-read",
		Method:      http.MethodPost,
		Path:        "/notifications/{notifId}/read",
		Summary:     "Mark a notification as read",
	}, MarkRead(deps))
	huma.Register(api, huma.Operation{
		OperationID: "notifications-archive",
		Method:      http.MethodPost,
		Path:        "/notifications/{notifId}/archive",
		Summary:     "Archive a notification",
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
	}, MarkAllRead(deps))
}
