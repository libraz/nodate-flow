package labels

import (
	"context"
	"database/sql"
	"errors"

	"github.com/nodate-flow/nodate-flow/apps/flow-api/internal/audit"
	"github.com/nodate-flow/nodate-flow/apps/flow-api/internal/db/generated"
	"github.com/nodate-flow/nodate-flow/apps/flow-api/internal/db/types"
	apierrors "github.com/nodate-flow/nodate-flow/apps/flow-api/internal/errors"
	"github.com/nodate-flow/nodate-flow/apps/flow-api/internal/eventbus"
	"github.com/nodate-flow/nodate-flow/apps/flow-api/internal/http/middleware"
)

// Create handles POST /workspaces/{wsId}/labels.
func Create(deps Deps) func(context.Context, *CreateLabelInput) (*CreateLabelOutput, error) {
	return func(ctx context.Context, in *CreateLabelInput) (*CreateLabelOutput, error) {
		ws, ok := middleware.WorkspaceFromContext(ctx)
		if !ok {
			return nil, httpErr(apierrors.WsWorkspaceNotFound)
		}

		pub := types.New()
		color := in.Body.Color
		if color == "" {
			color = "#6b7280"
		}

		var projectID sql.NullInt32
		if in.Body.ProjectID != "" {
			pid, err := types.Parse(in.Body.ProjectID)
			if err != nil {
				return nil, httpErr(apierrors.WsProjectNotFound)
			}
			proj, err := deps.Queries.FindProjectByPublicId(ctx, generated.FindProjectByPublicIdParams{
				WorkspaceID: ws.ID,
				PublicID:    pid,
			})
			if err != nil {
				if errors.Is(err, sql.ErrNoRows) {
					return nil, httpErr(apierrors.WsProjectNotFound)
				}
				return nil, httpErr(apierrors.InternalUnexpected)
			}
			projectID = sql.NullInt32{Int32: int32(proj.ID), Valid: true}
		}

		var parentLabelID sql.NullInt32
		if in.Body.ParentLabelID != "" {
			plid, err := types.Parse(in.Body.ParentLabelID)
			if err != nil {
				return nil, httpErr(apierrors.WsLabelNotFound)
			}
			parent, err := deps.Queries.FindLabelByPublicId(ctx, generated.FindLabelByPublicIdParams{
				WorkspaceID: ws.ID,
				PublicID:    plid,
			})
			if err != nil {
				if errors.Is(err, sql.ErrNoRows) {
					return nil, httpErr(apierrors.WsLabelNotFound)
				}
				return nil, httpErr(apierrors.InternalUnexpected)
			}
			parentLabelID = sql.NullInt32{Int32: int32(parent.ID), Valid: true}
		}

		if _, err := deps.Queries.CreateLabel(ctx, generated.CreateLabelParams{
			PublicID:      pub,
			WorkspaceID:   ws.ID,
			ProjectID:     projectID,
			ParentLabelID: parentLabelID,
			Name:          in.Body.Name,
			Color:         color,
			Description:   sql.NullString{String: in.Body.Description, Valid: in.Body.Description != ""},
			SortWeight:    0,
		}); err != nil {
			if isDuplicateEntry(err) {
				return nil, httpErr(apierrors.WsLabelNameAlreadyTaken)
			}
			return nil, httpErr(apierrors.InternalUnexpected)
		}

		_ = eventbus.Append(ctx, deps.DB, eventbus.Event{
			Type:        eventbus.LabelCreated,
			WorkspaceID: ws.ID,
			ActorUserID: actorPtr(ctx),
			Payload:     map[string]any{"labelId": pub.String()},
		})

		if deps.Audit != nil {
			if actorID, ok := middleware.ActorFromContext(ctx); ok {
				deps.Audit.Record(ctx, audit.Entry{
					Action:       "label.create",
					ActorID:      actorID,
					WorkspaceID:  ws.ID,
					ResourceType: "label",
					ResourceID:   pub.String(),
					Metadata:     map[string]any{"name": in.Body.Name},
				})
			}
		}

		row, err := deps.Queries.FindLabelByPublicId(ctx, generated.FindLabelByPublicIdParams{
			WorkspaceID: ws.ID,
			PublicID:    pub,
		})
		if err != nil {
			return nil, httpErr(apierrors.InternalUnexpected)
		}

		return &CreateLabelOutput{Body: mapLabel(row)}, nil
	}
}

