package labels

import (
	"context"
	"database/sql"
	stderrors "errors"

	"github.com/libraz/nodate-flow/apps/flow-api/internal/acl"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/dbretry"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/generated"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/types"
	apierrors "github.com/libraz/nodate-flow/apps/flow-api/internal/errors"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/eventbus"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/http/handlers/handlerutil"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/http/middleware"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/labelkit"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/mutationlog"
	"github.com/libraz/nodate-flow/packages/go-shared/apierr"
)

// checkProjectFloor holds a write to a project-scoped label to the
// project editor floor.
func checkProjectFloor(ctx context.Context, deps Deps, wsID, projectID uint32) *apierrors.Spec {
	actorID, _ := middleware.ActorFromContext(ctx)
	return handlerutil.RequireProjectRole(ctx, deps.DB, wsID, projectID, actorID, acl.ProjectRoleEditor)
}

// Create handles POST /workspaces/{wsId}/labels.
func Create(deps Deps) func(context.Context, *CreateLabelInput) (*CreateLabelOutput, error) {
	return func(ctx context.Context, in *CreateLabelInput) (*CreateLabelOutput, error) {
		ws, ok := middleware.WorkspaceFromContext(ctx)
		if !ok {
			return nil, httpErr(apierrors.WsWorkspaceNotFound)
		}

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
				return nil, httpErr(apierr.SpecForErrNoRows(err, apierrors.WsProjectNotFound, apierrors.InternalUnexpected))
			}
			if spec := checkProjectFloor(ctx, deps, ws.ID, proj.ID); spec != nil {
				return nil, httpErr(spec)
			}
			projectID = sql.NullInt32{Int32: int32(proj.ID), Valid: true} //#nosec G115 -- project_id is projects.id (BIGINT UNSIGNED), fits int32 within realistic deployments
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
				return nil, httpErr(apierr.SpecForErrNoRows(err, apierrors.WsLabelNotFound, apierrors.InternalUnexpected))
			}
			if spec, err := CheckParent(ctx, deps.DB, ws.ID, 0, projectID, parent.ProjectID, parent.ID); err != nil {
				return nil, httpErr(apierrors.InternalUnexpected)
			} else if spec != nil {
				return nil, httpErr(spec)
			}
			parentLabelID = sql.NullInt32{Int32: int32(parent.ID), Valid: true} //#nosec G115 -- parent_label_id is task_labels.id (BIGINT UNSIGNED), fits int32 within realistic deployments
		}

		actorID, _ := middleware.ActorFromContext(ctx)
		actor := mutationlog.Actor{UserID: actorID, WorkspaceID: ws.ID}

		var created labelkit.Label
		if err := dbretry.InTx(ctx, deps.DB, "labels.Create", nil, func(ctx context.Context, tx *dbretry.Tx) error {
			l, err := labelkit.Create(ctx, tx, labelkit.CreateArgs{
				WorkspaceID: ws.ID,
				ProjectID:   sql.NullInt64{Int64: int64(projectID.Int32), Valid: projectID.Valid},
				ParentID:    sql.NullInt64{Int64: int64(parentLabelID.Int32), Valid: parentLabelID.Valid},
				Name:        in.Body.Name,
				Color:       color,
				Description: in.Body.Description,
				Actor:       actor,
			})
			created = l
			return err
		}); err != nil {
			if stderrors.Is(err, labelkit.ErrNameTaken) {
				return nil, httpErr(apierrors.WsLabelNameAlreadyTaken)
			}
			return nil, httpErr(apierrors.InternalUnexpected)
		}
		pub := created.PublicID
		deps.Mutations.RecordTxAudit(ctx, actor, mutationlog.Mutation{
			AuditAction:  "label.create",
			ResourceType: "label",
			ResourceID:   pub.String(),
			Payload:      map[string]any{"labelId": pub.String(), "name": in.Body.Name},
			CallSite:     "labels.Create (labelkit.Create appended the event)",
		})

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
				return nil, httpErr(apierr.SpecForErrNoRows(err, apierrors.WsProjectNotFound, apierrors.InternalUnexpected))
			}
			rows, err := deps.Queries.ListLabelsForProject(ctx, generated.ListLabelsForProjectParams{
				WorkspaceID: ws.ID,
				ProjectID:   sql.NullInt32{Int32: int32(proj.ID), Valid: true}, //#nosec G115 -- project_id is projects.id (BIGINT UNSIGNED), fits int32 within realistic deployments
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
			return nil, httpErr(apierr.SpecForErrNoRows(err, apierrors.WsLabelNotFound, apierrors.InternalUnexpected))
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
			return nil, httpErr(apierr.SpecForErrNoRows(err, apierrors.WsLabelNotFound, apierrors.InternalUnexpected))
		}
		if row.ProjectID.Valid {
			if spec := checkProjectFloor(ctx, deps, ws.ID, uint32(row.ProjectID.Int32)); spec != nil { //#nosec G115 -- projects.id, positive by construction
				return nil, httpErr(spec)
			}
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
					return nil, httpErr(apierr.SpecForErrNoRows(findErr, apierrors.WsLabelNotFound, apierrors.InternalUnexpected))
				}
				if spec, chkErr := CheckParent(ctx, deps.DB, ws.ID, row.ID, row.ProjectID, parent.ProjectID, parent.ID); chkErr != nil {
					return nil, httpErr(apierrors.InternalUnexpected)
				} else if spec != nil {
					return nil, httpErr(spec)
				}
				parentID = sql.NullInt32{Int32: int32(parent.ID), Valid: true} //#nosec G115 -- parent label id is task_labels.id (BIGINT UNSIGNED), fits int32 within realistic deployments
			}
		}
		sw := row.SortWeight
		if in.Body.SortWeight != nil {
			sw = *in.Body.SortWeight
		}

		// Not an existence check: a PATCH re-sending the label's current
		// name / colour changes nothing and MySQL counts zero. The re-read
		// below is what fails if the label is gone.
		actorID, _ := middleware.ActorFromContext(ctx)
		if err := dbretry.InTx(ctx, deps.DB, "labels.Patch", nil, func(ctx context.Context, tx *dbretry.Tx) error {
			if _, err := generated.New(tx).UpdateLabel(ctx, generated.UpdateLabelParams{
				Name:          name,
				Color:         color,
				Description:   desc,
				ParentLabelID: parentID,
				SortWeight:    sw,
				WorkspaceID:   ws.ID,
				PublicID:      pub,
			}); err != nil {
				return err
			}
			return deps.Mutations.RecordInTx(ctx, tx, mutationlog.Actor{UserID: actorID, WorkspaceID: ws.ID}, mutationlog.Mutation{
				EventType:    eventbus.LabelUpdated,
				AuditAction:  "label.update",
				ResourceType: "label",
				ResourceID:   pub.String(),
				Payload:      map[string]any{"labelId": pub.String()},
				CallSite:     "labels.Patch",
			})
		}); err != nil {
			if isDuplicateEntry(err) {
				return nil, httpErr(apierrors.WsLabelNameAlreadyTaken)
			}
			return nil, httpErr(apierrors.InternalUnexpected)
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

		// Scoped to the workspace and to live labels, so a zero count means
		// there is no such label to delete here.
		actorID, _ := middleware.ActorFromContext(ctx)
		var missing bool
		if err := dbretry.InTx(ctx, deps.DB, "labels.Disable", nil, func(ctx context.Context, tx *dbretry.Tx) error {
			rows, err := generated.New(tx).DisableLabel(ctx, generated.DisableLabelParams{
				WorkspaceID: ws.ID,
				PublicID:    pub,
			})
			if err != nil {
				return err
			}
			if missing = rows == 0; missing {
				return nil
			}
			return deps.Mutations.RecordInTx(ctx, tx, mutationlog.Actor{UserID: actorID, WorkspaceID: ws.ID}, mutationlog.Mutation{
				EventType:    eventbus.LabelDisabled,
				AuditAction:  "label.disable",
				ResourceType: "label",
				ResourceID:   pub.String(),
				Payload:      map[string]any{"labelId": pub.String()},
				CallSite:     "labels.Disable",
			})
		}); err != nil {
			return nil, httpErr(apierrors.InternalUnexpected)
		}
		if missing {
			return nil, httpErr(apierrors.WsLabelNotFound)
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
			return nil, httpErr(apierr.SpecForErrNoRows(err, apierrors.WsLabelNotFound, apierrors.InternalUnexpected))
		}

		var taskProject uint32
		if err := deps.DB.QueryRowContext(ctx,
			`SELECT project_id FROM tasks WHERE id = ? AND workspace_id = ? LIMIT 1`,
			task.ID, ws.ID,
		).Scan(&taskProject); err != nil {
			return nil, httpErr(apierr.SpecForErrNoRows(err, apierrors.WsTaskNotFound, apierrors.InternalUnexpected))
		}
		if spec := CheckAttachScope(label.ProjectID, taskProject); spec != nil {
			return nil, httpErr(spec)
		}

		actorID, _ := middleware.ActorFromContext(ctx)
		actor := mutationlog.Actor{UserID: actorID, WorkspaceID: ws.ID}
		if err := dbretry.InTx(ctx, deps.DB, "labels.AddTaskLabel", nil, func(ctx context.Context, tx *dbretry.Tx) error {
			return labelkit.Attach(ctx, tx, labelkit.AttachArgs{
				TaskID:        task.ID,
				LabelID:       label.ID,
				LabelPublicID: labelPub,
				Actor:         actor,
			})
		}); err != nil {
			if stderrors.Is(err, labelkit.ErrAlreadyAttached) {
				return nil, httpErr(apierrors.WsLabelAlreadyAttached)
			}
			return nil, httpErr(apierrors.InternalUnexpected)
		}
		taskID := int64(task.ID)
		deps.Mutations.RecordTxAudit(ctx, actor, mutationlog.Mutation{
			AuditAction:  "task.label.add",
			ResourceType: "task_label",
			ResourceID:   labelPub.String(),
			TaskID:       &taskID,
			Payload:      map[string]any{"labelId": labelPub.String()},
			CallSite:     "labels.AddTaskLabel (labelkit.Attach appended the event)",
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
	return func(ctx context.Context, _ *ListTaskLabelsInput) (*ListTaskLabelsOutput, error) {
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
			return nil, httpErr(apierr.SpecForErrNoRows(err, apierrors.WsLabelNotFound, apierrors.InternalUnexpected))
		}

		actorID, _ := middleware.ActorFromContext(ctx)
		actor := mutationlog.Actor{UserID: actorID, WorkspaceID: ws.ID}
		err = dbretry.InTx(ctx, deps.DB, "labels.RemoveTaskLabel", nil, func(ctx context.Context, tx *dbretry.Tx) error {
			return labelkit.Detach(ctx, tx, labelkit.AttachArgs{
				TaskID:        task.ID,
				LabelID:       label.ID,
				LabelPublicID: labelPub,
				Actor:         actor,
			})
		})
		// Detaching a label the task does not carry asks for a state that
		// already holds: success, with nothing written to record.
		if err != nil && !stderrors.Is(err, labelkit.ErrNotAttached) {
			return nil, httpErr(apierrors.InternalUnexpected)
		}
		if err == nil {
			taskID := int64(task.ID)
			deps.Mutations.RecordTxAudit(ctx, actor, mutationlog.Mutation{
				AuditAction:  "task.label.remove",
				ResourceType: "task_label",
				ResourceID:   labelPub.String(),
				TaskID:       &taskID,
				Payload:      map[string]any{"labelId": labelPub.String()},
				CallSite:     "labels.RemoveTaskLabel (labelkit.Detach appended the event)",
			})
		}

		out := &RemoveTaskLabelOutput{}
		out.Body.Ok = true
		return out, nil
	}
}
