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
		Description: "Lists every user in the instance with status, role, and last-seen timestamp. Backs the admin Users panel; supports filtering by status and search by email or display name.",
		Tags:        []string{"Admin"},
	}, ListUsers(deps))

	huma.Register(api, huma.Operation{
		OperationID: "admin-get-user",
		Method:      http.MethodGet,
		Path:        "/admin/users/{userId}",
		Summary:     "Get user details",
		Description: "Returns the full user row plus the workspaces they belong to and their session count. Used by the admin user-detail page.",
		Tags:        []string{"Admin"},
	}, GetUser(deps))

	huma.Register(api, huma.Operation{
		OperationID: "admin-patch-user",
		Method:      http.MethodPatch,
		Path:        "/admin/users/{userId}",
		Summary:     "Update user (suspend/enable)",
		Description: "Changes administrative user state: suspends or re-enables the account. Suspending revokes all active sessions for the user.",
		Tags:        []string{"Admin"},
	}, PatchUser(deps))

	// --- User Sessions ---
	huma.Register(api, huma.Operation{
		OperationID: "admin-list-user-sessions",
		Method:      http.MethodGet,
		Path:        "/admin/users/{userId}/sessions",
		Summary:     "List sessions for a user",
		Description: "Lists every active session for the named user (device, IP, last-seen). Used by admins to investigate suspicious activity and to revoke sessions one by one.",
		Tags:        []string{"Admin"},
	}, ListUserSessions(deps))

	huma.Register(api, huma.Operation{
		OperationID: "admin-revoke-session",
		Method:      http.MethodDelete,
		Path:        "/admin/sessions/{sessionId}",
		Summary:     "Revoke a session",
		Description: "Revokes the named session immediately. Outstanding access tokens keep working until they expire; refresh attempts will fail.",
		Tags:        []string{"Admin"},
	}, RevokeSession(deps))

	// --- Workspaces ---
	huma.Register(api, huma.Operation{
		OperationID: "admin-list-workspaces",
		Method:      http.MethodGet,
		Path:        "/admin/workspaces",
		Summary:     "List all workspaces",
		Description: "Lists every workspace on the instance with member count, plan, and status. Backs the admin Workspaces panel.",
		Tags:        []string{"Admin"},
	}, ListWorkspaces(deps))

	huma.Register(api, huma.Operation{
		OperationID: "admin-get-workspace",
		Method:      http.MethodGet,
		Path:        "/admin/workspaces/{wsId}",
		Summary:     "Get workspace details",
		Description: "Returns the workspace metadata along with administrative counts (members, owners, projects) for the admin workspace-detail page.",
		Tags:        []string{"Admin"},
	}, GetWorkspace(deps))

	huma.Register(api, huma.Operation{
		OperationID: "admin-patch-workspace",
		Method:      http.MethodPatch,
		Path:        "/admin/workspaces/{wsId}",
		Summary:     "Update workspace (suspend/enable)",
		Description: "Changes administrative workspace state: suspends or re-enables the workspace. Suspended workspaces are hidden from the switcher and reject API calls.",
		Tags:        []string{"Admin"},
	}, PatchWorkspace(deps))

	huma.Register(api, huma.Operation{
		OperationID: "admin-delete-workspace",
		Method:      http.MethodDelete,
		Path:        "/admin/workspaces/{wsId}",
		Summary:     "Delete a workspace immediately",
		Description: "Destructive immediate delete. Sweeps every MinIO blob owned by the workspace, then issues a CASCADE-anchored hard DELETE on the workspaces row. Requires `confirm: true` in the request body, returns 400 WORKSPACE.DELETE.CONFIRM_REQUIRED otherwise. Idempotent: an already-deleted workspace returns 200 with deleted=false. Suspension (PATCH with enabled=false) is a separate, reversible operation and is NOT a precondition.",
		Tags:        []string{"Admin"},
	}, DeleteWorkspace(deps))

	huma.Register(api, huma.Operation{
		OperationID: "admin-delete-user",
		Method:      http.MethodDelete,
		Path:        "/admin/users/{userId}",
		Summary:     "Delete a user immediately",
		Description: "Destructive immediate delete. Reconciles ref_counts on every storage_objects row referenced by the user (task / calendar attachments uploaded by them across any workspace, plus avatar storage objects owned directly by them), hard-deletes the user (CASCADE clears attachment + avatar SO rows), then sweeps any orphaned MinIO blobs. Requires `confirm: true` in the request body, returns 400 USER.DELETE.CONFIRM_REQUIRED otherwise. Rejects self-delete with USER.DELETE.SELF_NOT_ALLOWED to prevent admins locking themselves out. Suspension (PATCH with enabled=false) is a separate operation and is NOT a precondition.",
		Tags:        []string{"Admin"},
	}, DeleteUser(deps))

	// --- Instance Admins ---
	huma.Register(api, huma.Operation{
		OperationID: "admin-list-admins",
		Method:      http.MethodGet,
		Path:        "/admin/instance-admins",
		Summary:     "List instance administrators",
		Description: "Lists every user with the instance-admin role so the admin UI can show who has top-level privileges.",
		Tags:        []string{"Admin"},
	}, ListAdmins(deps))

	huma.Register(api, huma.Operation{
		OperationID: "admin-grant-admin",
		Method:      http.MethodPost,
		Path:        "/admin/instance-admins",
		Summary:     "Grant instance admin privileges",
		Description: "Marks the supplied user as an instance admin. Idempotent: repeated grants are no-ops. Logged to the audit trail.",
		Tags:        []string{"Admin"},
	}, GrantAdmin(deps))

	huma.Register(api, huma.Operation{
		OperationID: "admin-revoke-admin",
		Method:      http.MethodDelete,
		Path:        "/admin/instance-admins/{userId}",
		Summary:     "Revoke instance admin privileges",
		Description: "Removes the instance-admin role from the named user. Refuses to remove the last remaining instance admin to keep the instance manageable.",
		Tags:        []string{"Admin"},
	}, RevokeAdmin(deps))

	// --- Audit Logs ---
	huma.Register(api, huma.Operation{
		OperationID: "admin-list-audit-logs",
		Method:      http.MethodGet,
		Path:        "/admin/audit-logs",
		Summary:     "List instance audit logs",
		Description: "Returns a cursor-paginated page of instance-level audit log entries with filters by actor, action, and time range. Backs the admin Audit panel.",
		Tags:        []string{"Admin"},
	}, ListAuditLogs(deps))

	// --- Instance Stats ---
	huma.Register(api, huma.Operation{
		OperationID: "admin-instance-stats",
		Method:      http.MethodGet,
		Path:        "/admin/instance-stats",
		Summary:     "Get instance-level statistics",
		Description: "Returns aggregate counts (users, workspaces, sessions, tasks) for the admin overview page. Computed on the fly; safe to call frequently.",
		Tags:        []string{"Admin"},
	}, InstanceStats(deps))

	// --- Settings ---
	huma.Register(api, huma.Operation{
		OperationID: "admin-list-settings",
		Method:      http.MethodGet,
		Path:        "/admin/settings",
		Summary:     "List instance settings",
		Description: "Returns every mutable instance-level setting (registration toggles, password policy, etc.) for the admin Settings panel.",
		Tags:        []string{"Admin"},
	}, ListSettings(deps))

	huma.Register(api, huma.Operation{
		OperationID: "admin-patch-settings",
		Method:      http.MethodPatch,
		Path:        "/admin/settings",
		Summary:     "Update instance settings",
		Description: "Patches one or more instance-level settings. Each settings change is recorded in the audit log and takes effect immediately for new requests.",
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
		Description: "One-shot bootstrap that promotes the calling user to instance admin when no admin exists yet. Refuses with ADMIN.SETUP.ALREADY_BOOTSTRAPPED once the first admin is in place.",
		Tags:        []string{"Admin"},
	}, Setup(deps))
}