// List handles GET /workspaces/{wsId}/labels.
func List(deps Deps) func(context.Context, *ListLabelsInput) (*ListLabelsOutput, error) {
	return func(ctx context.Context, in *ListLabelsInput) (*ListLabelsOutput, error) {
		ws, ok := middleware.WorkspaceFromContext(ctx)
		if !ok {
			return nil, httpErr(apierrors.WsWorkspaceNotFound)
		}

		limit := in.Limit
		if limit <= 0 {
			limit = 50
		}

		if in.ProjectID != "" {
			pid, err := types.Parse(in.ProjectID)
			if err != nil {
				return nil, httpErr(apierrors.WsProjectNotFound)
			}
			proj, err := deps.Queries.FindProjectByPublicId(ctx, generated.FindProjectByPublicIdParams{
				WorkspaceID: ws.ID,
				PublicID:    pid,
			})
			if err != nil {
				if errors.Is(err, sql.ErrNoRows) {
					return nil, httpErr(apierrors.WsProjectNotFound)
				}
				return nil, httpErr(apierrors.InternalUnexpected)
			}
			rows, err := deps.Queries.ListLabelsForProject(ctx, generated.ListLabelsForProjectParams{
				WorkspaceID: ws.ID,
				ProjectID:   sql.NullInt32{Int32: int32(proj.ID), Valid: true},
				Limit:       limit,
				Offset:      in.Offset,
			})
			if err != nil {
				return nil, httpErr(apierrors.InternalUnexpected)
			}
			out := &ListLabelsOutput{}
			out.Body.Labels = make([]Label, 0, len(rows))
			for _, r := range rows {
				out.Body.Labels = append(out.Body.Labels, mapProjectLabel(r))
			}
			if len(rows) > 0 {
				out.Body.Total = totalAsInt64(rows[0].Total)
			}
			return out, nil
		}

		rows, err := deps.Queries.ListLabelsForWorkspace(ctx, generated.ListLabelsForWorkspaceParams{
			WorkspaceID: ws.ID,
			Limit:       limit,
			Offset:      in.Offset,
		})
		if err != nil {
			return nil, httpErr(apierrors.InternalUnexpected)
		}

		out := &ListLabelsOutput{}
		out.Body.Labels = make([]Label, 0, len(rows))
		for _, r := range rows {
			out.Body.Labels = append(out.Body.Labels, mapWorkspaceLabel(r))
		}
		if len(rows) > 0 {
			out.Body.Total = totalAsInt64(rows[0].Total)
		}
		return out, nil
	}
}

// Get handles GET /workspaces/{wsId}/labels/{id}.
func Get(deps Deps) func(context.Context, *GetLabelInput) (*GetLabelOutput, error) {
	return func(ctx context.Context, in *GetLabelInput) (*GetLabelOutput, error) {
		ws, ok := middleware.WorkspaceFromContext(ctx)
		if !ok {
			return nil, httpErr(apierrors.WsWorkspaceNotFound)
		}

		pub, err := types.Parse(in.ID)
		if err != nil {
			return nil, httpErr(apierrors.WsLabelNotFound)
		}

		row, err := deps.Queries.FindLabelByPublicId(ctx, generated.FindLabelByPublicIdParams{
			WorkspaceID: ws.ID,
			PublicID:    pub,
		})
		if err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return nil, httpErr(apierrors.WsLabelNotFound)
			}
			return nil, httpErr(apierrors.InternalUnexpected)
		}

		return &GetLabelOutput{Body: mapLabel(row)}, nil
	}
}

