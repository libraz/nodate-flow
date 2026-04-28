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
		Description: "Registers an outbound webhook subscription for the workspace. Returns the freshly minted HMAC signing secret in the response body — this is the only time it is returned plaintext.",
		Tags:        []string{"Webhook"},
	}, Create(deps))

	huma.Register(api, huma.Operation{
		OperationID: "webhooks-list",
		Method:      http.MethodGet,
		Path:        "/workspaces/{wsId}/webhooks",
		Summary:     "List webhook subscriptions",
		Description: "Lists every webhook subscription in the workspace with its target URL, event filters, and active state. Signing secrets are masked.",
		Tags:        []string{"Webhook"},
	}, List(deps))

	huma.Register(api, huma.Operation{
		OperationID: "webhooks-get",
		Method:      http.MethodGet,
		Path:        "/workspaces/{wsId}/webhooks/{webhookId}",
		Summary:     "Get a webhook subscription (includes secret)",
		Description: "Returns the named webhook subscription including its plaintext signing secret so the operator can rotate it. Restricted to workspace admins.",
		Tags:        []string{"Webhook"},
	}, Get(deps))

	huma.Register(api, huma.Operation{
		OperationID: "webhooks-delete",
		Method:      http.MethodDelete,
		Path:        "/workspaces/{wsId}/webhooks/{webhookId}",
		Summary:     "Soft-delete a webhook subscription",
		Description: "Marks the subscription as removed so no further deliveries are attempted. Delivery history remains queryable for audit.",
		Tags:        []string{"Webhook"},
	}, Delete(deps))

	huma.Register(api, huma.Operation{
		OperationID: "webhooks-toggle",
		Method:      http.MethodPatch,
		Path:        "/workspaces/{wsId}/webhooks/{webhookId}/toggle",
		Summary:     "Activate or deactivate a webhook subscription",
		Description: "Flips the subscription's active flag. Inactive subscriptions stay registered but skip delivery — useful for pausing during a downstream outage.",
		Tags:        []string{"Webhook"},
	}, Toggle(deps))

	huma.Register(api, huma.Operation{
		OperationID: "webhooks-deliveries-list",
		Method:      http.MethodGet,
		Path:        "/workspaces/{wsId}/webhooks/{webhookId}/deliveries",
		Summary:     "List delivery log for a webhook subscription",
		Description: "Returns recent delivery attempts for the subscription with status code, latency, error reason, and the redacted payload preview. Used by the webhook detail panel for diagnostics.",
		Tags:        []string{"Webhook"},
	}, ListDeliveries(deps))

	huma.Register(api, huma.Operation{
		OperationID: "webhooks-test-delivery",
		Method:      http.MethodPost,
		Path:        "/workspaces/{wsId}/webhooks/{webhookId}/test",
		Summary:     "Send a test ping delivery",
		Description: "Synchronously sends a test ping payload signed with the subscription's secret so the operator can verify reachability and signature handling without waiting for a real event.",
		Tags:        []string{"Webhook"},
	}, TestDelivery(deps))
}
