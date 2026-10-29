package tasks

import (
	"context"
	"database/sql"
	stderrors "errors"
	"log/slog"
	"time"

	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/dbretry"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/generated"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/types"
	apierrors "github.com/libraz/nodate-flow/apps/flow-api/internal/errors"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/eventbus"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/http/handlers/handlerutil"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/http/middleware"
	nflog "github.com/libraz/nodate-flow/apps/flow-api/internal/log"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/mutationlog"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/storage"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/storagegc"
	"github.com/libraz/nodate-flow/packages/go-shared/ctxutil"
)

// ConfirmTaskAttachmentInput is the path for
// POST /tasks/{id}/attachments/{aid}/confirm.
type ConfirmTaskAttachmentInput struct {
	ID  string `path:"id"`
	AID string `path:"aid"`
}

// ConfirmTaskAttachmentBody is the response payload for the confirm endpoint.
type ConfirmTaskAttachmentBody struct {
	Ok       bool   `json:"ok"`
	ByteSize uint64 `json:"byteSize"`
}

// ConfirmTaskAttachmentOutput is the response for the confirm endpoint.
type ConfirmTaskAttachmentOutput struct {
	Body ConfirmTaskAttachmentBody
}

// ConfirmUpload handles POST /tasks/{id}/attachments/{aid}/confirm.
//
// The signed PUT URL binds the size, content type and SHA-256 and the blob
// relay enforces them; this endpoint is a second check on the stored
// object: after the client finishes the upload it calls confirm, the handler StatObjects
// the real blob, and if the actual stored size exceeds the shared per-file
// ceiling the attachment row and (if now unreferenced) the underlying blob
// are deleted and the request is rejected. On success the caller receives
// the object's true size.
func ConfirmUpload(deps Deps) func(context.Context, *ConfirmTaskAttachmentInput) (*ConfirmTaskAttachmentOutput, error) {
	return func(ctx context.Context, in *ConfirmTaskAttachmentInput) (*ConfirmTaskAttachmentOutput, error) {
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
			if stderrors.Is(err, sql.ErrNoRows) {
				return nil, httpErr(apierrors.WsTaskNotFound)
			}
			return nil, httpErr(apierrors.InternalUnexpected)
		}

		actual, err := deps.Storage.StatObject(ctx, row.StorageKey)
		if err != nil {
			// The blob is missing or unreadable — the upload never
			// completed, so there is nothing to confirm.
			return nil, httpErr(apierrors.InternalUnexpected)
		}
		size := uint64(actual) //#nosec G115 -- StatObject returns a non-negative object size

		if !storage.ExceedsUploadLimit(size, handlerutil.MaxUploadSize) {
			// The size is now a measurement rather than a claim, so the
			// row stops being a reservation: it becomes a dedup
			// candidate and the sweeper leaves it alone. The measured
			// size replaces the declared one, which until this point
			// was whatever the client felt like sending.
			if err := deps.Queries.MarkStorageObjectUploaded(ctx, generated.MarkStorageObjectUploadedParams{
				ByteSize: size,
				ID:       row.StorageObjectID,
			}); err != nil {
				return nil, httpErr(apierrors.InternalUnexpected)
			}
			out := &ConfirmTaskAttachmentOutput{}
			out.Body.Ok = true
			out.Body.ByteSize = size
			return out, nil
		}

		// Oversize upload: purge the attachment row and (if it drops to
		// zero references) the underlying storage object before rejecting.
		if err := rejectOversizeTaskAttachment(ctx, deps, ws.ID, task, aid, row.StorageObjectID); err != nil {
			return nil, err
		}

		return nil, httpErr(apierrors.ValidationFileTooLarge)
	}
}

// rejectOversizeTaskAttachment hard-deletes an attachment row, drops the
// ref count on its storage object, GCs the blob if now unreferenced, and
// removes it from object storage. It mirrors DeleteAttachment's
// transactional ordering so the ref count cannot drift.
func rejectOversizeTaskAttachment(ctx context.Context, deps Deps, wsID uint32, task middleware.TaskContext, aid types.PublicID, storageObjectID uint32) error {
	actorID, _ := middleware.ActorFromContext(ctx)
	taskInternal := int64(task.ID)
	var storageKeyToRemove string
	err := dbretry.InTx(ctx, deps.DB, "tasks.ConfirmUpload", nil, func(ctx context.Context, tx *dbretry.Tx) error {
		storageKeyToRemove = ""
		qtx := generated.New(tx)

		// This is the cleanup path for a blob this request has just rejected,
		// not an endpoint answering about a resource the caller named, so a
		// zero count has no 404 to map onto.
		//
		// affected-rows: not-applicable — the row was read a moment ago in this
		// same request, and the reject path needs it gone rather than counted.
		if _, err := qtx.DeleteAttachment(ctx, generated.DeleteAttachmentParams{
			WorkspaceID: wsID,
			TaskID:      handlerutil.NullInt32From(task.ID),
			PublicID:    aid,
		}); err != nil {
			return err
		}

		if _, err := qtx.DecrementStorageObjectRefCount(ctx, storageObjectID); err != nil {
			return err
		}

		fullRow, err := qtx.FindStorageObjectByID(ctx, storageObjectID)
		if err != nil {
			return err
		}

		gcResult, err := qtx.DeleteStorageObjectIfUnreferenced(ctx, storageObjectID)
		if err != nil {
			return err
		}
		if affected, _ := gcResult.RowsAffected(); affected == 1 {
			storageKeyToRemove = fullRow.StorageKey
			if err := storagegc.Queue(ctx, qtx, storageKeyToRemove); err != nil {
				return err
			}
		}

		return deps.Mutations.RecordInTx(ctx, tx, mutationlog.Actor{UserID: actorID, WorkspaceID: wsID}, mutationlog.Mutation{
			EventType:    eventbus.TaskAttachmentRemoved,
			AuditAction:  "attachment.delete",
			ResourceType: "attachment",
			ResourceID:   aid.String(),
			TaskID:       &taskInternal,
			Payload: map[string]any{
				"taskId":       task.PublicID.String(),
				"attachmentId": aid.String(),
				"reason":       "size_limit_exceeded",
			},
			CallSite: "tasks.ConfirmUpload",
		})
	})
	if err != nil {
		return httpErr(apierrors.InternalUnexpected)
	}

	if storageKeyToRemove != "" && deps.Storage != nil {
		cleanupCtx, cleanupCancel := ctxutil.Cleanup(ctx, 5*time.Second)
		if rmErr := storagegc.RemoveQueued(cleanupCtx, deps.Queries, deps.Storage, storageKeyToRemove); rmErr != nil {
			nflog.LoggerFromContext(cleanupCtx).WarnContext(cleanupCtx, "attachment confirm: oversize cleanup failed; the sweeper will retry",
				slog.Any("err", rmErr),
				slog.String("storage_key", storageKeyToRemove),
			)
		}
		cleanupCancel()
	}
	return nil
}
