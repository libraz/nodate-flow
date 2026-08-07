package webhooks

import (
	"context"
	"database/sql"
	"encoding/json"
	"time"

	"github.com/libraz/nodate-flow/apps/flow-api/internal/audit"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/generated"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/types"
	apierrors "github.com/libraz/nodate-flow/apps/flow-api/internal/errors"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/http/handlers/handlerutil"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/http/middleware"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/webhook"
	"github.com/libraz/nodate-flow/packages/go-shared/apierr"
)

// Create handles POST /workspaces/{wsId}/webhooks. It creates a new
// webhook subscription with an auto-generated HMAC secret.
func Create(deps Deps) func(context.Context, *CreateInput) (*CreateOutput, error) {
	return func(ctx context.Context, in *CreateInput) (*CreateOutput, error) {
		actorID, ok := middleware.ActorFromContext(ctx)
		if !ok {
			return nil, httpErr(apierrors.WsWorkspaceAccessDenied)
		}
		wsCtx, ok := middleware.WorkspaceFromContext(ctx)
		if !ok {
			return nil, httpErr(apierrors.WsWorkspaceNotFound)
		}

		// SSRF guard: only public https destinations may be registered.
		if err := webhook.ValidateURL(ctx, in.Body.URL); err != nil {
			return nil, httpErr(apierrors.WebhookSubscriptionUrlInvalid)
		}

		secret, err := webhook.GenerateSecret()
		if err != nil {
			return nil, httpErr(apierrors.InternalUnexpected)
		}

		eventTypes := in.Body.EventTypes
		if len(eventTypes) == 0 {
			eventTypes = json.RawMessage(`["*"]`)
		}

		pubID := types.New()
		if _, err := deps.Queries.CreateWebhookSubscription(ctx, generated.CreateWebhookSubscriptionParams{
			PublicID:    pubID,
			WorkspaceID: wsCtx.ID,
			CreatorID:   actorID,
			Url:         in.Body.URL,
			Secret:      secret,
			Description: in.Body.Description,
			EventTypes:  eventTypes,
		}); err != nil {
			return nil, httpErr(apierrors.InternalUnexpected)
		}

		deps.Audit.Record(ctx, audit.Entry{
			Action:       "webhook.subscription.create",
			ActorID:      actorID,
			WorkspaceID:  wsCtx.ID,
			ResourceType: "webhook_subscription",
			ResourceID:   pubID.String(),
		})

		now := handlerutil.NowUnix()
		out := &CreateOutput{}
		out.Body.Webhook = WebhookSubscriptionDetailDTO{
			WebhookSubscriptionDTO: WebhookSubscriptionDTO{
				ID:          pubID.String(),
				URL:         in.Body.URL,
				Description: in.Body.Description,
				EventTypes:  eventTypes,
				IsActive:    true,
				CreatedAt:   now,
			},
			Secret: secret,
		}
		return out, nil
	}
}

// List handles GET /workspaces/{wsId}/webhooks.
func List(deps Deps) func(context.Context, *ListInput) (*ListOutput, error) {
	return func(ctx context.Context, in *ListInput) (*ListOutput, error) {
		_, ok := middleware.ActorFromContext(ctx)
		if !ok {
			return nil, httpErr(apierrors.WsWorkspaceAccessDenied)
		}
		wsCtx, ok := middleware.WorkspaceFromContext(ctx)
		if !ok {
			return nil, httpErr(apierrors.WsWorkspaceNotFound)
		}

		limit := in.Limit
		if limit <= 0 {
			limit = 50
		}

		rows, err := deps.Queries.ListWebhookSubscriptions(ctx, generated.ListWebhookSubscriptionsParams{
			WorkspaceID: wsCtx.ID,
			Limit:       limit,
			Offset:      in.Offset,
		})
		if err != nil {
			return nil, httpErr(apierrors.InternalUnexpected)
		}

		out := &ListOutput{}
		out.Body.Webhooks = make([]WebhookSubscriptionDTO, 0, len(rows))
		for _, r := range rows {
			out.Body.Webhooks = append(out.Body.Webhooks, mapSubscriptionRow(r))
		}
		if len(rows) > 0 {
			out.Body.Total = totalAsInt64(rows[0].Total)
		}
		return out, nil
	}
}

