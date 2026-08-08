package tasks

import (
	"context"
	"database/sql"
	"log/slog"

	"github.com/libraz/nodate-flow/apps/flow-api/internal/audit"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/generated"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/types"
	apierrors "github.com/libraz/nodate-flow/apps/flow-api/internal/errors"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/eventbus"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/http/handlers/handlerutil"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/http/middleware"
	"github.com/libraz/nodate-flow/packages/go-shared/apierr"
	"github.com/libraz/nodate-flow/packages/go-shared/logutil"
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

		// Update the task description and create a new version snapshot,
		// all within a transaction.
		tx, err := deps.DB.BeginTx(ctx, nil)
		if err != nil {
			return nil, httpErr(apierrors.InternalUnexpected)
		}
		defer tx.Rollback() //nolint:errcheck
		qtx := deps.Queries.WithTx(tx)

		// Get the task public ID for the update query.
		taskRow, err := qtx.FindTaskByPublicId(ctx, generated.FindTaskByPublicIdParams{
			WorkspaceID: ws.ID,
			PublicID:    types.FromUUID(task.PublicID),
		})
		if err != nil {
			return nil, httpErr(apierrors.InternalUnexpected)
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
			return nil, httpErr(apierrors.InternalUnexpected)
		}

		// Get the next version number.
		nextVer, err := qtx.NextDescriptionVersionNumber(ctx, task.ID)
		if err != nil {
			return nil, httpErr(apierrors.InternalUnexpected)
		}

		// Create a new version snapshot for the restored content.
		newPub := types.New()
		if _, err := qtx.CreateDescriptionVersion(ctx, generated.CreateDescriptionVersionParams{
			PublicID:      newPub,
			WorkspaceID:   ws.ID,
			TaskID:        task.ID,
			AuthorUserID:  sql.NullInt32{Int32: int32(actorID), Valid: true}, //#nosec G115 -- actor user id sourced from session, fits int32 within realistic deployments
			VersionNumber: uint32(nextVer),                                   //#nosec G115 -- per-task version sequence, fits uint32
			Body:          version.Body,
			BodyLength:    uint32(len(version.Body)), //#nosec G115 -- description body length capped at 50KB by handler validation, fits uint32
		}); err != nil {
			return nil, httpErr(apierrors.InternalUnexpected)
		}

		if err := tx.Commit(); err != nil {
			return nil, httpErr(apierrors.InternalUnexpected)
		}

		taskIDInt64 := int64(task.ID)
		if err := eventbus.Append(ctx, deps.DB, eventbus.Event{
			Type:        eventbus.DescriptionVersionRestored,
			WorkspaceID: ws.ID,
			ActorUserID: actorPtr(ctx),
			TaskID:      &taskIDInt64,
			Payload: map[string]any{
				"taskId":        types.FromUUID(task.PublicID).String(),
				"restoredFrom":  versionPub.String(),
				"newVersionId":  newPub.String(),
				"versionNumber": nextVer,
			},
		}); err != nil {
			slog.ErrorContext(ctx, "eventbus.Append failed",
				slog.Any("err", err),
				slog.String("handler", "tasks.RestoreDescriptionVersion"),
				slog.String("event_type", string(eventbus.DescriptionVersionRestored)),
				logutil.LogEntity("workspace", ws.PublicID),
				logutil.LogEntity("task", task.PublicID),
				slog.String("version_id", versionPub.String()),
				slog.String("new_version_id", newPub.String()),
			)
		}

		if deps.Audit != nil {
			deps.Audit.Record(ctx, audit.Entry{
				Action:       "description_version.restore",
				ActorID:      actorID,
				WorkspaceID:  ws.ID,
				ResourceType: "task",
				ResourceID:   types.FromUUID(task.PublicID).String(),
				Metadata: map[string]any{
					"restoredVersionId": versionPub.String(),
					"newVersionId":      newPub.String(),
				},
			})
		}

		out := &RestoreDescriptionVersionOutput{}
		out.Body.Ok = true
		return out, nil
	}
}
