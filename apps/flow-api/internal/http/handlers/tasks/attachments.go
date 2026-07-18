package tasks

import (
	"context"
	"database/sql"
	stderrors "errors"
	"log/slog"
	"time"

	"github.com/nodate-flow/nodate-flow/apps/flow-api/internal/audit"
	"github.com/nodate-flow/nodate-flow/apps/flow-api/internal/db/generated"
	"github.com/nodate-flow/nodate-flow/apps/flow-api/internal/db/types"
	apierrors "github.com/nodate-flow/nodate-flow/apps/flow-api/internal/errors"
	"github.com/nodate-flow/nodate-flow/apps/flow-api/internal/eventbus"
	"github.com/nodate-flow/nodate-flow/apps/flow-api/internal/http/handlers/handlerutil"
	"github.com/nodate-flow/nodate-flow/apps/flow-api/internal/http/middleware"
	nflog "github.com/nodate-flow/nodate-flow/apps/flow-api/internal/log"
	"github.com/nodate-flow/nodate-flow/packages/go-shared/ctxutil"
	"github.com/nodate-flow/nodate-flow/packages/go-shared/logutil"
)

// ListAttachments handles GET /tasks/{id}/attachments. Each row is the
// attachments metadata joined with the backing storage_objects row, so
// the response carries content-type / byte_size / storage_key / sha256
// without an extra round trip. Returns the canonical empty envelope
// when the task has no attachments.
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
//
// The handler runs the soft-delete + ref-count drop + (possibly) the
// GC delete inside a single transaction so the storage_objects ref
// count cannot drift if any step fails. When the decrement leaves the
// row at ref_count = 0 the underlying MinIO blob is removed on a
// best-effort basis after commit; cleanup failures only emit a warn
// log because the DB has already been updated and re-running the GC
// sweeper will catch any orphans.
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

		tx, err := deps.DB.BeginTx(ctx, nil)
		if err != nil {
			return nil, httpErr(apierrors.InternalUnexpected)
		}
		defer func() { _ = tx.Rollback() }()
		qtx := deps.Queries.WithTx(tx)

		// Resolve the storage_object_id BEFORE the soft-delete so we
		// can decrement its ref count in the same transaction. The
		// lookup is scoped to the task whose ACL the caller already
		// cleared (task_id predicate), so an attachment on a different
		// (or unauthorized) task returns no row. A missing row - whether
		// the attachment never existed under this task, belongs to
		// another task, or was already deleted - is reported as 404 via
		// WS.TASK.NOT_FOUND so cross-task existence stays hidden.
		soRow, err := qtx.GetAttachmentStorageObjectIDForDelete(ctx, generated.GetAttachmentStorageObjectIDForDeleteParams{
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

		if err := qtx.DeleteAttachment(ctx, generated.DeleteAttachmentParams{
			WorkspaceID: ws.ID,
			TaskID:      handlerutil.NullInt32From(task.ID),
			PublicID:    aid,
		}); err != nil {
			return nil, httpErr(apierrors.InternalUnexpected)
		}

		decResult, err := qtx.DecrementStorageObjectRefCount(ctx, soRow.StorageObjectID)
		if err != nil {
			return nil, httpErr(apierrors.InternalUnexpected)
		}
		if affected, _ := decResult.RowsAffected(); affected != 1 {
			// Underflow guard tripped: the row was already at 0.
			// Programmer error somewhere upstream; log and continue
			// so the attachment delete still completes.
			nflog.LoggerFromContext(ctx).WarnContext(ctx, "storage object ref_count underflow",
				slog.Uint64("storage_object_id", uint64(soRow.StorageObjectID)),
				slog.String("handler", "tasks.DeleteAttachment"),
			)
		}

		// Pre-read the storage key BEFORE attempting the GC delete so
		// we still have the row data on a successful unreferenced
		// drop (the row is gone after the DELETE). FindStorageObjectByID
		// runs against the same transaction so we observe the
		// post-decrement ref_count consistently.
		fullRow, lookupErr := qtx.FindStorageObjectByID(ctx, soRow.StorageObjectID)
		if lookupErr != nil {
			return nil, httpErr(apierrors.InternalUnexpected)
		}

		// Attempt a GC delete; if no row was deleted there are still
		// other referencing rows and we must NOT touch MinIO.
		var storageKeyToRemove string
		gcResult, err := qtx.DeleteStorageObjectIfUnreferenced(ctx, soRow.StorageObjectID)
		if err != nil {
			return nil, httpErr(apierrors.InternalUnexpected)
		}
		if affected, _ := gcResult.RowsAffected(); affected == 1 {
			storageKeyToRemove = fullRow.StorageKey
		}

		if err := tx.Commit(); err != nil {
			return nil, httpErr(apierrors.InternalUnexpected)
		}

		if storageKeyToRemove != "" && deps.Storage != nil {
			cleanupCtx, cleanupCancel := ctxutil.Cleanup(ctx, 5*time.Second)
			if rmErr := deps.Storage.RemoveObject(cleanupCtx, storageKeyToRemove); rmErr != nil {
				nflog.LoggerFromContext(cleanupCtx).WarnContext(cleanupCtx, "attachment delete: orphan cleanup failed",
					slog.Any("err", rmErr),
					slog.String("storage_key", storageKeyToRemove),
				)
			}
			cleanupCancel()
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
			nflog.LoggerFromContext(ctx).ErrorContext(ctx, "eventbus.Append failed",
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
