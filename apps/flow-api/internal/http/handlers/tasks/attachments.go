package tasks

import (
	"context"
	"database/sql"
	"log/slog"

	"github.com/nodate-flow/nodate-flow/apps/flow-api/internal/audit"
	"github.com/nodate-flow/nodate-flow/apps/flow-api/internal/db/generated"
	"github.com/nodate-flow/nodate-flow/apps/flow-api/internal/db/types"
	apierrors "github.com/nodate-flow/nodate-flow/apps/flow-api/internal/errors"
	"github.com/nodate-flow/nodate-flow/apps/flow-api/internal/eventbus"
	"github.com/nodate-flow/nodate-flow/apps/flow-api/internal/http/handlers/handlerutil"
	"github.com/nodate-flow/nodate-flow/apps/flow-api/internal/http/middleware"
	"github.com/nodate-flow/nodate-flow/packages/go-shared/logutil"
)

// AddAttachment handles POST /tasks/{id}/attachments. Currently stores
// metadata only; the actual upload happens out-of-band.
func AddAttachment(deps Deps) func(context.Context, *AddTaskAttachmentInput) (*AddTaskAttachmentOutput, error) {
	return func(ctx context.Context, in *AddTaskAttachmentInput) (*AddTaskAttachmentOutput, error) {
		ws, ok := middleware.WorkspaceFromContext(ctx)
		if !ok {
			return nil, httpErr(apierrors.WsTaskNotFound)
		}
		task, ok := middleware.TaskFromContext(ctx)
		if !ok {
			return nil, httpErr(apierrors.WsTaskNotFound)
		}
		actorID, ok := middleware.ActorFromContext(ctx)
		if !ok {
			return nil, httpErr(apierrors.WsTaskAccessDenied)
		}
		pub := types.New()
		if _, err := deps.Queries.AddAttachment(ctx, generated.AddAttachmentParams{
			PublicID:       pub,
			WorkspaceID:    ws.ID,
			TaskID:         task.ID,
			UploaderID:     actorID,
			Filename:       in.Body.Filename,
			ContentType:    in.Body.ContentType,
			ByteSize:       in.Body.ByteSize,
			StorageKey:     in.Body.StorageKey,
			ChecksumSha256: sql.NullString{},
		}); err != nil {
			return nil, httpErr(apierrors.InternalUnexpected)
		}
		taskInternal := int64(task.ID)
		if err := eventbus.Append(ctx, deps.DB, eventbus.Event{
			Type:        eventbus.TaskAttachmentAdded,
			WorkspaceID: ws.ID,
			ActorUserID: actorPtr(ctx),
			TaskID:      &taskInternal,
			Payload: map[string]any{
				"taskId":       task.PublicID.String(),
				"attachmentId": pub.String(),
				"filename":     in.Body.Filename,
			},
		}); err != nil {
			slog.ErrorContext(ctx, "eventbus.Append failed",
				slog.Any("err", err),
				slog.String("handler", "tasks.AddAttachment"),
				slog.String("event_type", string(eventbus.TaskAttachmentAdded)),
				logutil.LogEntity("workspace", ws.PublicID),
				logutil.LogEntity("task", task.PublicID),
				slog.String("attachment_public_id", pub.String()),
			)
		}
		deps.Audit.Record(ctx, audit.Entry{
			Action:       "attachment.create",
			ActorID:      actorID,
			WorkspaceID:  ws.ID,
			ResourceType: "attachment",
			ResourceID:   pub.String(),
			Metadata:     map[string]any{"filename": in.Body.Filename},
		})
		return &AddTaskAttachmentOutput{Body: TaskAttachment{
			ID:          pub.String(),
			Filename:    in.Body.Filename,
			ContentType: in.Body.ContentType,
			ByteSize:    in.Body.ByteSize,
			StorageKey:  in.Body.StorageKey,
			CreatedAt:   handlerutil.NowUnix(),
		}}, nil
	}
}

