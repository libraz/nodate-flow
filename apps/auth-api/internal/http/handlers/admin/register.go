package admin

import (
	"net/http"

	"github.com/danielgtaylor/huma/v2"
)

// Register registers all admin Huma operations that require the
// RequireInstanceAdmin middleware on the given API.
func Register(api huma.API, deps Deps) {
	// --- Users ---
	huma.Register(api, huma.Operation{
		OperationID: "admin-list-users",
		Method:      http.MethodGet,
		Path:        "/admin/users",
		Summary:     "List all users",
		Tags:        []string{"Admin"},
	}, ListUsers(deps))

	huma.Register(api, huma.Operation{
		OperationID: "admin-get-user",
		Method:      http.MethodGet,
		Path:        "/admin/users/{userId}",
		Summary:     "Get user details",
		Tags:        []string{"Admin"},
	}, GetUser(deps))

	huma.Register(api, huma.Operation{
		OperationID: "admin-patch-user",
		Method:      http.MethodPatch,
		Path:        "/admin/users/{userId}",
		Summary:     "Update user (suspend/enable)",
		Tags:        []string{"Admin"},
	}, PatchUser(deps))

	// --- User Sessions ---
	huma.Register(api, huma.Operation{
		OperationID: "admin-list-user-sessions",
		Method:      http.MethodGet,
		Path:        "/admin/users/{userId}/sessions",
		Summary:     "List sessions for a user",
		Tags:        []string{"Admin"},
	}, ListUserSessions(deps))

	huma.Register(api, huma.Operation{
		OperationID: "admin-revoke-session",
		Method:      http.MethodDelete,
		Path:        "/admin/sessions/{sessionId}",
		Summary:     "Revoke a session",
		Tags:        []string{"Admin"},
	}, RevokeSession(deps))

	// --- Workspaces ---
	huma.Register(api, huma.Operation{
		OperationID: "admin-list-workspaces",
		Method:      http.MethodGet,
		Path:        "/admin/workspaces",
		Summary:     "List all workspaces",
		Tags:        []string{"Admin"},
	}, ListWorkspaces(deps))

	huma.Register(api, huma.Operation{
		OperationID: "admin-get-workspace",
		Method:      http.MethodGet,
		Path:        "/admin/workspaces/{wsId}",
		Summary:     "Get workspace details",
		Tags:        []string{"Admin"},
	}, GetWorkspace(deps))

	huma.Register(api, huma.Operation{
		OperationID: "admin-patch-workspace",
		Method:      http.MethodPatch,
		Path:        "/admin/workspaces/{wsId}",
		Summary:     "Update workspace (suspend/enable)",
		Tags:        []string{"Admin"},
	}, PatchWorkspace(deps))

	// --- Instance Admins ---
	huma.Register(api, huma.Operation{
		OperationID: "admin-list-admins",
		Method:      http.MethodGet,
		Path:        "/admin/instance-admins",
		Summary:     "List instance administrators",
		Tags:        []string{"Admin"},
	}, ListAdmins(deps))

	huma.Register(api, huma.Operation{
		OperationID: "admin-grant-admin",
		Method:      http.MethodPost,
		Path:        "/admin/instance-admins",
		Summary:     "Grant instance admin privileges",
		Tags:        []string{"Admin"},
	}, GrantAdmin(deps))

	huma.Register(api, huma.Operation{
		OperationID: "admin-revoke-admin",
		Method:      http.MethodDelete,
		Path:        "/admin/instance-admins/{userId}",
		Summary:     "Revoke instance admin privileges",
		Tags:        []string{"Admin"},
	}, RevokeAdmin(deps))

	// --- Audit Logs ---
	huma.Register(api, huma.Operation{
		OperationID: "admin-list-audit-logs",
		Method:      http.MethodGet,
		Path:        "/admin/audit-logs",
		Summary:     "List instance audit logs",
		Tags:        []string{"Admin"},
	}, ListAuditLogs(deps))

	// --- Settings ---
	huma.Register(api, huma.Operation{
		OperationID: "admin-list-settings",
		Method:      http.MethodGet,
		Path:        "/admin/settings",
		Summary:     "List instance settings",
		Tags:        []string{"Admin"},
	}, ListSettings(deps))

	huma.Register(api, huma.Operation{
		OperationID: "admin-patch-settings",
		Method:      http.MethodPatch,
		Path:        "/admin/settings",
		Summary:     "Update instance settings",
		Tags:        []string{"Admin"},
	}, PatchSettings(deps))
}

// RegisterSetup registers the bootstrap endpoint on a separate API instance
// that uses RequireAuth but NOT RequireInstanceAdmin, since no admin exists
// yet at bootstrap time.
func RegisterSetup(api huma.API, deps Deps) {
	huma.Register(api, huma.Operation{
		OperationID: "admin-setup",
		Method:      http.MethodPost,
		Path:        "/admin/setup",
		Summary:     "Bootstrap the first instance admin",
		Tags:        []string{"Admin"},
	}, Setup(deps))
}
