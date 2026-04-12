package tasks

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/nodate-flow/nodate-flow/apps/api/internal/db/generated"
	"github.com/nodate-flow/nodate-flow/apps/api/internal/db/types"
	apierrors "github.com/nodate-flow/nodate-flow/apps/api/internal/errors"
	"github.com/nodate-flow/nodate-flow/apps/api/internal/eventbus"
	"github.com/nodate-flow/nodate-flow/apps/api/internal/http/middleware"
)

const presignExpiry = 15 * time.Minute

// PresignUpload handles POST /tasks/{id}/attachments/presign. It
// creates an attachment metadata row and returns a presigned PUT URL
// that the client uses to upload the file directly to object storage.
func PresignUpload(deps Deps) func(context.Context, *PresignUploadInput) (*PresignUploadOutput, error) {
	return func(ctx context.Context, in *PresignUploadInput) (*PresignUploadOutput, error) {
		if deps.Storage == nil {
			return nil, httpErr(apierrors.InternalUnexpected)
		}
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
		storageKey := fmt.Sprintf("attachments/%s/%s/%s/%s",
			ws.PublicID.String(), task.PublicID.String(), pub.String(), in.Body.Filename)

		if _, err := deps.Queries.AddAttachment(ctx, generated.AddAttachmentParams{
			PublicID:       pub,
			WorkspaceID:    ws.ID,
			TaskID:         task.ID,
			UploaderID:     actorID,
			Filename:       in.Body.Filename,
			ContentType:    in.Body.ContentType,
			ByteSize:       in.Body.ByteSize,
			StorageKey:     storageKey,
			ChecksumSha256: sql.NullString{},
		}); err != nil {
			return nil, httpErr(apierrors.InternalUnexpected)
		}

		uploadURL, err := deps.Storage.PresignPut(ctx, storageKey, presignExpiry)
		if err != nil {
			return nil, httpErr(apierrors.InternalUnexpected)
		}

		taskInternal := int64(task.ID)
		_ = eventbus.Append(ctx, deps.DB, eventbus.Event{
			Type:        eventbus.TaskAttachmentAdded,
			WorkspaceID: ws.ID,
			ActorUserID: actorPtr(ctx),
			TaskID:      &taskInternal,
			Payload: map[string]any{
				"taskId":       task.PublicID.String(),
				"attachmentId": pub.String(),
				"filename":     in.Body.Filename,
			},
		})

		return &PresignUploadOutput{Body: PresignUploadOutputBody{
			UploadURL:    uploadURL,
			StorageKey:   storageKey,
			AttachmentID: pub.String(),
		}}, nil
	}
}

// DownloadAttachment handles GET /tasks/{id}/attachments/{aid}/download.
// It returns a presigned GET URL with Content-Disposition: attachment.
func DownloadAttachment(deps Deps) func(context.Context, *DownloadAttachmentInput) (*DownloadAttachmentOutput, error) {
	return func(ctx context.Context, in *DownloadAttachmentInput) (*DownloadAttachmentOutput, error) {
		if deps.Storage == nil {
			return nil, httpErr(apierrors.InternalUnexpected)
		}
		ws, ok := middleware.WorkspaceFromContext(ctx)
		if !ok {
			return nil, httpErr(apierrors.WsTaskNotFound)
		}

		aid, err := types.Parse(in.AID)
		if err != nil {
			return nil, httpErr(apierrors.ValidationPathParamInvalid)
		}

		row, err := deps.Queries.GetAttachmentByPublicID(ctx, generated.GetAttachmentByPublicIDParams{
			WorkspaceID: ws.ID,
			PublicID:    aid,
		})
		if err != nil {
			return nil, httpErr(apierrors.WsTaskNotFound)
		}

		downloadURL, err := deps.Storage.PresignGet(ctx, row.StorageKey, row.Filename, presignExpiry)
		if err != nil {
			return nil, httpErr(apierrors.InternalUnexpected)
		}

		return &DownloadAttachmentOutput{Body: DownloadAttachmentOutputBody{
			DownloadURL: downloadURL,
		}}, nil
	}
}