// Get handles GET /workspaces/{wsId}/webhooks/{webhookId}. It returns
// the subscription detail including the signing secret.
func Get(deps Deps) func(context.Context, *GetInput) (*GetOutput, error) {
	return func(ctx context.Context, in *GetInput) (*GetOutput, error) {
		_, ok := middleware.ActorFromContext(ctx)
		if !ok {
			return nil, httpErr(apierrors.WsWorkspaceAccessDenied)
		}
		wsCtx, ok := middleware.WorkspaceFromContext(ctx)
		if !ok {
			return nil, httpErr(apierrors.WsWorkspaceNotFound)
		}

		pub, err := types.Parse(in.WebhookID)
		if err != nil {
			return nil, httpErr(apierrors.WebhookSubscriptionNotFound)
		}

		row, err := deps.Queries.GetWebhookSubscription(ctx, generated.GetWebhookSubscriptionParams{
			WorkspaceID: wsCtx.ID,
			PublicID:    pub,
		})
		if err != nil {
			return nil, httpErr(apierr.SpecForErrNoRows(err, apierrors.WebhookSubscriptionNotFound, apierrors.InternalUnexpected))
		}

		out := &GetOutput{}
		out.Body.Webhook = WebhookSubscriptionDetailDTO{
			WebhookSubscriptionDTO: WebhookSubscriptionDTO{
				ID:          row.PublicID.String(),
				URL:         row.Url,
				Description: row.Description,
				EventTypes:  row.EventTypes,
				IsActive:    row.IsActive,
				CreatedAt:   row.CreatedAt.Unix(),
				UpdatedAt:   nullTimeUnix(row.UpdatedAt),
			},
			Secret: row.Secret,
		}
		return out, nil
	}
}

// Delete handles DELETE /workspaces/{wsId}/webhooks/{webhookId}. It
// soft-disables the subscription.
func Delete(deps Deps) func(context.Context, *DeleteInput) (*DeleteOutput, error) {
	return func(ctx context.Context, in *DeleteInput) (*DeleteOutput, error) {
		actorID, ok := middleware.ActorFromContext(ctx)
		if !ok {
			return nil, httpErr(apierrors.WsWorkspaceAccessDenied)
		}
		wsCtx, ok := middleware.WorkspaceFromContext(ctx)
		if !ok {
			return nil, httpErr(apierrors.WsWorkspaceNotFound)
		}

		pub, err := types.Parse(in.WebhookID)
		if err != nil {
			return nil, httpErr(apierrors.WebhookSubscriptionNotFound)
		}

		if err := deps.Queries.DeleteWebhookSubscription(ctx, generated.DeleteWebhookSubscriptionParams{
			WorkspaceID: wsCtx.ID,
			PublicID:    pub,
		}); err != nil {
			return nil, httpErr(apierrors.InternalUnexpected)
		}

		deps.Audit.Record(ctx, audit.Entry{
			Action:       "webhook.subscription.delete",
			ActorID:      actorID,
			WorkspaceID:  wsCtx.ID,
			ResourceType: "webhook_subscription",
			ResourceID:   in.WebhookID,
		})

		out := &DeleteOutput{}
		out.Body.Ok = true
		return out, nil
	}
}

