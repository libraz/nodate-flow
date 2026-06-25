package activity

import (
	"net/http"

	"github.com/danielgtaylor/huma/v2"
)

// Register wires the workspace-scoped unified activity feed route. The caller
// must attach RequireAuth + RequireWorkspaceMember middleware to the
// underlying chi router; the feed is workspace-member readable, mirroring the
// workspace timeline endpoint (the same class of workspace-wide activity read).
func Register(api huma.API, deps Deps) {
	huma.Register(api, huma.Operation{
		OperationID: "workspaces-activity-list",
		Method:      http.MethodGet,
		Path:        "/workspaces/{wsId}/activity",
		Summary:     "List the unified activity feed for a workspace",
		Description: "Returns a cursor-paginated page of the workspace activity feed: a UNION of audit log entries, AI invocations, and MCP invocations ordered newest first. Workspace-member readable; backs the workspace Activity view.",
		Tags:        []string{"Workspace"},
	}, List(deps))
}
