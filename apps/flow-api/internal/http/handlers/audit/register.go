package audit

import (
	"net/http"

	"github.com/danielgtaylor/huma/v2"
)

// Register wires the workspace-scoped audit log read routes. The caller
// must attach RequireAuth + RequireWorkspaceMember +
// RequireWorkspaceRole(WorkspaceRoleAdmin) middleware to the underlying
// chi router; the audit trail is intentionally gated to workspace
// administrators only.
func Register(api huma.API, deps Deps) {
	huma.Register(api, huma.Operation{
		OperationID: "audit-logs-list",
		Method:      http.MethodGet,
		Path:        "/workspaces/{wsId}/audit-logs",
		Summary:     "List recent audit log entries for a workspace",
		Description: "Returns a cursor-paginated page of workspace audit log entries (actor, action, target, timestamp). Restricted to workspace admins; backs the admin Audit panel.",
		Tags:        []string{"Admin"},
	}, List(deps))
}
