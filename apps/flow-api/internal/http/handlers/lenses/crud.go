package lenses

import (
	"context"
	"database/sql"

	"github.com/libraz/nodate-flow/apps/flow-api/internal/audit"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/generated"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/types"
	apierrors "github.com/libraz/nodate-flow/apps/flow-api/internal/errors"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/http/handlers/handlerutil"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/http/middleware"
	"github.com/libraz/nodate-flow/packages/go-shared/apierr"
)

// Create handles POST /workspaces/{wsId}/lenses.
func Create(deps Deps) func(context.Context, *CreateLensInput) (*CreateLensOutput, error) {
	return func(ctx context.Context, in *CreateLensInput) (*CreateLensOutput, error) {
		ws, ok := middleware.WorkspaceFromContext(ctx)
		if !ok {
			return nil, httpErr(apierrors.WsWorkspaceNotFound)
		}
		actorID, ok := middleware.ActorFromContext(ctx)
		if !ok {
			return nil, httpErr(apierrors.WsMemberRoleDenied)
		}

		// Resolve optional project public_id → internal id.
		var projectID sql.NullInt32
		if in.Body.ProjectID != nil && *in.Body.ProjectID != "" {
			pid, err := types.Parse(*in.Body.ProjectID)
			if err != nil {
				return nil, httpErr(apierrors.ValidationPathParamInvalid)
			}
			row, err := deps.Queries.FindProjectByPublicId(ctx, generated.FindProjectByPublicIdParams{
				WorkspaceID: ws.ID,
				PublicID:    pid,
			})
			if err != nil {
				return nil, httpErr(apierr.SpecForErrNoRows(err, apierrors.WsProjectNotFound, apierrors.InternalUnexpected))
			}
			projectID = sql.NullInt32{Int32: int32(row.ID), Valid: true} //#nosec G115 -- project_id is projects.id (BIGINT UNSIGNED), fits int32 within realistic deployments
		}

		pub := types.New()
		lensBlob := buildLensJSON(in.Body.Filter, in.Body.Sort, in.Body.GroupBy)
		description := optionalNullString(in.Body.Description)

		if _, err := deps.Queries.CreateLens(ctx, generated.CreateLensParams{
			PublicID:    pub,
			WorkspaceID: ws.ID,
			ProjectID:   projectID,
			CreatorID:   actorID,
			Name:        in.Body.Name,
			Description: description,
			LensJson:    lensBlob,
			IsDefault:   in.Body.IsDefault,
			SortWeight:  0,
		}); err != nil {
			// Check for duplicate name via MySQL error code.
			if isDuplicateEntry(err) {
				return nil, httpErr(apierrors.WsLensNameAlreadyTaken)
			}
			return nil, httpErr(apierrors.InternalUnexpected)
		}

		deps.Audit.Record(ctx, audit.Entry{
			Action:       "lens.create",
			ActorID:      actorID,
			WorkspaceID:  ws.ID,
			ResourceType: "lens",
			ResourceID:   pub.String(),
			Metadata:     map[string]any{"name": in.Body.Name},
		})

		return &CreateLensOutput{Body: SavedLens{
			ID:          pub.String(),
			Name:        in.Body.Name,
			Description: in.Body.Description,
			Filter:      in.Body.Filter,
			Sort:        in.Body.Sort,
			GroupBy:     in.Body.GroupBy,
			IsDefault:   in.Body.IsDefault,
			CreatedAt:   handlerutil.NowUnix(),
		}}, nil
	}
}