// Patch handles PATCH /workspaces/{wsId}/labels/{id}.
func Patch(deps Deps) func(context.Context, *PatchLabelInput) (*PatchLabelOutput, error) {
	return func(ctx context.Context, in *PatchLabelInput) (*PatchLabelOutput, error) {
		ws, ok := middleware.WorkspaceFromContext(ctx)
		if !ok {
			return nil, httpErr(apierrors.WsWorkspaceNotFound)
		}

		pub, err := types.Parse(in.ID)
		if err != nil {
			return nil, httpErr(apierrors.WsLabelNotFound)
		}

		row, err := deps.Queries.FindLabelByPublicId(ctx, generated.FindLabelByPublicIdParams{
			WorkspaceID: ws.ID,
			PublicID:    pub,
		})
		if err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return nil, httpErr(apierrors.WsLabelNotFound)
			}
			return nil, httpErr(apierrors.InternalUnexpected)
		}

		name := row.Name
		if in.Body.Name != nil {
			name = *in.Body.Name
		}
		color := row.Color
		if in.Body.Color != nil {
			color = *in.Body.Color
		}
		desc := row.Description
		if in.Body.Description != nil {
			desc = sql.NullString{String: *in.Body.Description, Valid: *in.Body.Description != ""}
		}
		parentID := row.ParentLabelID
		if in.Body.ParentLabelID != nil {
			if *in.Body.ParentLabelID == "" {
				parentID = sql.NullInt32{}
			} else {
				plid, parseErr := types.Parse(*in.Body.ParentLabelID)
				if parseErr != nil {
					return nil, httpErr(apierrors.WsLabelNotFound)
				}
				parent, findErr := deps.Queries.FindLabelByPublicId(ctx, generated.FindLabelByPublicIdParams{
					WorkspaceID: ws.ID,
					PublicID:    plid,
				})
				if findErr != nil {
					if errors.Is(findErr, sql.ErrNoRows) {
						return nil, httpErr(apierrors.WsLabelNotFound)
					}
					return nil, httpErr(apierrors.InternalUnexpected)
				}
				parentID = sql.NullInt32{Int32: int32(parent.ID), Valid: true}
			}
		}
		sw := row.SortWeight
		if in.Body.SortWeight != nil {
			sw = *in.Body.SortWeight
		}

		if err := deps.Queries.UpdateLabel(ctx, generated.UpdateLabelParams{
			Name:          name,
			Color:         color,
			Description:   desc,
			ParentLabelID: parentID,
			SortWeight:    sw,
			WorkspaceID:   ws.ID,
			PublicID:      pub,
		}); err != nil {
			if isDuplicateEntry(err) {
				return nil, httpErr(apierrors.WsLabelNameAlreadyTaken)
			}
			return nil, httpErr(apierrors.InternalUnexpected)
		}

		_ = eventbus.Append(ctx, deps.DB, eventbus.Event{
			Type:        eventbus.LabelUpdated,
			WorkspaceID: ws.ID,
			ActorUserID: actorPtr(ctx),
			Payload:     map[string]any{"labelId": pub.String()},
		})

		if deps.Audit != nil {
			if actorID, ok := middleware.ActorFromContext(ctx); ok {
				deps.Audit.Record(ctx, audit.Entry{
					Action:       "label.update",
					ActorID:      actorID,
					WorkspaceID:  ws.ID,
					ResourceType: "label",
					ResourceID:   pub.String(),
				})
			}
		}

		updated, err := deps.Queries.FindLabelByPublicId(ctx, generated.FindLabelByPublicIdParams{
			WorkspaceID: ws.ID,
			PublicID:    pub,
		})
		if err != nil {
			return nil, httpErr(apierrors.InternalUnexpected)
		}

		return &PatchLabelOutput{Body: mapLabel(updated)}, nil
	}
}

// Disable handles DELETE /workspaces/{wsId}/labels/{id}.
func Disable(deps Deps) func(context.Context, *DisableLabelInput) (*DisableLabelOutput, error) {
	return func(ctx context.Context, in *DisableLabelInput) (*DisableLabelOutput, error) {
		ws, ok := middleware.WorkspaceFromContext(ctx)
		if !ok {
			return nil, httpErr(apierrors.WsWorkspaceNotFound)
		}

		pub, err := types.Parse(in.ID)
		if err != nil {
			return nil, httpErr(apierrors.WsLabelNotFound)
		}

		if err := deps.Queries.DisableLabel(ctx, generated.DisableLabelParams{
			WorkspaceID: ws.ID,
			PublicID:    pub,
		}); err != nil {
			return nil, httpErr(apierrors.InternalUnexpected)
		}

		_ = eventbus.Append(ctx, deps.DB, eventbus.Event{
			Type:        eventbus.LabelDisabled,
			WorkspaceID: ws.ID,
			ActorUserID: actorPtr(ctx),
			Payload:     map[string]any{"labelId": pub.String()},
		})

		if deps.Audit != nil {
			if actorID, ok := middleware.ActorFromContext(ctx); ok {
				deps.Audit.Record(ctx, audit.Entry{
					Action:       "label.disable",
					ActorID:      actorID,
					WorkspaceID:  ws.ID,
					ResourceType: "label",
					ResourceID:   pub.String(),
				})
			}
		}

		out := &DisableLabelOutput{}
		out.Body.Ok = true
		return out, nil
	}
}