// ListAttachments handles GET /tasks/{id}/attachments.
func ListAttachments(deps Deps) func(context.Context, *ListTaskAttachmentsInput) (*ListTaskAttachmentsOutput, error) {
	return func(ctx context.Context, in *ListTaskAttachmentsInput) (*ListTaskAttachmentsOutput, error) {
		ws, ok := middleware.WorkspaceFromContext(ctx)
		if !ok {
			return nil, httpErr(apierrors.WsTaskNotFound)
		}
		task, ok := middleware.TaskFromContext(ctx)
		if !ok {
			return nil, httpErr(apierrors.WsTaskNotFound)
		}
		limit := in.Limit
		if limit <= 0 {
			limit = 50
		}
		rows, err := deps.Queries.ListAttachmentsForTask(ctx, generated.ListAttachmentsForTaskParams{
			WorkspaceID: ws.ID,
			PublicID:    types.FromUUID(task.PublicID),
			Limit:       limit,
			Offset:      in.Offset,
		})
		if err != nil {
			return nil, httpErr(apierrors.InternalUnexpected)
		}
		out := &ListTaskAttachmentsOutput{}
		out.Body.Attachments = make([]TaskAttachment, 0, len(rows))
		for _, r := range rows {
			out.Body.Attachments = append(out.Body.Attachments, rowToAttachment(r))
		}
		if len(rows) > 0 {
			out.Body.Total = totalAsInt64(rows[0].Total)
		}
		return out, nil
	}
}

// DeleteAttachment handles DELETE /tasks/{id}/attachments/{aid}.
func DeleteAttachment(deps Deps) func(context.Context, *DeleteTaskAttachmentInput) (*DeleteTaskAttachmentOutput, error) {
	return func(ctx context.Context, in *DeleteTaskAttachmentInput) (*DeleteTaskAttachmentOutput, error) {
		ws, ok := middleware.WorkspaceFromContext(ctx)
		if !ok {
			return nil, httpErr(apierrors.WsTaskNotFound)
		}
		task, ok := middleware.TaskFromContext(ctx)
		if !ok {
			return nil, httpErr(apierrors.WsTaskNotFound)
		}
		aid, err := types.Parse(in.AID)
		if err != nil {
			return nil, httpErr(apierrors.ValidationPathParamInvalid)
		}
		if err := deps.Queries.DeleteAttachment(ctx, generated.DeleteAttachmentParams{
			WorkspaceID: ws.ID,
			PublicID:    aid,
		}); err != nil {
			return nil, httpErr(apierrors.InternalUnexpected)
		}
		taskInternal := int64(task.ID)
		if err := eventbus.Append(ctx, deps.DB, eventbus.Event{
			Type:        eventbus.TaskAttachmentRemoved,
			WorkspaceID: ws.ID,
			ActorUserID: actorPtr(ctx),
			TaskID:      &taskInternal,
			Payload: map[string]any{
				"taskId":       task.PublicID.String(),
				"attachmentId": aid.String(),
			},
		}); err != nil {
			slog.ErrorContext(ctx, "eventbus.Append failed",
				slog.Any("err", err),
				slog.String("handler", "tasks.DeleteAttachment"),
				slog.String("event_type", string(eventbus.TaskAttachmentRemoved)),
				logutil.LogEntity("workspace", ws.PublicID),
				logutil.LogEntity("task", task.PublicID),
				slog.String("attachment_public_id", aid.String()),
			)
		}
		if actorID, ok := middleware.ActorFromContext(ctx); ok {
			deps.Audit.Record(ctx, audit.Entry{
				Action:       "attachment.delete",
				ActorID:      actorID,
				WorkspaceID:  ws.ID,
				ResourceType: "attachment",
				ResourceID:   aid.String(),
			})
		}
		out := &DeleteTaskAttachmentOutput{}
		out.Body.Ok = true
		return out, nil
	}
}
