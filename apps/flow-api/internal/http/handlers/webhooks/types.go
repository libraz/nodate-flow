// Package webhooks contains Huma operation handlers for webhook
// subscription management and delivery log inspection.
package webhooks

import (
	"database/sql"
	"encoding/json"

	"github.com/danielgtaylor/huma/v2"

	"github.com/nodate-flow/nodate-flow/apps/flow-api/internal/audit"
	"github.com/nodate-flow/nodate-flow/apps/flow-api/internal/db/generated"
	apierrors "github.com/nodate-flow/nodate-flow/apps/flow-api/internal/errors"
)

// Deps is the dependency bundle for handlers in this package.
type Deps struct {
	DB      *sql.DB
	Queries *generated.Queries
	Audit   *audit.Recorder
}

func httpErr(spec *apierrors.Spec) error {
	return huma.NewError(spec.Status, spec.Code+": "+spec.Message)
}

func nullTimeUnix(t sql.NullTime) *int64 {
	if !t.Valid {
		return nil
	}
	v := t.Time.Unix()
	return &v
}

func totalAsInt64(v interface{}) int64 {
	switch x := v.(type) {
	case int64:
		return x
	case int:
		return int64(x)
	case uint64:
		return int64(x)
	case []byte:
		var n int64
		for _, c := range x {
			if c < '0' || c > '9' {
				return n
			}
			n = n*10 + int64(c-'0')
		}
		return n
	}
	return 0
}

// --- DTOs ---

// WebhookSubscriptionDTO is the public DTO for a webhook subscription.
type WebhookSubscriptionDTO struct {
	ID                 string          `json:"id"`
	URL                string          `json:"url"`
	Description        string          `json:"description"`
	EventTypes         json.RawMessage `json:"eventTypes"`
	IsActive           bool            `json:"isActive"`
	CreatorID          string          `json:"creatorId,omitempty"`
	CreatorDisplayName string          `json:"creatorDisplayName,omitempty"`
	CreatedAt          int64           `json:"createdAt"`
	UpdatedAt          *int64          `json:"updatedAt"`
}

// WebhookSubscriptionDetailDTO extends the subscription with the secret
// field. Returned only on Create and Get (for the creator).
type WebhookSubscriptionDetailDTO struct {
	WebhookSubscriptionDTO
	Secret string `json:"secret"`
}

// WebhookDeliveryDTO is the public DTO for a webhook delivery log entry.
type WebhookDeliveryDTO struct {
	ID          string `json:"id"`
	EventType   string `json:"eventType"`
	Status      string `json:"status"`
	HttpStatus  *int16 `json:"httpStatus"`
	Attempts    uint8  `json:"attempts"`
	MaxAttempts uint8  `json:"maxAttempts"`
	DeliveredAt *int64 `json:"deliveredAt"`
	FailedAt    *int64 `json:"failedAt"`
	CreatedAt   int64  `json:"createdAt"`
}

// --- Create ---

// CreateInput is the request for POST /workspaces/{wsId}/webhooks.
type CreateInput struct {
	WsID string `path:"wsId" doc:"Workspace public id (UUID v7)"`
	Body struct {
		URL         string          `json:"url" required:"true" doc:"Target URL for webhook delivery"`
		Description string          `json:"description" doc:"Human-readable description"`
		EventTypes  json.RawMessage `json:"eventTypes" required:"true" doc:"JSON array of event type patterns"`
	}
}

// CreateOutputBody is the response body for POST /workspaces/{wsId}/webhooks.
type CreateOutputBody struct {
	Webhook WebhookSubscriptionDetailDTO `json:"webhook"`
}

// CreateOutput is the response for POST /workspaces/{wsId}/webhooks.
type CreateOutput struct {
	Body CreateOutputBody
}

// --- List ---

// ListInput is the query for GET /workspaces/{wsId}/webhooks.
type ListInput struct {
	WsID   string `path:"wsId" doc:"Workspace public id (UUID v7)"`
	Limit  int32  `query:"limit" minimum:"1" maximum:"200" default:"50"`
	Offset int32  `query:"offset" minimum:"0" default:"0"`
}

// ListOutputBody is the response body for GET /workspaces/{wsId}/webhooks.
type ListOutputBody struct {
	Total      int64                    `json:"total"`
	Webhooks   []WebhookSubscriptionDTO `json:"webhooks"`
	NextCursor *string                  `json:"nextCursor"`
}

