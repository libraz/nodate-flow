package dashboard

import (
	"context"
	"database/sql"
	"encoding/json"

	"github.com/libraz/nodate-flow/apps/flow-api/internal/audit"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/dbretry"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/generated"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/types"
	apierrors "github.com/libraz/nodate-flow/apps/flow-api/internal/errors"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/eventbus"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/http/handlers/handlerutil"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/http/middleware"
	"github.com/libraz/nodate-flow/packages/go-shared/apierr"
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

		eventbus.AppendBestEffort(ctx, dbretry.AutoCommit(deps.DB), eventbus.Event{
			Type:        eventbus.DashboardWidgetCreated,
			WorkspaceID: ws.ID,
			ActorUserID: actorPtr(ctx),
			Payload: map[string]any{
				"widgetId":   pub.String(),
				"widgetType": in.Body.WidgetType,
				"title":      in.Body.Title,
			},
		}, "dashboard.Create")

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

		// Existence is not decided by the row count here; MySQL counts
		// changed rows, so a PATCH that sets the values already stored
		// reports zero. The re-read below fails if the widget is gone.
		if _, err := deps.Queries.UpdateWidget(ctx, params); err != nil {
			return nil, httpErr(apierrors.InternalUnexpected)
		}

		eventbus.AppendBestEffort(ctx, dbretry.AutoCommit(deps.DB), eventbus.Event{
			Type:        eventbus.DashboardWidgetUpdated,
			WorkspaceID: ws.ID,
			ActorUserID: actorPtr(ctx),
			Payload: map[string]any{
				"widgetId": pub.String(),
			},
		}, "dashboard.Update")

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

		// The affected-row count is not the existence check: MySQL counts
		// changed rows, so re-sending the position a widget already has
		// reports zero. Existence is checked by the read just above.
		if _, err := deps.Queries.UpdateWidgetPosition(ctx, generated.UpdateWidgetPositionParams{
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

		eventbus.AppendBestEffort(ctx, dbretry.AutoCommit(deps.DB), eventbus.Event{
			Type:        eventbus.DashboardWidgetUpdated,
			WorkspaceID: ws.ID,
			ActorUserID: actorPtr(ctx),
			Payload: map[string]any{
				"widgetId": pub.String(),
				"action":   "position",
			},
		}, "dashboard.UpdatePosition")

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

		// Scoped to the workspace and to widgets that are still live, so a
		// zero count means the caller named a widget that is not there to
		// remove. Reporting success wrote a dashboard.widget.disabled event
		// for a widget that never existed.
		rows, err := deps.Queries.DisableWidget(ctx, generated.DisableWidgetParams{
			WorkspaceID: ws.ID,
			PublicID:    pub,
		})
		if err != nil {
			return nil, httpErr(apierrors.InternalUnexpected)
		}
		if rows == 0 {
			return nil, httpErr(apierrors.WsDashboardWidgetNotFound)
		}

		eventbus.AppendBestEffort(ctx, dbretry.AutoCommit(deps.DB), eventbus.Event{
			Type:        eventbus.DashboardWidgetDisabled,
			WorkspaceID: ws.ID,
			ActorUserID: actorPtr(ctx),
			Payload: map[string]any{
				"widgetId": pub.String(),
			},
		}, "dashboard.Delete")

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
