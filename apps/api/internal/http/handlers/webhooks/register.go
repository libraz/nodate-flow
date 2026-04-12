package webhooks

import (
	"net/http"

	"github.com/danielgtaylor/huma/v2"
)

// Register wires all webhook subscription management routes into the
// workspace-scoped admin group. The caller must attach RequireAuth,
// RequireWorkspaceMember, and RequireWorkspaceRole(admin) middleware.
func Register(api huma.API, deps Deps) {
	huma.Register(api, huma.Operation{
		OperationID: "webhooks-create",
		Method:      http.MethodPost,
		Path:        "/workspaces/{wsId}/webhooks",
		Summary:     "Create a webhook subscription",
	}, Create(deps))

	huma.Register(api, huma.Operation{
		OperationID: "webhooks-list",
		Method:      http.MethodGet,
		Path:        "/workspaces/{wsId}/webhooks",
		Summary:     "List webhook subscriptions",
	}, List(deps))

	huma.Register(api, huma.Operation{
		OperationID: "webhooks-get",
		Method:      http.MethodGet,
		Path:        "/workspaces/{wsId}/webhooks/{webhookId}",
		Summary:     "Get a webhook subscription (includes secret)",
	}, Get(deps))

	huma.Register(api, huma.Operation{
		OperationID: "webhooks-delete",
		Method:      http.MethodDelete,
		Path:        "/workspaces/{wsId}/webhooks/{webhookId}",
		Summary:     "Soft-delete a webhook subscription",
	}, Delete(deps))

	huma.Register(api, huma.Operation{
		OperationID: "webhooks-toggle",
		Method:      http.MethodPatch,
		Path:        "/workspaces/{wsId}/webhooks/{webhookId}/toggle",
		Summary:     "Activate or deactivate a webhook subscription",
	}, Toggle(deps))

	huma.Register(api, huma.Operation{
		OperationID: "webhooks-deliveries-list",
		Method:      http.MethodGet,
		Path:        "/workspaces/{wsId}/webhooks/{webhookId}/deliveries",
		Summary:     "List delivery log for a webhook subscription",
	}, ListDeliveries(deps))

	huma.Register(api, huma.Operation{
		OperationID: "webhooks-test-delivery",
		Method:      http.MethodPost,
		Path:        "/workspaces/{wsId}/webhooks/{webhookId}/test",
		Summary:     "Send a test ping delivery",
	}, TestDelivery(deps))
}