// ListOutput is the response for GET /workspaces/{wsId}/webhooks.
type ListOutput struct {
	Body ListOutputBody
}

// --- Get ---

// GetInput is the path for GET /workspaces/{wsId}/webhooks/{webhookId}.
type GetInput struct {
	WsID      string `path:"wsId" doc:"Workspace public id (UUID v7)"`
	WebhookID string `path:"webhookId" doc:"Webhook subscription public id (UUID v7)"`
}

// GetOutputBody is the response body for GET /workspaces/{wsId}/webhooks/{webhookId}.
type GetOutputBody struct {
	Webhook WebhookSubscriptionDetailDTO `json:"webhook"`
}

// GetOutput is the response for GET /workspaces/{wsId}/webhooks/{webhookId}.
type GetOutput struct {
	Body GetOutputBody
}

// --- Delete ---

// DeleteInput is the path for DELETE /workspaces/{wsId}/webhooks/{webhookId}.
type DeleteInput struct {
	WsID      string `path:"wsId" doc:"Workspace public id (UUID v7)"`
	WebhookID string `path:"webhookId" doc:"Webhook subscription public id (UUID v7)"`
}

// DeleteOutputBody is the response body for DELETE /workspaces/{wsId}/webhooks/{webhookId}.
type DeleteOutputBody struct {
	Ok bool `json:"ok"`
}

// DeleteOutput is the response for DELETE /workspaces/{wsId}/webhooks/{webhookId}.
type DeleteOutput struct {
	Body DeleteOutputBody
}

// --- Toggle ---

// ToggleInput is the request for PATCH /workspaces/{wsId}/webhooks/{webhookId}/toggle.
type ToggleInput struct {
	WsID      string `path:"wsId" doc:"Workspace public id (UUID v7)"`
	WebhookID string `path:"webhookId" doc:"Webhook subscription public id (UUID v7)"`
	Body      struct {
		IsActive bool `json:"isActive" required:"true" doc:"Desired active state"`
	}
}

// ToggleOutputBody is the response body for PATCH /workspaces/{wsId}/webhooks/{webhookId}/toggle.
type ToggleOutputBody struct {
	Ok bool `json:"ok"`
}

// ToggleOutput is the response for PATCH /workspaces/{wsId}/webhooks/{webhookId}/toggle.
type ToggleOutput struct {
	Body ToggleOutputBody
}

// --- ListDeliveries ---

// ListDeliveriesInput is the query for GET /workspaces/{wsId}/webhooks/{webhookId}/deliveries.
type ListDeliveriesInput struct {
	WsID      string `path:"wsId" doc:"Workspace public id (UUID v7)"`
	WebhookID string `path:"webhookId" doc:"Webhook subscription public id (UUID v7)"`
	Limit     int32  `query:"limit" minimum:"1" maximum:"200" default:"50"`
	Offset    int32  `query:"offset" minimum:"0" default:"0"`
}

// ListDeliveriesOutputBody is the response body for GET /workspaces/{wsId}/webhooks/{webhookId}/deliveries.
type ListDeliveriesOutputBody struct {
	Total      int64                `json:"total"`
	Deliveries []WebhookDeliveryDTO `json:"deliveries"`
	NextCursor *string              `json:"nextCursor"`
}

// ListDeliveriesOutput is the response for GET /workspaces/{wsId}/webhooks/{webhookId}/deliveries.
type ListDeliveriesOutput struct {
	Body ListDeliveriesOutputBody
}

// --- TestDelivery ---

// TestDeliveryInput is the path for POST /workspaces/{wsId}/webhooks/{webhookId}/test.
type TestDeliveryInput struct {
	WsID      string `path:"wsId" doc:"Workspace public id (UUID v7)"`
	WebhookID string `path:"webhookId" doc:"Webhook subscription public id (UUID v7)"`
}

// TestDeliveryOutputBody is the response body for POST /workspaces/{wsId}/webhooks/{webhookId}/test.
type TestDeliveryOutputBody struct {
	DeliveryID string `json:"deliveryId"`
}

// TestDeliveryOutput is the response for POST /workspaces/{wsId}/webhooks/{webhookId}/test.
type TestDeliveryOutput struct {
	Body TestDeliveryOutputBody
}
