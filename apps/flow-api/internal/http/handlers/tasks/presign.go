package tasks

import (
	"context"
	"database/sql"
	"encoding/hex"
	stderrors "errors"
	"log/slog"
	"strings"
	"time"

	"github.com/nodate-flow/nodate-flow/apps/flow-api/internal/db/generated"
	"github.com/nodate-flow/nodate-flow/apps/flow-api/internal/db/types"
	apierrors "github.com/nodate-flow/nodate-flow/apps/flow-api/internal/errors"
	"github.com/nodate-flow/nodate-flow/apps/flow-api/internal/eventbus"
	"github.com/nodate-flow/nodate-flow/apps/flow-api/internal/http/handlers/handlerutil"
	"github.com/nodate-flow/nodate-flow/apps/flow-api/internal/http/middleware"
	"github.com/nodate-flow/nodate-flow/apps/flow-api/internal/storage"
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

// presignMaxAttempts bounds the duplicate-entry retry loop in
// PresignUpload. We restart the transaction (not just the SELECT)
// when InsertStorageObject hits MySQL 1062, because the same
// transaction's REPEATABLE READ snapshot was pinned at the first
// FindStorageObjectByWorkspaceSha and cannot observe the winning
// inserter's committed row. Three attempts is plenty for two-way
// races; in practice attempt 0 succeeds, attempt 1 dedups onto the
// winner, and attempt 2 is a defensive ceiling against pathological
// many-way contention.
const presignMaxAttempts = 3

// PresignUpload handles POST /tasks/{id}/attachments/presign.
//
// The flow has two branches driven by the SHA-256 the client computed
// over the file body:
//
//  1. Dedup hit. The (workspace_id, sha256) UNIQUE key on
//     storage_objects already has a row, so the handler bumps that
//     row's ref_count, inserts the attachment with the existing FK,
//     and returns deduplicated=true with no presigned URL — the
//     client should NOT issue a PUT.
//  2. Miss. The handler allocates a new storage_objects row at
//     ref_count=1 with a content-addressed storage key
//     (workspace/{wsHex}/{sha256Hex}), inserts the attachment, and
//     returns a short-lived presigned PUT URL the client streams the
//     file bytes to. The blob lands at the predetermined key so the
//     server never has to mutate the storage_objects row after the
//     upload completes.
//
// Both branches happen inside a single transaction so the attachment
// row and the storage_objects row stay consistent if either INSERT
// fails. The presigned URL is generated AFTER commit so a failed
// MinIO call cannot leave an orphan attachment.
//
// Concurrent-race retry: on InsertStorageObject MySQL 1062 we MUST
// roll the transaction back and start a fresh one before re-running
// FindStorageObjectByWorkspaceSha. Under REPEATABLE READ the snapshot
// is pinned at the first SELECT inside the transaction, so a same-tx
// re-find still sees ErrNoRows even after the winning racer commits.
// Restarting the tx resets the snapshot so the next attempt observes
// the winner row and can dedup onto it instead of leaking a 500.
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

		wsHex := hex.EncodeToString(ws.PublicID[:])
		shaHex := strings.ToLower(in.Body.Sha256)
		baseStorageKey := storage.StorageKeyForWorkspace(wsHex, shaHex)

		var (
			storageObjectID uint32
			deduplicated    bool
			storageKey      string
			attachPub       = types.New()
			committed       bool
		)

	attempts:
		for attempt := 0; attempt < presignMaxAttempts; attempt++ {
			tx, err := deps.DB.BeginTx(ctx, nil)
			if err != nil {
				return nil, httpErr(apierrors.InternalUnexpected)
			}
			rolledBack := false
			rollback := func() {
				if !rolledBack {
					_ = tx.Rollback()
					rolledBack = true
				}
			}
			qtx := deps.Queries.WithTx(tx)

			storageKey = baseStorageKey
			deduplicated = false

			existing, findErr := qtx.FindStorageObjectByWorkspaceSha(ctx, generated.FindStorageObjectByWorkspaceShaParams{
				WorkspaceID: handlerutil.NullInt32From(ws.ID),
				Sha256:      shaBytes,
			})
			switch {
			case findErr == nil:
				// Dedup hit: bump the existing row's ref_count.
				if err := qtx.IncrementStorageObjectRefCount(ctx, existing.ID); err != nil {
					rollback()
					return nil, httpErr(apierrors.InternalUnexpected)
				}
				storageObjectID = existing.ID
				storageKey = existing.StorageKey
				deduplicated = true
			case stderrors.Is(findErr, sql.ErrNoRows):
				// Miss: allocate a new storage_objects row with ref_count=1.
				soPub := types.New()
				insRes, insErr := qtx.InsertStorageObject(ctx, generated.InsertStorageObjectParams{
					PublicID:    soPub,
					WorkspaceID: handlerutil.NullInt32From(ws.ID),
					OwnerUserID: sql.NullInt32{},
					Sha256:      shaBytes,
					ByteSize:    in.Body.ByteSize,
					ContentType: in.Body.ContentType,
					StorageKey:  storageKey,
				})
				switch {
				case insErr == nil:
					lastID, idErr := insRes.LastInsertId()
					if idErr != nil || lastID <= 0 {
						rollback()
						return nil, httpErr(apierrors.InternalUnexpected)
					}
					storageObjectID = uint32(lastID) //#nosec G115 -- AUTO_INCREMENT id fits uint32 within realistic deployments
				case handlerutil.IsDuplicateEntry(insErr):
					// Race lost: another tx committed the
					// (workspace_id, sha256) row first. Roll back this
					// tx so the next attempt's REPEATABLE READ
					// snapshot can observe the winner; a same-tx
					// re-find would still hit the pinned snapshot and
					// see ErrNoRows.
					rollback()
					continue attempts
				default:
					rollback()
					return nil, httpErr(apierrors.InternalUnexpected)
				}
			default:
				rollback()
				return nil, httpErr(apierrors.InternalUnexpected)
			}

			if _, err := qtx.AddAttachment(ctx, generated.AddAttachmentParams{
				PublicID:        attachPub,
				WorkspaceID:     ws.ID,
				TaskID:          handlerutil.NullInt32From(task.ID),
				UploaderID:      actorID,
				StorageObjectID: storageObjectID,
				Filename:        in.Body.Filename,
			}); err != nil {
				rollback()
				return nil, httpErr(apierrors.InternalUnexpected)
			}

			if err := tx.Commit(); err != nil {
				// Commit failed; the tx is already gone. Suppress the
				// deferred rollback and surface the error.
				rolledBack = true
				return nil, httpErr(apierrors.InternalUnexpected)
			}
			rolledBack = true
			committed = true
			break attempts
		}
		if !committed {
			// All retries lost the race. In practice unreachable: with
			// presignMaxAttempts=3 a two-way race converges on attempt
			// 1 and a many-way race on attempt 2. Surface as 500 so
			// the (extremely rare) ceiling event is observable.
			return nil, httpErr(apierrors.InternalUnexpected)
		}

		// Generate the presigned PUT URL only on the miss branch.
		// PresignPutWithSha256 binds x-amz-content-sha256 into the
		// SigV4 signed-headers list so the bucket rejects the upload
		// (XAmzContentSHA256Mismatch / BadDigest) if the body bytes
		// do not actually hash to the value the client claimed. That
		// closes the content-poisoning attack against the dedup row:
		// otherwise a client could upload bytes B under a presign
		// minted for sha=A and the next legitimate uploader of A
		// would dedup onto the wrong content.
		var (
			uploadURL       string
			requiredHeaders map[string]string
		)
		if !deduplicated {
			u, err := deps.Storage.PresignPutWithSha256(ctx, storageKey, shaHex, presignExpiry)
			if err != nil {
				// The DB is committed; we cannot rollback. The client
				// can retry the presign endpoint and will hit the
				// dedup branch on the second call (since the
				// storage_objects row is now in place). Surface the
				// error so the client knows not to upload.
				return nil, httpErr(apierrors.InternalUnexpected)
			}
			uploadURL = u
			requiredHeaders = map[string]string{"x-amz-content-sha256": shaHex}
		}

		taskInternal := int64(task.ID)
		if err := eventbus.Append(ctx, deps.DB, eventbus.Event{
			Type:        eventbus.TaskAttachmentAdded,
			WorkspaceID: ws.ID,
			ActorUserID: actorPtr(ctx),
			TaskID:      &taskInternal,
			Payload: map[string]any{
				"taskId":       task.PublicID.String(),
				"attachmentId": attachPub.String(),
				"filename":     in.Body.Filename,
				"deduplicated": deduplicated,
			},
		}); err != nil {
			slog.ErrorContext(ctx, "eventbus.Append failed",
				slog.Any("err", err),
				slog.String("handler", "tasks.PresignUpload"),
				slog.String("event_type", string(eventbus.TaskAttachmentAdded)),
				slog.Int64("workspace_id", int64(ws.ID)),
				slog.Int64("actor_id", int64(actorID)),
				slog.Int64("task_id", taskInternal),
				slog.String("attachment_id", attachPub.String()),
			)
		}

		return &PresignUploadOutput{Body: PresignUploadOutputBody{
			UploadURL:       uploadURL,
			StorageKey:      storageKey,
			AttachmentID:    attachPub.String(),
			Deduplicated:    deduplicated,
			RequiredHeaders: requiredHeaders,
		}}, nil
	}
}

// DownloadAttachment handles GET /tasks/{id}/attachments/{aid}/download.
// It returns a presigned GET URL with Content-Disposition: attachment.
// The filename embedded in the disposition header uses RFC 5987 form so
// non-ASCII (e.g. Japanese) names round-trip without mojibake.
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
