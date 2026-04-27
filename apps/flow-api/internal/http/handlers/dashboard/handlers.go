package dashboard

import (
	"context"
	"database/sql"
	"encoding/json"
	"log/slog"

	"github.com/nodate-flow/nodate-flow/apps/flow-api/internal/audit"
	"github.com/nodate-flow/nodate-flow/apps/flow-api/internal/db/generated"
	"github.com/nodate-flow/nodate-flow/apps/flow-api/internal/db/types"
	apierrors "github.com/nodate-flow/nodate-flow/apps/flow-api/internal/errors"
	"github.com/nodate-flow/nodate-flow/apps/flow-api/internal/eventbus"
	"github.com/nodate-flow/nodate-flow/apps/flow-api/internal/http/handlers/handlerutil"
	"github.com/nodate-flow/nodate-flow/apps/flow-api/internal/http/middleware"
	"github.com/nodate-flow/nodate-flow/packages/go-shared/apierr"
	"github.com/nodate-flow/nodate-flow/packages/go-shared/logutil"
)

// actorPtr delegates to handlerutil.ActorPtr.
var actorPtr = handlerutil.ActorPtr

// Create handles POST /workspaces/{wsId}/dashboard/widgets.
func Create(deps Deps) func(context.Context, *CreateWidgetInput) (*CreateWidgetOutput, error) {
	return func(ctx context.Context, in *CreateWidgetInput) (*CreateWidgetOutput, error) {
		ws, ok := middleware.WorkspaceFromContext(ctx)
		if !ok {
			return nil, httpErr(apierrors.WsWorkspaceNotFound)
		}
		actorID, ok := middleware.ActorFromContext(ctx)
		if !ok {
			return nil, httpErr(apierrors.WsMemberRoleDenied)
		}

		configRaw := json.RawMessage("{}")
		if in.Body.Config != nil {
			configRaw = in.Body.Config
		}

		pub := types.New()
		_, err := deps.Queries.CreateWidget(ctx, generated.CreateWidgetParams{
			PublicID:    pub,
			WorkspaceID: ws.ID,
			CreatorID:   actorID,
			WidgetType:  generated.DashboardWidgetsWidgetType(in.Body.WidgetType),
			Title:       in.Body.Title,
			Config:      configRaw,
			PositionX:   uint16(in.Body.PositionX), //#nosec G115 -- PositionX request-validated to maximum:1023 (well below uint16)
			PositionY:   uint16(in.Body.PositionY), //#nosec G115 -- PositionY request-validated to maximum:1023 (well below uint16)
			Width:       uint16(in.Body.Width),     //#nosec G115 -- Width request-validated to a small uint range
			Height:      uint16(in.Body.Height),    //#nosec G115 -- Height request-validated to a small uint range
		})
		if err != nil {
			return nil, httpErr(apierrors.InternalUnexpected)
		}

		if err := eventbus.Append(ctx, deps.DB, eventbus.Event{
			Type:        eventbus.DashboardWidgetCreated,
			WorkspaceID: ws.ID,
			ActorUserID: actorPtr(ctx),
			Payload: map[string]any{
				"widgetId":   pub.String(),
				"widgetType": in.Body.WidgetType,
				"title":      in.Body.Title,
			},
		}); err != nil {
			slog.ErrorContext(ctx, "eventbus.Append failed",
				slog.Any("err", err),
				slog.String("handler", "dashboard.Create"),
				slog.String("event_type", string(eventbus.DashboardWidgetCreated)),
				logutil.LogEntity("workspace", ws.PublicID),
				slog.String("widget_public_id", pub.String()),
			)
		}

		deps.Audit.Record(ctx, audit.Entry{
			Action:       "dashboard.widget.create",
			ActorID:      actorID,
			WorkspaceID:  ws.ID,
			ResourceType: "dashboard_widget",
			ResourceID:   pub.String(),
			Metadata:     map[string]any{"widgetType": in.Body.WidgetType, "title": in.Body.Title},
		})

		return &CreateWidgetOutput{Body: WidgetDTO{
			ID:                 pub.String(),
			CreatorID:          "",
			CreatorDisplayName: "",
			WidgetType:         in.Body.WidgetType,
			Title:              in.Body.Title,
			Config:             configRaw,
			PositionX:          in.Body.PositionX,
			PositionY:          in.Body.PositionY,
			Width:              in.Body.Width,
			Height:             in.Body.Height,
			SortWeight:         0,
			UpdatedAt:          0,
			CreatedAt:          handlerutil.NowUnix(),
		}}, nil
	}
}

