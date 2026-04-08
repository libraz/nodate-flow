package tasks

import (
	"context"
	"database/sql"
	"time"

	"github.com/nodate-flow/nodate-flow/apps/api/internal/db/generated"
	"github.com/nodate-flow/nodate-flow/apps/api/internal/db/types"
	apierrors "github.com/nodate-flow/nodate-flow/apps/api/internal/errors"
	"github.com/nodate-flow/nodate-flow/apps/api/internal/eventbus"
	"github.com/nodate-flow/nodate-flow/apps/api/internal/http/middleware"
)

// AddAttachment handles POST /tasks/{id}/attachments. Phase 1 stores
// metadata only; the actual upload happens out-of-band.
func AddAttachment(deps Deps) func(context.Context, *AddAttachmentInput) (*AddAttachmentOutput, error) {
	return func(ctx context.Context, in *AddAttachmentInput) (*AddAttachmentOutput, error) {
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
		_ = eventbus.Append(ctx, deps.DB, eventbus.Event{
			Type:        "task.attachment.added",
			WorkspaceID: ws.ID,
			ActorUserID: actorPtr(ctx),
			TaskID:      &taskInternal,
			Payload: map[string]any{
				"taskId":       task.PublicID.String(),
				"attachmentId": pub.String(),
				"filename":     in.Body.Filename,
			},
		})
		return &AddAttachmentOutput{Body: Attachment{
			ID:          pub.String(),
			Filename:    in.Body.Filename,
			ContentType: in.Body.ContentType,
			ByteSize:    in.Body.ByteSize,
			StorageKey:  in.Body.StorageKey,
			CreatedAt:   time.Now(),
		}}, nil
	}
}

// ListAttachments handles GET /tasks/{id}/attachments.
func ListAttachments(deps Deps) func(context.Context, *ListAttachmentsInput) (*ListAttachmentsOutput, error) {
	return func(ctx context.Context, in *ListAttachmentsInput) (*ListAttachmentsOutput, error) {
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
		out := &ListAttachmentsOutput{}
		out.Body.Attachments = make([]Attachment, 0, len(rows))
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
func DeleteAttachment(deps Deps) func(context.Context, *DeleteAttachmentInput) (*DeleteAttachmentOutput, error) {
	return func(ctx context.Context, in *DeleteAttachmentInput) (*DeleteAttachmentOutput, error) {
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
		_ = eventbus.Append(ctx, deps.DB, eventbus.Event{
			Type:        "task.attachment.removed",
			WorkspaceID: ws.ID,
			ActorUserID: actorPtr(ctx),
			TaskID:      &taskInternal,
			Payload: map[string]any{
				"taskId":       task.PublicID.String(),
				"attachmentId": aid.String(),
			},
		})
		out := &DeleteAttachmentOutput{}
		out.Body.Ok = true
		return out, nil
	}
}
