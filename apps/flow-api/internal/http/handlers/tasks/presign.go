package tasks

import (
	"context"
	"encoding/hex"
	stderrors "errors"
	"time"

	"github.com/libraz/nodate-flow/apps/flow-api/internal/attachkit"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/generated"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/types"
	apierrors "github.com/libraz/nodate-flow/apps/flow-api/internal/errors"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/http/handlers/handlerutil"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/http/middleware"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/mutationlog"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/storage"
)

const presignExpiry = 15 * time.Minute

// PresignUpload handles POST /tasks/{id}/attachments/presign.
//
// attachkit.Reserve settles the storage object against the SHA-256 the
// client computed over the file body: a dedup hit on a confirmed object
// that is still in the store returns deduplicated=true with no upload URL
// (the client must NOT issue a PUT); anything else returns a short-lived
// signed PUT URL for the content-addressed key the row already points at.
// The signed URL is minted after commit so a failed store call cannot
// leave an orphan attachment.
func PresignUpload(deps Deps) func(context.Context, *PresignUploadInput) (*PresignUploadOutput, error) {
	return func(ctx context.Context, in *PresignUploadInput) (*PresignUploadOutput, error) {
		if deps.Storage == nil {
			return nil, httpErr(apierrors.InternalStorageNotConfigured)
		}
		if !storage.IsAllowedUpload(in.Body.ContentType, in.Body.Filename) {
			return nil, httpErr(apierrors.ValidationFileTypeNotAllowed)
		}
		if in.Body.ByteSize > storage.MaxUploadSize {
			return nil, httpErr(apierrors.ValidationFileTooLarge)
		}
		shaBytes, err := hex.DecodeString(in.Body.Sha256)
		if err != nil || len(shaBytes) != 32 {
			return nil, httpErr(apierrors.ValidationChecksumFormatInvalid)
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

		actor := mutationlog.Actor{UserID: actorID, WorkspaceID: ws.ID}
		res, err := attachkit.Reserve(ctx, deps.DB, attachkit.Args{
			WorkspaceID:       ws.ID,
			WorkspacePublicID: types.FromUUID(ws.PublicID),
			TaskID:            task.ID,
			TaskPublicID:      types.FromUUID(task.PublicID),
			Filename:          in.Body.Filename,
			ContentType:       in.Body.ContentType,
			Size:              in.Body.ByteSize,
			SHA256:            shaBytes,
			UploaderUserID:    actorID,
			Actor:             actor,
			Store:             deps.Storage,
		})
		if err != nil {
			if stderrors.Is(err, attachkit.ErrTaskGone) {
				return nil, httpErr(apierrors.WsTaskNotFound)
			}
			return nil, httpErr(apierrors.InternalUnexpected)
		}
		attachID := res.AttachmentPublicID.String()
		taskInternal := int64(task.ID)
		deps.Mutations.RecordTxAudit(ctx, actor, mutationlog.Mutation{
			AuditAction:  "attachment.create",
			ResourceType: "attachment",
			ResourceID:   attachID,
			TaskID:       &taskInternal,
			Payload: map[string]any{
				"taskId":       task.PublicID.String(),
				"attachmentId": attachID,
				"filename":     in.Body.Filename,
			},
			CallSite: "tasks.PresignUpload (attachkit.Reserve appended the event)",
		})

		// The URL binds the exact size, media type and SHA-256, and the blob
		// relay has the store reject bytes that hash differently, so a client
		// cannot upload bytes B against a claim of sha=A and poison the dedup
		// row the next legitimate uploader of A would land on.
		var uploadURL string
		if !res.AlreadyUploaded {
			u, err := deps.Storage.SignedPutURL(res.StorageKey, in.Body.ContentType, int64(in.Body.ByteSize), hex.EncodeToString(shaBytes), presignExpiry)
			if err != nil {
				// The DB is committed; a retried presign dedups onto the
				// row now in place.
				return nil, httpErr(apierrors.InternalUnexpected)
			}
			uploadURL = u
		}

		return &PresignUploadOutput{Body: PresignUploadOutputBody{
			UploadURL:    uploadURL,
			StorageKey:   res.StorageKey,
			AttachmentID: attachID,
			Deduplicated: res.AlreadyUploaded,
		}}, nil
	}
}

// DownloadAttachment handles GET /tasks/{id}/attachments/{aid}/download.
// It returns a signed URL on the blob relay that serves the file as an
// attachment. The relay emits the filename in RFC 5987 form so non-ASCII
// (e.g. Japanese) names round-trip without mojibake.
func DownloadAttachment(deps Deps) func(context.Context, *DownloadAttachmentInput) (*DownloadAttachmentOutput, error) {
	return func(ctx context.Context, in *DownloadAttachmentInput) (*DownloadAttachmentOutput, error) {
		if deps.Storage == nil {
			return nil, httpErr(apierrors.InternalStorageNotConfigured)
		}
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

		row, err := deps.Queries.GetAttachmentByPublicID(ctx, generated.GetAttachmentByPublicIDParams{
			WorkspaceID: ws.ID,
			TaskID:      handlerutil.NullInt32From(task.ID),
			PublicID:    aid,
		})
		if err != nil {
			return nil, httpErr(apierrors.WsTaskNotFound)
		}

		downloadURL, err := deps.Storage.SignedDownloadURL(row.StorageKey, row.Filename, presignExpiry)
		if err != nil {
			return nil, httpErr(apierrors.InternalUnexpected)
		}

		return &DownloadAttachmentOutput{Body: DownloadAttachmentOutputBody{
			DownloadURL: downloadURL,
		}}, nil
	}
}

// PreviewAttachment handles GET /tasks/{id}/attachments/{aid}/preview.
// It returns a signed URL that serves an allowlisted raster image inline.
// Like DownloadAttachment it writes no audit row.
func PreviewAttachment(deps Deps) func(context.Context, *PreviewAttachmentInput) (*PreviewAttachmentOutput, error) {
	return func(ctx context.Context, in *PreviewAttachmentInput) (*PreviewAttachmentOutput, error) {
		if deps.Storage == nil {
			return nil, httpErr(apierrors.InternalStorageNotConfigured)
		}
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

		row, err := deps.Queries.GetAttachmentByPublicID(ctx, generated.GetAttachmentByPublicIDParams{
			WorkspaceID: ws.ID,
			TaskID:      handlerutil.NullInt32From(task.ID),
			PublicID:    aid,
		})
		if err != nil {
			return nil, httpErr(apierrors.WsTaskNotFound)
		}

		previewURL, expiresAt, err := deps.Storage.SignedPreviewURL(row.StorageKey, row.Filename, row.ContentType)
		if err != nil {
			if stderrors.Is(err, storage.ErrNotPreviewable) {
				return nil, httpErr(apierrors.ValidationFileNotPreviewable)
			}
			return nil, httpErr(apierrors.InternalUnexpected)
		}

		return &PreviewAttachmentOutput{Body: PreviewAttachmentOutputBody{
			PreviewURL: previewURL,
			ExpiresAt:  expiresAt.Unix(),
		}}, nil
	}
}
