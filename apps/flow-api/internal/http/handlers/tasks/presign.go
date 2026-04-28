package tasks

import (
	"context"
	"database/sql"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/nodate-flow/nodate-flow/apps/flow-api/internal/db/generated"
	"github.com/nodate-flow/nodate-flow/apps/flow-api/internal/db/types"
	apierrors "github.com/nodate-flow/nodate-flow/apps/flow-api/internal/errors"
	"github.com/nodate-flow/nodate-flow/apps/flow-api/internal/eventbus"
	"github.com/nodate-flow/nodate-flow/apps/flow-api/internal/http/handlers/handlerutil"
	"github.com/nodate-flow/nodate-flow/apps/flow-api/internal/http/middleware"
)

const presignExpiry = 15 * time.Minute

// maxFileSize is the per-file upload limit (100 MB).
const maxFileSize = 100 * 1024 * 1024

// allowedMIMEPrefixes lists safe MIME type prefixes for uploads.
// Intentionally excludes application/octet-stream (catch-all) and
// limits application/vnd.ms-* to safe Office types.
var allowedMIMEPrefixes = []string{
	"image/",
	"text/",
	"application/pdf",
	"application/json",
	"application/xml",
	"application/zip",
	"application/gzip",
	"application/x-tar",
	"application/vnd.openxmlformats-officedocument",
	"application/vnd.ms-excel",
	"application/vnd.ms-powerpoint",
	"application/vnd.oasis.opendocument",
	"video/",
	"audio/",
}

// blockedExtensions rejects dangerous file extensions regardless of
// the declared MIME type.
var blockedExtensions = []string{
	".exe", ".dll", ".bat", ".cmd", ".com", ".scr", ".pif",
	".msi", ".msp", ".mst", ".vbs", ".vbe", ".js", ".jse",
	".wsf", ".wsh", ".ps1", ".psm1",
}

func isAllowedContentType(ct string) bool {
	ct = strings.ToLower(strings.TrimSpace(ct))
	for _, prefix := range allowedMIMEPrefixes {
		if strings.HasPrefix(ct, prefix) {
			return true
		}
	}
	return false
}

func hasBlockedExtension(filename string) bool {
	lower := strings.ToLower(filename)
	for _, ext := range blockedExtensions {
		if strings.HasSuffix(lower, ext) {
			return true
		}
	}
	return false
}

// PresignUpload handles POST /tasks/{id}/attachments/presign. It
// creates an attachment metadata row and returns a presigned PUT URL
// that the client uses to upload the file directly to object storage.
func PresignUpload(deps Deps) func(context.Context, *PresignUploadInput) (*PresignUploadOutput, error) {
	return func(ctx context.Context, in *PresignUploadInput) (*PresignUploadOutput, error) {
		if deps.Storage == nil {
			return nil, httpErr(apierrors.InternalStorageNotConfigured)
		}
		if !isAllowedContentType(in.Body.ContentType) {
			return nil, httpErr(apierrors.ValidationFileTypeNotAllowed)
		}
		if hasBlockedExtension(in.Body.Filename) {
			return nil, httpErr(apierrors.ValidationFileTypeNotAllowed)
		}
		if in.Body.ByteSize > maxFileSize {
			return nil, httpErr(apierrors.ValidationFileTooLarge)
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
			TaskID:         handlerutil.NullInt32From(task.ID),
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
				slog.String("handler", "tasks.PresignUpload"),
				slog.String("event_type", string(eventbus.TaskAttachmentAdded)),
				slog.Int64("workspace_id", int64(ws.ID)),
				slog.Int64("actor_id", int64(actorID)),
				slog.Int64("task_id", taskInternal),
				slog.String("attachment_id", pub.String()),
			)
		}

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
			return nil, httpErr(apierrors.InternalStorageNotConfigured)
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