// Toggle handles PATCH /workspaces/{wsId}/webhooks/{webhookId}/toggle.
// It activates or deactivates a subscription.
func Toggle(deps Deps) func(context.Context, *ToggleInput) (*ToggleOutput, error) {
	return func(ctx context.Context, in *ToggleInput) (*ToggleOutput, error) {
		actorID, ok := middleware.ActorFromContext(ctx)
		if !ok {
			return nil, httpErr(apierrors.WsWorkspaceAccessDenied)
		}
		wsCtx, ok := middleware.WorkspaceFromContext(ctx)
		if !ok {
			return nil, httpErr(apierrors.WsWorkspaceNotFound)
		}

		pub, err := types.Parse(in.WebhookID)
		if err != nil {
			return nil, httpErr(apierrors.WebhookSubscriptionNotFound)
		}

		if err := deps.Queries.ToggleWebhookSubscription(ctx, generated.ToggleWebhookSubscriptionParams{
			IsActive:    in.Body.IsActive,
			WorkspaceID: wsCtx.ID,
			PublicID:    pub,
		}); err != nil {
			return nil, httpErr(apierrors.InternalUnexpected)
		}

		deps.Audit.Record(ctx, audit.Entry{
			Action:       "webhook.subscription.toggle",
			ActorID:      actorID,
			WorkspaceID:  wsCtx.ID,
			ResourceType: "webhook_subscription",
			ResourceID:   in.WebhookID,
			Metadata:     map[string]any{"isActive": in.Body.IsActive},
		})

		out := &ToggleOutput{}
		out.Body.Ok = true
		return out, nil
	}
}

// ListDeliveries handles GET /workspaces/{wsId}/webhooks/{webhookId}/deliveries.
func ListDeliveries(deps Deps) func(context.Context, *ListDeliveriesInput) (*ListDeliveriesOutput, error) {
	return func(ctx context.Context, in *ListDeliveriesInput) (*ListDeliveriesOutput, error) {
		_, ok := middleware.ActorFromContext(ctx)
		if !ok {
			return nil, httpErr(apierrors.WsWorkspaceAccessDenied)
		}
		wsCtx, ok := middleware.WorkspaceFromContext(ctx)
		if !ok {
			return nil, httpErr(apierrors.WsWorkspaceNotFound)
		}

		pub, err := types.Parse(in.WebhookID)
		if err != nil {
			return nil, httpErr(apierrors.WebhookSubscriptionNotFound)
		}

		limit := in.Limit
		if limit <= 0 {
			limit = 50
		}

		rows, err := deps.Queries.ListWebhookDeliveries(ctx, generated.ListWebhookDeliveriesParams{
			WorkspaceID:   wsCtx.ID,
			WorkspaceID_2: wsCtx.ID,
			PublicID:      pub,
			Limit:         limit,
			Offset:        in.Offset,
		})
		if err != nil {
			return nil, httpErr(apierrors.InternalUnexpected)
		}

		out := &ListDeliveriesOutput{}
		out.Body.Deliveries = make([]WebhookDeliveryDTO, 0, len(rows))
		for _, r := range rows {
			out.Body.Deliveries = append(out.Body.Deliveries, mapDeliveryRow(r))
		}
		if len(rows) > 0 {
			out.Body.Total = totalAsInt64(rows[0].Total)
		}
		return out, nil
	}
}

