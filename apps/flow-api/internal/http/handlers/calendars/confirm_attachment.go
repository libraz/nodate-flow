package calendars

import (
	"context"
	"log/slog"
	"time"

	"github.com/google/uuid"

	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/generated/calendar"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/types"
	apierrors "github.com/libraz/nodate-flow/apps/flow-api/internal/errors"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/http/handlers/handlerutil"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/storage"
	"github.com/libraz/nodate-flow/packages/go-shared/apierr"
	"github.com/libraz/nodate-flow/packages/go-shared/ctxutil"
)

// ConfirmAttachmentInput is the path for
// POST /workspaces/{wsId}/calendars/{calId}/events/{evtId}/attachments/{attId}/confirm.
type ConfirmAttachmentInput struct {
	WsID  string `path:"wsId" doc:"Workspace public ID"`
	CalID string `path:"calId" doc:"Calendar public ID"`
	EvtID string `path:"evtId" doc:"Event public ID"`
	AttID string `path:"attId" doc:"Attachment public ID"`
}

// ConfirmAttachmentOutput is the response for the confirm endpoint.
type ConfirmAttachmentOutput struct {
	Body struct {
		Ok       bool   `json:"ok" doc:"True when the uploaded object passed the size check."`
		ByteSize uint64 `json:"byteSize" doc:"The object's true stored size in bytes."`
	}
}

// ConfirmAttachment is the calendar analogue of tasks.ConfirmUpload.
//
// The presigned PUT binds only the file's SHA-256, not its length, so a
// client can declare a tiny byteSize at presign time and stream a much
// larger body. After the upload finishes the client calls confirm; the
// handler StatObjects the real blob and, if the actual size exceeds the
// shared per-file ceiling, deletes the attachment row (and GCs the blob if
// it is now unreferenced) and rejects the request. Success returns the
// object's true size.
func ConfirmAttachment(deps Deps) func(context.Context, *ConfirmAttachmentInput) (*ConfirmAttachmentOutput, error) {
	return func(ctx context.Context, input *ConfirmAttachmentInput) (*ConfirmAttachmentOutput, error) {
		if deps.Storage == nil {
			return nil, httpErr(apierrors.InternalStorageNotConfigured)
		}
		wsID, actorID, err := resolveWorkspace(ctx, deps.Queries, input.WsID)
		if err != nil {
			return nil, err
		}
		cal, _, err := resolveCalendarWrite(ctx, deps.CalendarQueries, wsID, actorID, input.CalID)
		if err != nil {
			return nil, err
		}
		evt, err := resolveEvent(ctx, deps.CalendarQueries, cal.ID, wsID, input.EvtID)
		if err != nil {
			return nil, err
		}

		attUID, err := uuid.Parse(input.AttID)
		if err != nil {
			return nil, httpErr(apierrors.CalendarAttachmentNotFound)
		}

		att, err := deps.CalendarQueries.FindCalendarEventAttachmentByPublicId(ctx, calendar.FindCalendarEventAttachmentByPublicIdParams{
			PublicID:    types.FromUUID(attUID),
			EventID:     handlerutil.NullInt32From(evt.ID),
			WorkspaceID: wsID,
		})
		if err != nil {
			return nil, httpErr(apierr.SpecForErrNoRows(err, apierrors.CalendarAttachmentNotFound, apierrors.CalendarAttachmentStoreReadInterrupted))
		}

		actual, err := deps.Storage.StatObject(ctx, att.StorageKey)
		if err != nil {
			// Upload never completed — nothing to confirm.
			return nil, httpErr(apierrors.InternalUnexpected)
		}
		size := uint64(actual) //#nosec G115 -- StatObject returns a non-negative object size

		if !storage.ExceedsUploadLimit(size, handlerutil.MaxUploadSize) {
			out := &ConfirmAttachmentOutput{}
			out.Body.Ok = true
			out.Body.ByteSize = size
			return out, nil
		}

		if err := rejectOversizeCalendarAttachment(ctx, deps, wsID, evt.ID, att.PublicID, att.StorageObjectID); err != nil {
			return nil, err
		}

		_ = appendCalendarEvent(ctx, deps.DB, wsID, "calendar.event.attachment.deleted", &actorID, map[string]any{
			"eventId":      input.EvtID,
			"calendarId":   input.CalID,
			"attachmentId": input.AttID,
			"reason":       "size_limit_exceeded",
		})

		return nil, httpErr(apierrors.ValidationFileTooLarge)
	}
}

// rejectOversizeCalendarAttachment hard-deletes an event attachment, drops
// the ref count on its storage object, GCs the blob if now unreferenced,
// and removes it from object storage. It mirrors DeleteAttachment's
// transactional ordering so the ref count cannot drift.
func rejectOversizeCalendarAttachment(ctx context.Context, deps Deps, wsID uint32, eventID uint32, attPub types.PublicID, storageObjectID uint32) error {
	tx, err := deps.DB.BeginTx(ctx, nil)
	if err != nil {
		return httpErr(apierrors.InternalUnexpected)
	}
	defer func() { _ = tx.Rollback() }()
	qtx := deps.Queries.WithTx(tx)
	cqtx := deps.CalendarQueries.WithTx(tx)

	if err := cqtx.DeleteCalendarEventAttachment(ctx, calendar.DeleteCalendarEventAttachmentParams{
		PublicID:    attPub,
		EventID:     handlerutil.NullInt32From(eventID),
		WorkspaceID: wsID,
	}); err != nil {
		return httpErr(apierrors.CalendarAttachmentStoreDeleteInterrupted)
	}

	if _, err := qtx.DecrementStorageObjectRefCount(ctx, storageObjectID); err != nil {
		return httpErr(apierrors.CalendarAttachmentStoreDeleteInterrupted)
	}

	fullRow, err := qtx.FindStorageObjectByID(ctx, storageObjectID)
	if err != nil {
		return httpErr(apierrors.InternalUnexpected)
	}

	var storageKeyToRemove string
	gcRes, err := qtx.DeleteStorageObjectIfUnreferenced(ctx, storageObjectID)
	if err != nil {
		return httpErr(apierrors.CalendarAttachmentStoreDeleteInterrupted)
	}
	if affected, _ := gcRes.RowsAffected(); affected == 1 {
		storageKeyToRemove = fullRow.StorageKey
	}

	if err := tx.Commit(); err != nil {
		return httpErr(apierrors.InternalUnexpected)
	}

	if storageKeyToRemove != "" && deps.Storage != nil {
		cleanupCtx, cleanupCancel := ctxutil.Cleanup(ctx, 5*time.Second)
		if rmErr := deps.Storage.RemoveObject(cleanupCtx, storageKeyToRemove); rmErr != nil {
			slog.WarnContext(cleanupCtx, "calendar attachment confirm: oversize cleanup failed",
				slog.Any("err", rmErr),
				slog.String("storage_key", storageKeyToRemove),
			)
		}
		cleanupCancel()
	}
	return nil
}