// AddTaskLabel handles POST /tasks/{id}/labels.
func AddTaskLabel(deps Deps) func(context.Context, *AddTaskLabelInput) (*AddTaskLabelOutput, error) {
	return func(ctx context.Context, in *AddTaskLabelInput) (*AddTaskLabelOutput, error) {
		ws, ok := middleware.WorkspaceFromContext(ctx)
		if !ok {
			return nil, httpErr(apierrors.WsWorkspaceNotFound)
		}
		task, ok := middleware.TaskFromContext(ctx)
		if !ok {
			return nil, httpErr(apierrors.WsTaskNotFound)
		}

		labelPub, err := types.Parse(in.Body.LabelID)
		if err != nil {
			return nil, httpErr(apierrors.WsLabelNotFound)
		}

		label, err := deps.Queries.FindLabelByPublicId(ctx, generated.FindLabelByPublicIdParams{
			WorkspaceID: ws.ID,
			PublicID:    labelPub,
		})
		if err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return nil, httpErr(apierrors.WsLabelNotFound)
			}
			return nil, httpErr(apierrors.InternalUnexpected)
		}

		pub := types.New()
		if _, err := deps.Queries.CreateTaskLabel(ctx, generated.CreateTaskLabelParams{
			PublicID:    pub,
			WorkspaceID: ws.ID,
			TaskID:      task.ID,
			LabelID:     label.ID,
			SortWeight:  0,
		}); err != nil {
			if isDuplicateEntry(err) {
				return nil, httpErr(apierrors.WsLabelNameAlreadyTaken)
			}
			return nil, httpErr(apierrors.InternalUnexpected)
		}

		taskID := int64(task.ID)
		_ = eventbus.Append(ctx, deps.DB, eventbus.Event{
			Type:        eventbus.TaskLabelAdded,
			WorkspaceID: ws.ID,
			ActorUserID: actorPtr(ctx),
			TaskID:      &taskID,
			Payload:     map[string]any{"labelId": labelPub.String()},
		})

		return &AddTaskLabelOutput{Body: TaskLabel{
			ID:          labelPub.String(),
			Name:        label.Name,
			Color:       label.Color,
			Description: nullStr(label.Description),
			SortWeight:  0,
			CreatedAt:   label.CreatedAt.Unix(),
		}}, nil
	}
}

// ListTaskLabels handles GET /tasks/{id}/labels.
func ListTaskLabels(deps Deps) func(context.Context, *ListTaskLabelsInput) (*ListTaskLabelsOutput, error) {
	return func(ctx context.Context, in *ListTaskLabelsInput) (*ListTaskLabelsOutput, error) {
		ws, ok := middleware.WorkspaceFromContext(ctx)
		if !ok {
			return nil, httpErr(apierrors.WsWorkspaceNotFound)
		}
		task, ok := middleware.TaskFromContext(ctx)
		if !ok {
			return nil, httpErr(apierrors.WsTaskNotFound)
		}

		rows, err := deps.Queries.ListTaskLabels(ctx, generated.ListTaskLabelsParams{
			WorkspaceID: ws.ID,
			TaskID:      task.ID,
		})
		if err != nil {
			return nil, httpErr(apierrors.InternalUnexpected)
		}

		out := &ListTaskLabelsOutput{}
		out.Body.Labels = make([]TaskLabel, 0, len(rows))
		for _, r := range rows {
			out.Body.Labels = append(out.Body.Labels, mapTaskLabel(r))
		}
		return out, nil
	}
}

// RemoveTaskLabel handles DELETE /tasks/{id}/labels/{labelId}.
func RemoveTaskLabel(deps Deps) func(context.Context, *RemoveTaskLabelInput) (*RemoveTaskLabelOutput, error) {
	return func(ctx context.Context, in *RemoveTaskLabelInput) (*RemoveTaskLabelOutput, error) {
		ws, ok := middleware.WorkspaceFromContext(ctx)
		if !ok {
			return nil, httpErr(apierrors.WsWorkspaceNotFound)
		}
		task, ok := middleware.TaskFromContext(ctx)
		if !ok {
			return nil, httpErr(apierrors.WsTaskNotFound)
		}

		labelPub, err := types.Parse(in.LabelID)
		if err != nil {
			return nil, httpErr(apierrors.WsLabelNotFound)
		}

		label, err := deps.Queries.FindLabelByPublicId(ctx, generated.FindLabelByPublicIdParams{
			WorkspaceID: ws.ID,
			PublicID:    labelPub,
		})
		if err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return nil, httpErr(apierrors.WsLabelNotFound)
			}
			return nil, httpErr(apierrors.InternalUnexpected)
		}

		if err := deps.Queries.DisableTaskLabel(ctx, generated.DisableTaskLabelParams{
			WorkspaceID: ws.ID,
			TaskID:      task.ID,
			LabelID:     label.ID,
		}); err != nil {
			return nil, httpErr(apierrors.InternalUnexpected)
		}

		taskID := int64(task.ID)
		_ = eventbus.Append(ctx, deps.DB, eventbus.Event{
			Type:        eventbus.TaskLabelRemoved,
			WorkspaceID: ws.ID,
			ActorUserID: actorPtr(ctx),
			TaskID:      &taskID,
			Payload:     map[string]any{"labelId": labelPub.String()},
		})

		if deps.Audit != nil {
			if actorID, ok := middleware.ActorFromContext(ctx); ok {
				deps.Audit.Record(ctx, audit.Entry{
					Action:       "task.label.remove",
					ActorID:      actorID,
					WorkspaceID:  ws.ID,
					ResourceType: "task_label",
					ResourceID:   labelPub.String(),
				})
			}
		}

		out := &RemoveTaskLabelOutput{}
		out.Body.Ok = true
		return out, nil
	}
}
