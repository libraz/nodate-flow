package integrationmappings

import (
	"net/http"

	"github.com/danielgtaylor/huma/v2"
)

// Register wires the workspace-scoped integration source mapping routes
// under /workspaces/{wsId}/integration-mappings. The caller must attach
// RequireWorkspaceMember plus the workspace-admin floor: a mapping
// decides which tenant receives a repository's or Slack team's events,
// so creating one is an administrative act.
func Register(api huma.API, deps Deps) {
	huma.Register(api, huma.Operation{
		OperationID: "integration-mappings-list",
		Method:      http.MethodGet,
		Path:        "/workspaces/{wsId}/integration-mappings",
		Summary:     "List inbound webhook source mappings",
		Description: "Returns every external source (GitHub repository, Slack team, Google push channel) whose inbound webhook deliveries are routed to this workspace, including paused ones.",
		Tags:        []string{"Integration"},
	}, List(deps))

	huma.Register(api, huma.Operation{
		OperationID: "integration-mappings-create",
		Method:      http.MethodPost,
		Path:        "/workspaces/{wsId}/integration-mappings",
		Summary:     "Route an external webhook source to this workspace",
		Description: "Claims an external source so its inbound webhook deliveries are filed in this workspace. A source belongs to exactly one workspace instance-wide; claiming one that is already mapped fails with INTEGRATION.MAPPING.SOURCE_ALREADY_MAPPED.",
		Tags:        []string{"Integration"},
	}, Create(deps))

	huma.Register(api, huma.Operation{
		OperationID: "integration-mappings-patch",
		Method:      http.MethodPatch,
		Path:        "/workspaces/{wsId}/integration-mappings/{id}",
		Summary:     "Rename or pause a webhook source mapping",
		Description: "Updates the display label, or sets enabled=false to stop routing deliveries from the source while keeping the claim on it. The provider and external key are immutable.",
		Tags:        []string{"Integration"},
	}, Patch(deps))

	huma.Register(api, huma.Operation{
		OperationID: "integration-mappings-delete",
		Method:      http.MethodDelete,
		Path:        "/workspaces/{wsId}/integration-mappings/{id}",
		Summary:     "Release a webhook source mapping",
		Description: "Removes the mapping and releases the claim on the external source so another workspace may map it. Deliveries from that source stop being accepted immediately.",
		Tags:        []string{"Integration"},
	}, Delete(deps))
}