// List handles GET /workspaces/{wsId}/dashboard/widgets.
func List(deps Deps) func(context.Context, *ListWidgetsInput) (*ListWidgetsOutput, error) {
	return func(ctx context.Context, in *ListWidgetsInput) (*ListWidgetsOutput, error) {
		ws, ok := middleware.WorkspaceFromContext(ctx)
		if !ok {
			return nil, httpErr(apierrors.WsWorkspaceNotFound)
		}

		limit := in.Limit
		if limit <= 0 {
			limit = 50
		}

		rows, err := deps.Queries.ListWidgetsForWorkspace(ctx, generated.ListWidgetsForWorkspaceParams{
			WorkspaceID: ws.ID,
			Limit:       limit,
			Offset:      in.Offset,
		})
		if err != nil {
			return nil, httpErr(apierrors.InternalUnexpected)
		}

		out := &ListWidgetsOutput{}
		out.Body.Widgets = make([]WidgetDTO, 0, len(rows))
		for _, r := range rows {
			out.Body.Widgets = append(out.Body.Widgets, mapListRow(r))
		}
		if len(rows) > 0 {
			out.Body.Total = totalAsInt64(rows[0].Total)
		}
		return out, nil
	}
}

// Get handles GET /workspaces/{wsId}/dashboard/widgets/{widgetId}.
func Get(deps Deps) func(context.Context, *GetWidgetInput) (*GetWidgetOutput, error) {
	return func(ctx context.Context, in *GetWidgetInput) (*GetWidgetOutput, error) {
		ws, ok := middleware.WorkspaceFromContext(ctx)
		if !ok {
			return nil, httpErr(apierrors.WsWorkspaceNotFound)
		}
		pub, err := types.Parse(in.WidgetID)
		if err != nil {
			return nil, httpErr(apierrors.ValidationPathParamInvalid)
		}
		row, err := deps.Queries.GetWidgetByPublicID(ctx, generated.GetWidgetByPublicIDParams{
			WorkspaceID: ws.ID,
			PublicID:    pub,
		})
		if err != nil {
			return nil, httpErr(apierr.SpecForErrNoRows(err, apierrors.WsWorkspaceNotFound, apierrors.InternalUnexpected))
		}
		return &GetWidgetOutput{Body: mapGetRow(row)}, nil
	}
}