// TestDelivery handles POST /workspaces/{wsId}/webhooks/{webhookId}/test.
// It creates a test ping delivery for the subscription.
func TestDelivery(deps Deps) func(context.Context, *TestDeliveryInput) (*TestDeliveryOutput, error) {
	return func(ctx context.Context, in *TestDeliveryInput) (*TestDeliveryOutput, error) {
		actorID, ok := middleware.ActorFromContext(ctx)
		if !ok {
			return nil, httpErr(apierrors.WsWorkspaceAccessDenied)
		}
		wsCtx, ok := middleware.WorkspaceFromContext(ctx)
		if !ok {
			return nil, httpErr(apierrors.WsWorkspaceNotFound)
		}

		pub, err := types.Parse(in.WebhookID)
		if err != nil {
			return nil, httpErr(apierrors.WebhookSubscriptionNotFound)
		}

		// Verify the subscription exists.
		sub, err := deps.Queries.GetWebhookSubscription(ctx, generated.GetWebhookSubscriptionParams{
			WorkspaceID: wsCtx.ID,
			PublicID:    pub,
		})
		if err != nil {
			return nil, httpErr(apierr.SpecForErrNoRows(err, apierrors.WebhookSubscriptionNotFound, apierrors.InternalUnexpected))
		}

		// SSRF guard: refuse to enqueue a test delivery when the stored
		// URL is (or has become) a non-public destination.
		if err := webhook.ValidateURL(ctx, sub.Url); err != nil {
			return nil, httpErr(apierrors.WebhookSubscriptionUrlInvalid)
		}

		deliveryPub := types.New()
		now := time.Now().UTC()

		// Build a test ping payload. It carries the same identifier
		// fields a real delivery does, minus the ones a ping has no
		// answer for, so an operator verifying their receiver against
		// this ping is verifying it against the real shape.
		payload, _ := json.Marshal(map[string]any{
			"eventType":   "webhook.test",
			"deliveryId":  deliveryPub.String(),
			"workspaceId": wsCtx.PublicID.String(),
			"occurredAt":  handlerutil.NowUnix(),
		})

		// Resolve subscription internal id for the delivery.
		const subIDQuery = `SELECT id FROM webhook_subscriptions WHERE workspace_id = ? AND public_id = ? AND enabled = TRUE LIMIT 1`
		var subID uint32
		if err := deps.DB.QueryRowContext(ctx, subIDQuery, wsCtx.ID, pub).Scan(&subID); err != nil {
			return nil, httpErr(apierrors.WebhookSubscriptionNotFound)
		}

		if _, err := deps.Queries.CreateWebhookDelivery(ctx, generated.CreateWebhookDeliveryParams{
			PublicID:       deliveryPub,
			WorkspaceID:    wsCtx.ID,
			SubscriptionID: subID,
			EventType:      "webhook.test",
			EventPublicID:  nil,
			PayloadJson:    payload,
			NextRetryAt:    sql.NullTime{Time: now, Valid: true},
		}); err != nil {
			return nil, httpErr(apierrors.InternalUnexpected)
		}

		deps.Audit.Record(ctx, audit.Entry{
			Action:       "webhook.delivery.test",
			ActorID:      actorID,
			WorkspaceID:  wsCtx.ID,
			ResourceType: "webhook_subscription",
			ResourceID:   in.WebhookID,
		})

		out := &TestDeliveryOutput{}
		out.Body.DeliveryID = deliveryPub.String()
		return out, nil
	}
}

// --- mappers ---

// mapSubscriptionRow converts a ListWebhookSubscriptionsRow to a DTO.
// The secret is intentionally excluded from list responses.
func mapSubscriptionRow(r generated.ListWebhookSubscriptionsRow) WebhookSubscriptionDTO {
	return WebhookSubscriptionDTO{
		ID:                 r.PublicID.String(),
		URL:                r.Url,
		Description:        r.Description,
		EventTypes:         r.EventTypes,
		IsActive:           r.IsActive,
		CreatorID:          r.CreatorPublicID.String(),
		CreatorDisplayName: r.CreatorDisplayName,
		CreatedAt:          r.CreatedAt.Unix(),
		UpdatedAt:          nullTimeUnix(r.UpdatedAt),
	}
}

// mapDeliveryRow converts a ListWebhookDeliveriesRow to a DTO.
func mapDeliveryRow(r generated.ListWebhookDeliveriesRow) WebhookDeliveryDTO {
	var httpStatus *int16
	if r.HttpStatus.Valid {
		httpStatus = &r.HttpStatus.Int16
	}
	return WebhookDeliveryDTO{
		ID:          r.PublicID.String(),
		EventType:   r.EventType,
		Status:      string(r.Status),
		HTTPStatus:  httpStatus,
		Attempts:    r.Attempts,
		MaxAttempts: r.MaxAttempts,
		DeliveredAt: nullTimeUnix(r.DeliveredAt),
		FailedAt:    nullTimeUnix(r.FailedAt),
		CreatedAt:   r.CreatedAt.Unix(),
	}
}
