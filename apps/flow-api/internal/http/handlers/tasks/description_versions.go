package tasks

import (
	"context"
	"database/sql"

	"github.com/libraz/nodate-flow/apps/flow-api/internal/ai/embed"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/audit"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/dbretry"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/generated"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/types"
	apierrors "github.com/libraz/nodate-flow/apps/flow-api/internal/errors"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/eventbus"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/http/handlers/handlerutil"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/http/middleware"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/taskdesc"
	"github.com/libraz/nodate-flow/packages/go-shared/apierr"
)

// mapDescriptionVersionRow converts a ListDescriptionVersionsRow to the DTO.
func mapDescriptionVersionRow(r generated.ListDescriptionVersionsRow) DescriptionVersion {
	return DescriptionVersion{
		ID:                r.PublicID.String(),
		VersionNumber:     int(r.VersionNumber),
		AuthorID:          handlerutil.PublicIDOrEmpty(r.AuthorPublicID),
		AuthorDisplayName: nullStr(r.AuthorDisplayName),
		BodyLength:        int(r.BodyLength),
		CreatedAt:         r.CreatedAt.Unix(),
	}
}

// mapDescriptionVersionFull converts a FindDescriptionVersionRow to the full DTO.
func mapDescriptionVersionFull(r generated.FindDescriptionVersionRow) DescriptionVersionFull {
	return DescriptionVersionFull{
		DescriptionVersion: DescriptionVersion{
			ID:                r.PublicID.String(),
			VersionNumber:     int(r.VersionNumber),
			AuthorID:          handlerutil.PublicIDOrEmpty(r.AuthorPublicID),
			AuthorDisplayName: nullStr(r.AuthorDisplayName),
			BodyLength:        int(r.BodyLength),
			CreatedAt:         r.CreatedAt.Unix(),
		},
		Body: r.Body,
	}
}

// ListDescriptionVersions handles GET /tasks/{id}/description-history.
func ListDescriptionVersions(deps Deps) func(context.Context, *ListDescriptionVersionsInput) (*ListDescriptionVersionsOutput, error) {
	return func(ctx context.Context, _ *ListDescriptionVersionsInput) (*ListDescriptionVersionsOutput, error) {
		ws, ok := middleware.WorkspaceFromContext(ctx)
		if !ok {
			return nil, httpErr(apierrors.WsWorkspaceNotFound)
		}
		task, ok := middleware.TaskFromContext(ctx)
		if !ok {
			return nil, httpErr(apierrors.WsTaskNotFound)
		}

		rows, err := deps.Queries.ListDescriptionVersions(ctx, generated.ListDescriptionVersionsParams{
			WorkspaceID: ws.ID,
			TaskID:      task.ID,
		})
		if err != nil {
			return nil, httpErr(apierrors.InternalUnexpected)
		}

		out := &ListDescriptionVersionsOutput{}
		out.Body.Versions = make([]DescriptionVersion, 0, len(rows))
		for _, r := range rows {
			out.Body.Versions = append(out.Body.Versions, mapDescriptionVersionRow(r))
		}
		return out, nil
	}
}

// GetDescriptionVersion handles GET /tasks/{id}/description-history/{versionId}.
func GetDescriptionVersion(deps Deps) func(context.Context, *GetDescriptionVersionInput) (*GetDescriptionVersionOutput, error) {
	return func(ctx context.Context, in *GetDescriptionVersionInput) (*GetDescriptionVersionOutput, error) {
		ws, ok := middleware.WorkspaceFromContext(ctx)
		if !ok {
			return nil, httpErr(apierrors.WsWorkspaceNotFound)
		}
		task, ok := middleware.TaskFromContext(ctx)
		if !ok {
			return nil, httpErr(apierrors.WsTaskNotFound)
		}

		versionPub, err := types.Parse(in.VersionID)
		if err != nil {
			return nil, httpErr(apierrors.WsDescriptionVersionNotFound)
		}

		row, err := deps.Queries.FindDescriptionVersion(ctx, generated.FindDescriptionVersionParams{
			WorkspaceID: ws.ID,
			TaskID:      task.ID,
			PublicID:    versionPub,
		})
		if err != nil {
			return nil, httpErr(apierr.SpecForErrNoRows(err, apierrors.WsDescriptionVersionNotFound, apierrors.InternalUnexpected))
		}

		return &GetDescriptionVersionOutput{Body: mapDescriptionVersionFull(row)}, nil
	}
}