// Update handles PATCH /workspaces/{wsId}/dashboard/widgets/{widgetId}.
func Update(deps Deps) func(context.Context, *UpdateWidgetInput) (*UpdateWidgetOutput, error) {
	return func(ctx context.Context, in *UpdateWidgetInput) (*UpdateWidgetOutput, error) {
		ws, ok := middleware.WorkspaceFromContext(ctx)
		if !ok {
			return nil, httpErr(apierrors.WsWorkspaceNotFound)
		}
		pub, err := types.Parse(in.WidgetID)
		if err != nil {
			return nil, httpErr(apierrors.ValidationPathParamInvalid)
		}

		// Verify the widget exists before updating.
		_, err = deps.Queries.GetWidgetByPublicID(ctx, generated.GetWidgetByPublicIDParams{
			WorkspaceID: ws.ID,
			PublicID:    pub,
		})
		if err != nil {
			return nil, httpErr(apierr.SpecForErrNoRows(err, apierrors.WsWorkspaceNotFound, apierrors.InternalUnexpected))
		}

		// Build partial update params.
		params := generated.UpdateWidgetParams{
			WorkspaceID: ws.ID,
			PublicID:    pub,
		}
		if in.Body.Title != nil {
			params.Title = sql.NullString{String: *in.Body.Title, Valid: true}
		}
		if in.Body.Config != nil {
			params.Config = *in.Body.Config
		}
		if in.Body.PositionX != nil {
			params.PositionX = sql.NullInt16{Int16: int16(*in.Body.PositionX), Valid: true} //#nosec G115 -- PositionX request-validated to maximum:1023
		}
		if in.Body.PositionY != nil {
			params.PositionY = sql.NullInt16{Int16: int16(*in.Body.PositionY), Valid: true} //#nosec G115 -- PositionY request-validated to maximum:1023
		}
		if in.Body.Width != nil {
			params.Width = sql.NullInt16{Int16: int16(*in.Body.Width), Valid: true} //#nosec G115 -- Width request-validated to a small uint range
		}
		if in.Body.Height != nil {
			params.Height = sql.NullInt16{Int16: int16(*in.Body.Height), Valid: true} //#nosec G115 -- Height request-validated to a small uint range
		}

		if err := deps.Queries.UpdateWidget(ctx, params); err != nil {
			return nil, httpErr(apierrors.InternalUnexpected)
		}

		if err := eventbus.Append(ctx, deps.DB, eventbus.Event{
			Type:        eventbus.DashboardWidgetUpdated,
			WorkspaceID: ws.ID,
			ActorUserID: actorPtr(ctx),
			Payload: map[string]any{
				"widgetId": pub.String(),
			},
		}); err != nil {
			slog.ErrorContext(ctx, "eventbus.Append failed",
				slog.Any("err", err),
				slog.String("handler", "dashboard.Update"),
				slog.String("event_type", string(eventbus.DashboardWidgetUpdated)),
				logutil.LogEntity("workspace", ws.PublicID),
				slog.String("widget_public_id", pub.String()),
			)
		}

		if actorID, ok := middleware.ActorFromContext(ctx); ok {
			deps.Audit.Record(ctx, audit.Entry{
				Action:       "dashboard.widget.update",
				ActorID:      actorID,
				WorkspaceID:  ws.ID,
				ResourceType: "dashboard_widget",
				ResourceID:   pub.String(),
			})
		}

		// Re-fetch to return the updated row.
		updated, err := deps.Queries.GetWidgetByPublicID(ctx, generated.GetWidgetByPublicIDParams{
			WorkspaceID: ws.ID,
			PublicID:    pub,
		})
		if err != nil {
			return nil, httpErr(apierrors.InternalUnexpected)
		}
		return &UpdateWidgetOutput{Body: mapGetRow(updated)}, nil
	}
}

