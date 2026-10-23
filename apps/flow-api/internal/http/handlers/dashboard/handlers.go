package dashboard

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"

	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/dbretry"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/generated"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/types"
	apierrors "github.com/libraz/nodate-flow/apps/flow-api/internal/errors"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/eventbus"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/http/handlers/handlerutil"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/http/middleware"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/mutationlog"
	"github.com/libraz/nodate-flow/packages/go-shared/apierr"
)

// errWidgetNotFound rolls a delete back when the widget is not there to
// remove, so no event or audit row describes it.
var errWidgetNotFound = errors.New("dashboard: widget not found")

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
		if err := dbretry.InTx(ctx, deps.DB, "dashboard.Create", nil, func(ctx context.Context, tx *dbretry.Tx) error {
			if _, err := generated.New(tx).CreateWidget(ctx, generated.CreateWidgetParams{
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
			}); err != nil {
				return err
			}
			return deps.Mutations.RecordInTx(ctx, tx, mutationlog.Actor{UserID: actorID, WorkspaceID: ws.ID}, mutationlog.Mutation{
				EventType:    eventbus.DashboardWidgetCreated,
				AuditAction:  "dashboard.widget.create",
				ResourceType: "dashboard_widget",
				ResourceID:   pub.String(),
				Payload: map[string]any{
					"widgetId":   pub.String(),
					"widgetType": in.Body.WidgetType,
					"title":      in.Body.Title,
				},
				CallSite: "dashboard.Create",
			})
		}); err != nil {
			return nil, httpErr(apierrors.InternalUnexpected)
		}

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
		actorID, _ := middleware.ActorFromContext(ctx)
		if err := dbretry.InTx(ctx, deps.DB, "dashboard.Update", nil, func(ctx context.Context, tx *dbretry.Tx) error {
			if _, err := generated.New(tx).UpdateWidget(ctx, params); err != nil {
				return err
			}
			return deps.Mutations.RecordInTx(ctx, tx, mutationlog.Actor{UserID: actorID, WorkspaceID: ws.ID}, mutationlog.Mutation{
				EventType:    eventbus.DashboardWidgetUpdated,
				AuditAction:  "dashboard.widget.update",
				ResourceType: "dashboard_widget",
				ResourceID:   pub.String(),
				Payload:      map[string]any{"widgetId": pub.String()},
				CallSite:     "dashboard.Update",
			})
		}); err != nil {
			return nil, httpErr(apierrors.InternalUnexpected)
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
		actorID, _ := middleware.ActorFromContext(ctx)
		if err := dbretry.InTx(ctx, deps.DB, "dashboard.UpdatePosition", nil, func(ctx context.Context, tx *dbretry.Tx) error {
			if _, err := generated.New(tx).UpdateWidgetPosition(ctx, generated.UpdateWidgetPositionParams{
				PositionX:   uint16(in.Body.PositionX), //#nosec G115 -- PositionX request-validated to maximum:1023 (well below uint16)
				PositionY:   uint16(in.Body.PositionY), //#nosec G115 -- PositionY request-validated to maximum:1023 (well below uint16)
				Width:       uint16(in.Body.Width),     //#nosec G115 -- Width request-validated to a small uint range
				Height:      uint16(in.Body.Height),    //#nosec G115 -- Height request-validated to a small uint range
				SortWeight:  int32(in.Body.SortWeight), //#nosec G115 -- SortWeight request-validated to a 32-bit signed range
				WorkspaceID: ws.ID,
				PublicID:    pub,
			}); err != nil {
				return err
			}
			return deps.Mutations.RecordInTx(ctx, tx, mutationlog.Actor{UserID: actorID, WorkspaceID: ws.ID}, mutationlog.Mutation{
				EventType:    eventbus.DashboardWidgetUpdated,
				AuditAction:  "dashboard.widget.position",
				ResourceType: "dashboard_widget",
				ResourceID:   pub.String(),
				Payload:      map[string]any{"widgetId": pub.String(), "action": "position"},
				CallSite:     "dashboard.UpdatePosition",
			})
		}); err != nil {
			return nil, httpErr(apierrors.InternalUnexpected)
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
		actorID, _ := middleware.ActorFromContext(ctx)
		err = dbretry.InTx(ctx, deps.DB, "dashboard.Delete", nil, func(ctx context.Context, tx *dbretry.Tx) error {
			rows, err := generated.New(tx).DisableWidget(ctx, generated.DisableWidgetParams{
				WorkspaceID: ws.ID,
				PublicID:    pub,
			})
			if err != nil {
				return err
			}
			if rows == 0 {
				return errWidgetNotFound
			}
			return deps.Mutations.RecordInTx(ctx, tx, mutationlog.Actor{UserID: actorID, WorkspaceID: ws.ID}, mutationlog.Mutation{
				EventType:    eventbus.DashboardWidgetDisabled,
				AuditAction:  "dashboard.widget.delete",
				ResourceType: "dashboard_widget",
				ResourceID:   pub.String(),
				Payload:      map[string]any{"widgetId": pub.String()},
				CallSite:     "dashboard.Delete",
			})
		})
		if errors.Is(err, errWidgetNotFound) {
			return nil, httpErr(apierrors.WsDashboardWidgetNotFound)
		}
		if err != nil {
			return nil, httpErr(apierrors.InternalUnexpected)
		}

		out := &DeleteWidgetOutput{}
		out.Body.Ok = true
		return out, nil
	}
}