// List handles GET /workspaces/{wsId}/lenses.
func List(deps Deps) func(context.Context, *ListLensesInput) (*ListLensesOutput, error) {
	return func(ctx context.Context, in *ListLensesInput) (*ListLensesOutput, error) {
		ws, ok := middleware.WorkspaceFromContext(ctx)
		if !ok {
			return nil, httpErr(apierrors.WsWorkspaceNotFound)
		}

		// Resolve optional project public_id → internal id.
		var projectID sql.NullInt32
		if in.ProjectID != "" {
			pid, err := types.Parse(in.ProjectID)
			if err != nil {
				return nil, httpErr(apierrors.ValidationPathParamInvalid)
			}
			row, err := deps.Queries.FindProjectByPublicId(ctx, generated.FindProjectByPublicIdParams{
				WorkspaceID: ws.ID,
				PublicID:    pid,
			})
			if err != nil {
				return nil, httpErr(apierr.SpecForErrNoRows(err, apierrors.WsProjectNotFound, apierrors.InternalUnexpected))
			}
			projectID = sql.NullInt32{Int32: int32(row.ID), Valid: true} //#nosec G115 -- project_id is projects.id (BIGINT UNSIGNED), fits int32 within realistic deployments
		}

		limit := in.Limit
		if limit <= 0 {
			limit = 50
		}

		rows, err := deps.Queries.ListLensesForProject(ctx, generated.ListLensesForProjectParams{
			WorkspaceID: ws.ID,
			ProjectID:   projectID,
			Limit:       limit,
			Offset:      in.Offset,
		})
		if err != nil {
			return nil, httpErr(apierrors.InternalUnexpected)
		}

		out := &ListLensesOutput{}
		out.Body.Lenses = make([]SavedLens, 0, len(rows))
		for _, r := range rows {
			out.Body.Lenses = append(out.Body.Lenses, rowToLensFromList(r))
		}
		if len(rows) > 0 {
			out.Body.Total = totalAsInt64(rows[0].Total)
		}
		return out, nil
	}
}

// Get handles GET /workspaces/{wsId}/lenses/{lensId}.
func Get(deps Deps) func(context.Context, *GetLensInput) (*GetLensOutput, error) {
	return func(ctx context.Context, in *GetLensInput) (*GetLensOutput, error) {
		ws, ok := middleware.WorkspaceFromContext(ctx)
		if !ok {
			return nil, httpErr(apierrors.WsWorkspaceNotFound)
		}
		lid, err := types.Parse(in.LensID)
		if err != nil {
			return nil, httpErr(apierrors.ValidationPathParamInvalid)
		}
		row, err := deps.Queries.GetLensByPublicID(ctx, generated.GetLensByPublicIDParams{
			WorkspaceID: ws.ID,
			PublicID:    lid,
		})
		if err != nil {
			return nil, httpErr(apierr.SpecForErrNoRows(err, apierrors.WsLensNotFound, apierrors.InternalUnexpected))
		}
		return &GetLensOutput{Body: rowToLensFromGet(row)}, nil
	}
}