// RestoreDescriptionVersion handles POST /tasks/{id}/description-history/{versionId}/restore.
//
// task-precondition: date-order not-applicable — the update writes the task's
// stored due and start dates back unchanged, having read both from the row it
// is restoring into, so it cannot put them out of order. Rows predating the
// rule may already hold an inverted pair; checking here would refuse a
// description restore over dates the restore does not touch.
func RestoreDescriptionVersion(deps Deps) func(context.Context, *RestoreDescriptionVersionInput) (*RestoreDescriptionVersionOutput, error) {
	return func(ctx context.Context, in *RestoreDescriptionVersionInput) (*RestoreDescriptionVersionOutput, error) {
		ws, ok := middleware.WorkspaceFromContext(ctx)
		if !ok {
			return nil, httpErr(apierrors.WsWorkspaceNotFound)
		}
		task, ok := middleware.TaskFromContext(ctx)
		if !ok {
			return nil, httpErr(apierrors.WsTaskNotFound)
		}

		versionPub, err := types.Parse(in.VersionID)
		if err != nil {
			return nil, httpErr(apierrors.WsDescriptionVersionNotFound)
		}

		// Find the version to restore.
		version, err := deps.Queries.FindDescriptionVersion(ctx, generated.FindDescriptionVersionParams{
			WorkspaceID: ws.ID,
			TaskID:      task.ID,
			PublicID:    versionPub,
		})
		if err != nil {
			return nil, httpErr(apierr.SpecForErrNoRows(err, apierrors.WsDescriptionVersionNotFound, apierrors.InternalUnexpected))
		}

		actorID, _ := middleware.ActorFromContext(ctx)

		// Update the task description, snapshot it and re-derive the
		// mentions it names, all on one commit boundary. dbretry.InTx rather
		// than a bare transaction: the mention sync takes a commit boundary,
		// which is what lets the event it appends defer its fan-out until
		// the description it describes is observable.
		var taskRow generated.FindTaskByPublicIdRow
		var restored taskdesc.Version
		if err := dbretry.InTx(ctx, deps.DB, "tasks.RestoreDescriptionVersion", nil, func(ctx context.Context, tx *dbretry.Tx) error {
			qtx := deps.Queries.WithTx(tx.RawTx())

			// Get the task public ID for the update query.
			var rerr error
			taskRow, rerr = qtx.FindTaskByPublicId(ctx, generated.FindTaskByPublicIdParams{
				WorkspaceID: ws.ID,
				PublicID:    types.FromUUID(task.PublicID),
			})
			if rerr != nil {
				return rerr
			}

			// Update the task description.
			// Not an existence check: restoring a version whose body already
			// matches the task changes nothing and MySQL counts zero. The task
			// was read into taskRow above.
			if _, err := qtx.UpdateTask(ctx, generated.UpdateTaskParams{
				Title:           taskRow.Title,
				Description:     sql.NullString{String: version.Body, Valid: version.Body != ""},
				Priority:        taskRow.Priority,
				DueOn:           taskRow.DueOn,
				StartedOn:       taskRow.StartedOn,
				SortWeight:      taskRow.SortWeight,
				Visibility:      taskRow.Visibility,
				UpdatedByUserID: sql.NullInt32{Int32: int32(actorID), Valid: actorID != 0}, //#nosec G115 -- actor user id sourced from session, fits int32 within realistic deployments
				WorkspaceID:     ws.ID,
				PublicID:        types.FromUUID(task.PublicID),
			}); err != nil {
				return err
			}

			// The restored body becomes the newest version rather than rewinding
			// the history to the one it came from.
			restored, rerr = taskdesc.Snapshot(ctx, qtx, ws.ID, task.ID,
				sql.NullInt32{Int32: int32(actorID), Valid: true}, //#nosec G115 -- actor user id sourced from session, fits int32 within realistic deployments
				version.Body,
			)
			if rerr != nil {
				return rerr
			}
			// The restored body is the one the task now carries, so it is the
			// body the mentions table has to agree with — including when the
			// version being restored to named nobody.
			return syncDescriptionMentions(ctx, tx, ws.ID, task, actorPtr(ctx), version.Body)
		}); err != nil {
			return nil, httpErr(apierrors.InternalUnexpected)
		}

		taskIDInt64 := int64(task.ID)
		eventbus.AppendBestEffort(ctx, dbretry.AutoCommit(deps.DB), eventbus.Event{
			Type:        eventbus.DescriptionVersionRestored,
			WorkspaceID: ws.ID,
			ActorUserID: actorPtr(ctx),
			TaskID:      &taskIDInt64,
			Payload: map[string]any{
				"taskId":        types.FromUUID(task.PublicID).String(),
				"restoredFrom":  versionPub.String(),
				"newVersionId":  restored.PublicID.String(),
				"versionNumber": restored.Number,
			},
		}, "tasks.RestoreDescriptionVersion")

		if deps.Audit != nil {
			deps.Audit.Record(ctx, audit.Entry{
				Action:       "description_version.restore",
				ActorID:      actorID,
				WorkspaceID:  ws.ID,
				ResourceType: "task",
				ResourceID:   types.FromUUID(task.PublicID).String(),
				Metadata: map[string]any{
					"restoredVersionId": versionPub.String(),
					"newVersionId":      restored.PublicID.String(),
				},
			})
		}

		// A restore replaces the description the task is stored under, so
		// the embedding follows it. The title is untouched by this write,
		// so the row's own title is what pairs with the restored body.
		embed.RefreshTaskAfterCommit(ctx, deps.Embedder, ws.ID, task.ID, taskRow.Title, version.Body)

		out := &RestoreDescriptionVersionOutput{}
		out.Body.Ok = true
		return out, nil
	}
}