// UpdatePosition handles PUT /workspaces/{wsId}/dashboard/widgets/{widgetId}/position.
func UpdatePosition(deps Deps) func(context.Context, *UpdateWidgetPositionInput) (*UpdateWidgetPositionOutput, error) {
	return func(ctx context.Context, in *UpdateWidgetPositionInput) (*UpdateWidgetPositionOutput, error) {
		ws, ok := middleware.WorkspaceFromContext(ctx)
		if !ok {
			return nil, httpErr(apierrors.WsWorkspaceNotFound)
		}
		pub, err := types.Parse(in.WidgetID)
		if err != nil {
			return nil, httpErr(apierrors.ValidationPathParamInvalid)
		}

		// Verify the widget exists before updating.
		_, err = deps.Queries.GetWidgetByPublicID(ctx, generated.GetWidgetByPublicIDParams{
			WorkspaceID: ws.ID,
			PublicID:    pub,
		})
		if err != nil {
			return nil, httpErr(apierr.SpecForErrNoRows(err, apierrors.WsWorkspaceNotFound, apierrors.InternalUnexpected))
		}

		if err := deps.Queries.UpdateWidgetPosition(ctx, generated.UpdateWidgetPositionParams{
			PositionX:   uint16(in.Body.PositionX), //#nosec G115 -- PositionX request-validated to maximum:1023 (well below uint16)
			PositionY:   uint16(in.Body.PositionY), //#nosec G115 -- PositionY request-validated to maximum:1023 (well below uint16)
			Width:       uint16(in.Body.Width),     //#nosec G115 -- Width request-validated to a small uint range
			Height:      uint16(in.Body.Height),    //#nosec G115 -- Height request-validated to a small uint range
			SortWeight:  int32(in.Body.SortWeight), //#nosec G115 -- SortWeight request-validated to a 32-bit signed range
			WorkspaceID: ws.ID,
			PublicID:    pub,
		}); err != nil {
			return nil, httpErr(apierrors.InternalUnexpected)
		}

		if err := eventbus.Append(ctx, deps.DB, eventbus.Event{
			Type:        eventbus.DashboardWidgetUpdated,
			WorkspaceID: ws.ID,
			ActorUserID: actorPtr(ctx),
			Payload: map[string]any{
				"widgetId": pub.String(),
				"action":   "position",
			},
		}); err != nil {
			slog.ErrorContext(ctx, "eventbus.Append failed",
				slog.Any("err", err),
				slog.String("handler", "dashboard.UpdatePosition"),
				slog.String("event_type", string(eventbus.DashboardWidgetUpdated)),
				logutil.LogEntity("workspace", ws.PublicID),
				slog.String("widget_public_id", pub.String()),
			)
		}

		if actorID, ok := middleware.ActorFromContext(ctx); ok {
			deps.Audit.Record(ctx, audit.Entry{
				Action:       "dashboard.widget.position",
				ActorID:      actorID,
				WorkspaceID:  ws.ID,
				ResourceType: "dashboard_widget",
				ResourceID:   pub.String(),
			})
		}

		// Re-fetch to return the updated row.
		updated, err := deps.Queries.GetWidgetByPublicID(ctx, generated.GetWidgetByPublicIDParams{
			WorkspaceID: ws.ID,
			PublicID:    pub,
		})
		if err != nil {
			return nil, httpErr(apierrors.InternalUnexpected)
		}
		return &UpdateWidgetPositionOutput{Body: mapGetRow(updated)}, nil
	}
}

// Delete handles DELETE /workspaces/{wsId}/dashboard/widgets/{widgetId}.
func Delete(deps Deps) func(context.Context, *DeleteWidgetInput) (*DeleteWidgetOutput, error) {
	return func(ctx context.Context, in *DeleteWidgetInput) (*DeleteWidgetOutput, error) {
		ws, ok := middleware.WorkspaceFromContext(ctx)
		if !ok {
			return nil, httpErr(apierrors.WsWorkspaceNotFound)
		}
		pub, err := types.Parse(in.WidgetID)
		if err != nil {
			return nil, httpErr(apierrors.ValidationPathParamInvalid)
		}

		if err := deps.Queries.DisableWidget(ctx, generated.DisableWidgetParams{
			WorkspaceID: ws.ID,
			PublicID:    pub,
		}); err != nil {
			return nil, httpErr(apierrors.InternalUnexpected)
		}

		if err := eventbus.Append(ctx, deps.DB, eventbus.Event{
			Type:        eventbus.DashboardWidgetDisabled,
			WorkspaceID: ws.ID,
			ActorUserID: actorPtr(ctx),
			Payload: map[string]any{
				"widgetId": pub.String(),
			},
		}); err != nil {
			slog.ErrorContext(ctx, "eventbus.Append failed",
				slog.Any("err", err),
				slog.String("handler", "dashboard.Delete"),
				slog.String("event_type", string(eventbus.DashboardWidgetDisabled)),
				logutil.LogEntity("workspace", ws.PublicID),
				slog.String("widget_public_id", pub.String()),
			)
		}

		if actorID, ok := middleware.ActorFromContext(ctx); ok {
			deps.Audit.Record(ctx, audit.Entry{
				Action:       "dashboard.widget.delete",
				ActorID:      actorID,
				WorkspaceID:  ws.ID,
				ResourceType: "dashboard_widget",
				ResourceID:   pub.String(),
			})
		}

		out := &DeleteWidgetOutput{}
		out.Body.Ok = true
		return out, nil
	}
}