// Update handles PATCH /workspaces/{wsId}/lenses/{lensId}.
func Update(deps Deps) func(context.Context, *UpdateLensInput) (*UpdateLensOutput, error) {
	return func(ctx context.Context, in *UpdateLensInput) (*UpdateLensOutput, error) {
		ws, ok := middleware.WorkspaceFromContext(ctx)
		if !ok {
			return nil, httpErr(apierrors.WsWorkspaceNotFound)
		}
		lid, err := types.Parse(in.LensID)
		if err != nil {
			return nil, httpErr(apierrors.ValidationPathParamInvalid)
		}

		// Fetch existing to merge partial updates.
		existing, err := deps.Queries.GetLensByPublicID(ctx, generated.GetLensByPublicIDParams{
			WorkspaceID: ws.ID,
			PublicID:    lid,
		})
		if err != nil {
			return nil, httpErr(apierr.SpecForErrNoRows(err, apierrors.WsLensNotFound, apierrors.InternalUnexpected))
		}

		// Parse existing lens_json for merge.
		existFilter, existSort, existGroupBy := parseLensJSON(existing.LensJson)

		name := existing.Name
		if in.Body.Name != nil {
			name = *in.Body.Name
		}
		// Description is a *string in the patch body so we cannot tell
		// "unset" apart from "explicit null" once decoded; treat nil as
		// "leave alone" (PATCH merge semantics) and any non-nil value as
		// the new description (empty string clears).
		description := existing.Description
		if in.Body.Description != nil {
			description = optionalNullString(in.Body.Description)
		}
		filter := existFilter
		if in.Body.Filter != nil {
			filter = *in.Body.Filter
		}
		sort := existSort
		if in.Body.Sort != nil {
			sort = *in.Body.Sort
		}
		groupBy := existGroupBy
		if in.Body.GroupBy != nil {
			groupBy = in.Body.GroupBy
		}
		isDefault := existing.IsDefault
		if in.Body.IsDefault != nil {
			isDefault = *in.Body.IsDefault
		}

		lensBlob := buildLensJSON(filter, sort, groupBy)

		if err := deps.Queries.UpdateLens(ctx, generated.UpdateLensParams{
			Name:        name,
			Description: description,
			LensJson:    lensBlob,
			IsDefault:   isDefault,
			WorkspaceID: ws.ID,
			PublicID:    lid,
		}); err != nil {
			if isDuplicateEntry(err) {
				return nil, httpErr(apierrors.WsLensNameAlreadyTaken)
			}
			return nil, httpErr(apierrors.InternalUnexpected)
		}

		if actorID, ok := middleware.ActorFromContext(ctx); ok {
			deps.Audit.Record(ctx, audit.Entry{
				Action:       "lens.update",
				ActorID:      actorID,
				WorkspaceID:  ws.ID,
				ResourceType: "lens",
				ResourceID:   existing.PublicID.String(),
			})
		}

		return &UpdateLensOutput{Body: SavedLens{
			ID:                 existing.PublicID.String(),
			CreatorID:          existing.CreatorPublicID.String(),
			CreatorDisplayName: existing.CreatorDisplayName,
			Name:               name,
			Description:        nullString(description),
			Filter:             filter,
			Sort:               sort,
			GroupBy:            groupBy,
			IsDefault:          isDefault,
			IsPublic:           existing.IsPublic,
			SharedAt:           nullTimeUnix(existing.SharedAt),
			SafetyCheckedAt:    nullTimeUnix(existing.SafetyCheckedAt),
			SortWeight:         existing.SortWeight,
			UpdatedAt:          nullTimeUnix(existing.UpdatedAt),
			CreatedAt:          existing.CreatedAt.Unix(),
		}}, nil
	}
}

// Delete handles DELETE /workspaces/{wsId}/lenses/{lensId}.
func Delete(deps Deps) func(context.Context, *DeleteLensInput) (*DeleteLensOutput, error) {
	return func(ctx context.Context, in *DeleteLensInput) (*DeleteLensOutput, error) {
		ws, ok := middleware.WorkspaceFromContext(ctx)
		if !ok {
			return nil, httpErr(apierrors.WsWorkspaceNotFound)
		}
		lid, err := types.Parse(in.LensID)
		if err != nil {
			return nil, httpErr(apierrors.ValidationPathParamInvalid)
		}
		if err := deps.Queries.DeleteLens(ctx, generated.DeleteLensParams{
			WorkspaceID: ws.ID,
			PublicID:    lid,
		}); err != nil {
			return nil, httpErr(apierrors.InternalUnexpected)
		}

		if actorID, ok := middleware.ActorFromContext(ctx); ok {
			deps.Audit.Record(ctx, audit.Entry{
				Action:       "lens.delete",
				ActorID:      actorID,
				WorkspaceID:  ws.ID,
				ResourceType: "lens",
				ResourceID:   lid.String(),
			})
		}

		out := &DeleteLensOutput{}
		out.Body.Ok = true
		return out, nil
	}
}

// isDuplicateEntry delegates to handlerutil.IsDuplicateEntry.
var isDuplicateEntry = handlerutil.IsDuplicateEntry
