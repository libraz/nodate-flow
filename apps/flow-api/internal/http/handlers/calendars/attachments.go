package calendars

import (
	"context"
	"encoding/hex"
	stderrors "errors"
	"log/slog"
	"time"

	"github.com/google/uuid"

	"github.com/libraz/nodate-flow/apps/flow-api/internal/attachkit"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/generated/calendar"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/types"
	apierrors "github.com/libraz/nodate-flow/apps/flow-api/internal/errors"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/eventbus"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/http/handlers/handlerutil"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/mutationlog"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/storage"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/storagegc"
	"github.com/libraz/nodate-flow/packages/go-shared/apierr"
	"github.com/libraz/nodate-flow/packages/go-shared/ctxutil"
	"github.com/libraz/nodate-flow/packages/go-shared/logutil"
)

const calendarPresignExpiry = 15 * time.Minute

// --- Input/Output types ---

// ListAttachmentsInput is the input for listing event attachments.
type ListAttachmentsInput struct {
	WsID  string `path:"wsId" doc:"Workspace public ID"`
	CalID string `path:"calId" doc:"Calendar public ID"`
	EvtID string `path:"evtId" doc:"Event public ID"`
}

// AttachmentResponse is the JSON representation of an event attachment.
//
// StorageObjectID surfaces storage_objects.public_id so two attachments
// pointing at the same blob (the dedup case) are observably the same
// underlying object. ContentType / ByteSize / StorageKey / ChecksumSHA256
// are flattened in from storage_objects via JOIN; the API never exposes
// the internal storage_objects.id.
type AttachmentResponse struct {
	ID              string `json:"id"`
	StorageObjectID string `json:"storageObjectId,omitempty"`
	Filename        string `json:"filename"`
	ContentType     string `json:"contentType"`
	ByteSize        uint64 `json:"byteSize"`
	StorageKey      string `json:"storageKey"`
	ChecksumSHA256  string `json:"checksumSha256,omitempty"`
	UploaderID      string `json:"uploaderId"`
	UploaderName    string `json:"uploaderName"`
	CreatedAt       int64  `json:"createdAt"`
}

// ListAttachmentsOutput is the response for the list attachments endpoint.
type ListAttachmentsOutput struct {
	Body struct {
		Attachments []AttachmentResponse `json:"attachments"`
	}
}

// PresignAttachmentInput is the request body for the calendar event
// presign endpoint. Mirrors tasks.PresignUploadInput so the two
// upload-flow surfaces stay shaped the same.
type PresignAttachmentInput struct {
	WsID  string `path:"wsId" doc:"Workspace public ID"`
	CalID string `path:"calId" doc:"Calendar public ID"`
	EvtID string `path:"evtId" doc:"Event public ID"`
	Body  struct {
		Filename    string `json:"filename" minLength:"1" maxLength:"512" doc:"Original filename"`
		ContentType string `json:"contentType" minLength:"1" maxLength:"127" doc:"MIME type"`
		ByteSize    uint64 `json:"byteSize" minimum:"1" doc:"File size in bytes. Max 100 MB; over that the handler answers 413 VALIDATION.FILE.TOO_LARGE."`
		Sha256      string `json:"sha256" minLength:"64" maxLength:"64" pattern:"^[0-9a-f]{64}$" doc:"Lowercase hex SHA-256 digest of the file body (64 chars). Drives content-addressed dedup."`
	}
}

// PresignAttachmentOutput is the response for the presign endpoint.
type PresignAttachmentOutput struct {
	Body struct {
		UploadURL    string `json:"uploadUrl,omitempty" doc:"Signed PUT URL on the API. Empty when deduplicated=true."`
		StorageKey   string `json:"storageKey" doc:"Object key (informational; the signed URL already encodes it)."`
		AttachmentID string `json:"attachmentId" doc:"Public ID of the created attachment row"`
		Deduplicated bool   `json:"deduplicated" doc:"True when the server reused an existing blob and no upload is needed."`
	}
}

// DownloadAttachmentInput is the input for the download endpoint.
type DownloadAttachmentInput struct {
	WsID  string `path:"wsId" doc:"Workspace public ID"`
	CalID string `path:"calId" doc:"Calendar public ID"`
	EvtID string `path:"evtId" doc:"Event public ID"`
	AttID string `path:"attId" doc:"Attachment public ID"`
}

// DownloadCalendarAttachmentOutputBody is the response body for the
// calendar event attachment download endpoint. It carries an
// operation-specific name to avoid a generated OpenAPI schema collision
// with the tasks package's DownloadAttachmentOutputBody.
type DownloadCalendarAttachmentOutputBody struct {
	DownloadURL string `json:"downloadUrl" doc:"Signed GET URL on the API; the file is served as an attachment"`
}

// DownloadAttachmentOutput is the response for the download endpoint.
type DownloadAttachmentOutput struct {
	Body DownloadCalendarAttachmentOutputBody
}

// DeleteAttachmentInput is the input for soft-deleting an attachment.
type DeleteAttachmentInput struct {
	WsID  string `path:"wsId" doc:"Workspace public ID"`
	CalID string `path:"calId" doc:"Calendar public ID"`
	EvtID string `path:"evtId" doc:"Event public ID"`
	AttID string `path:"attId" doc:"Attachment public ID"`
}

// DeleteAttachmentOutput is the response for the delete attachment endpoint.
type DeleteAttachmentOutput struct {
	Body struct {
		Deleted bool `json:"deleted"`
	}
}

// --- Handlers ---

// ListAttachments returns all attachments on an event. Each row is
// joined against storage_objects so the response carries content-type,
// byte size, storage key and sha256 without an extra round trip.
func ListAttachments(deps Deps) func(context.Context, *ListAttachmentsInput) (*ListAttachmentsOutput, error) {
	return func(ctx context.Context, input *ListAttachmentsInput) (*ListAttachmentsOutput, error) {
		wsID, actorID, err := resolveWorkspace(ctx, deps.Queries, input.WsID)
		if err != nil {
			return nil, err
		}
		cal, _, err := resolveCalendar(ctx, deps.CalendarQueries, wsID, actorID, input.CalID)
		if err != nil {
			return nil, err
		}

		evt, err := resolveEvent(ctx, deps.CalendarQueries, cal.ID, wsID, input.EvtID)
		if err != nil {
			return nil, err
		}

		rows, err := deps.CalendarQueries.ListCalendarEventAttachments(ctx, calendar.ListCalendarEventAttachmentsParams{
			WorkspaceID: wsID,
			EventID:     handlerutil.NullInt32From(evt.ID),
		})
		if err != nil {
			return nil, httpErr(apierrors.CalendarAttachmentListQueryInterrupted)
		}

		out := &ListAttachmentsOutput{}
		out.Body.Attachments = make([]AttachmentResponse, len(rows))
		for i, r := range rows {
			out.Body.Attachments[i] = AttachmentResponse{
				ID:              r.PublicID.String(),
				StorageObjectID: r.StorageObjectPublicID.String(),
				Filename:        r.Filename,
				ContentType:     r.ContentType,
				ByteSize:        r.ByteSize,
				StorageKey:      r.StorageKey,
				ChecksumSHA256:  hex.EncodeToString(r.Sha256),
				UploaderID:      handlerutil.PublicIDOrEmpty(r.UserPublicID),
				UploaderName:    handlerutil.BylineDisplayName(r.DisplayName),
				CreatedAt:       r.CreatedAt.Unix(),
			}
		}
		return out, nil
	}
}

// PresignAttachment is the calendar event analogue of
// tasks.PresignUpload. The client supplies a SHA-256 of the file body;
// the server runs content-addressed dedup against
// storage_objects(workspace_id, sha256) and either bumps the existing
// row's ref_count (deduplicated=true, no upload) or allocates a fresh
// row and returns a signed PUT URL.
func PresignAttachment(deps Deps) func(context.Context, *PresignAttachmentInput) (*PresignAttachmentOutput, error) {
	return func(ctx context.Context, input *PresignAttachmentInput) (*PresignAttachmentOutput, error) {
		if deps.Storage == nil {
			return nil, httpErr(apierrors.InternalStorageNotConfigured)
		}
		// Cross-surface upload validation: mirror the task presign path
		// so calendar attachments enforce the same MIME allowlist,
		// blocked-extension deny list, and per-file size ceiling.
		if !storage.IsAllowedUpload(input.Body.ContentType, input.Body.Filename) {
			return nil, httpErr(apierrors.ValidationFileTypeNotAllowed)
		}
		if input.Body.ByteSize > storage.MaxUploadSize {
			return nil, httpErr(apierrors.ValidationFileTooLarge)
		}
		shaBytes, err := hex.DecodeString(input.Body.Sha256)
		if err != nil || len(shaBytes) != 32 {
			return nil, httpErr(apierrors.ValidationChecksumFormatInvalid)
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

		// The wsId path param is already the workspace public_id (UUID v7)
		// and resolveWorkspace above has authenticated it, so the storage
		// key prefix derives straight from the input.
		wsUID, err := uuid.Parse(input.WsID)
		if err != nil {
			return nil, httpErr(apierrors.ValidationPathParamInvalid)
		}

		var (
			resv      attachkit.Reservation
			attPub    = types.New()
			committed bool
		)

	attempts:
		for attempt := 0; attempt < attachkit.MaxAttempts; attempt++ {
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
			cqtx := deps.CalendarQueries.WithTx(tx)

			resv, err = attachkit.ReserveObject(ctx, qtx, attachkit.Object{
				WorkspaceID:       wsID,
				WorkspacePublicID: types.FromUUID(wsUID),
				SHA256:            shaBytes,
				Size:              input.Body.ByteSize,
				ContentType:       input.Body.ContentType,
			})
			switch {
			case err == nil:
			case stderrors.Is(err, attachkit.ErrRaceLost):
				// Roll back so the next attempt's snapshot sees the winner.
				rollback()
				continue attempts
			case stderrors.Is(err, attachkit.ErrObjectRead):
				rollback()
				return nil, httpErr(apierrors.CalendarAttachmentStoreReadInterrupted)
			default:
				rollback()
				return nil, httpErr(apierrors.CalendarAttachmentStoreWriteInterrupted)
			}

			if _, err := cqtx.CreateCalendarEventAttachment(ctx, calendar.CreateCalendarEventAttachmentParams{
				PublicID:        attPub,
				WorkspaceID:     wsID,
				EventID:         handlerutil.NullInt32From(evt.ID),
				UploaderID:      actorID,
				StorageObjectID: resv.StorageObjectID,
				Filename:        input.Body.Filename,
			}); err != nil {
				rollback()
				return nil, httpErr(apierrors.CalendarAttachmentStoreWriteInterrupted)
			}

			if err := tx.Commit(); err != nil {
				rolledBack = true
				return nil, httpErr(apierrors.InternalUnexpected)
			}
			rolledBack = true
			committed = true
			break attempts
		}
		if !committed {
			// Retry ceiling reached. Practically unreachable; the
			// 1062 retry path converges in <=2 attempts even under
			// many-way contention.
			return nil, httpErr(apierrors.InternalUnexpected)
		}

		// A dedup hit is only kept when its object is confirmed and still
		// in the store; otherwise the caller uploads to the same key.
		deduplicated := attachkit.StillStored(ctx, deps.Storage, resv)
		storageKey := resv.StorageKey

		// Mint the signed upload URL only on the miss branch. The URL binds
		// the exact size, media type and SHA-256, and the blob relay has the
		// store reject bytes that hash differently, so a client cannot upload
		// bytes B against a claim of sha=A and poison the row that the next
		// legitimate uploader of A would dedup onto.
		var uploadURL string
		if !deduplicated {
			u, err := deps.Storage.SignedPutURL(storageKey, input.Body.ContentType, int64(input.Body.ByteSize), hex.EncodeToString(shaBytes), calendarPresignExpiry)
			if err != nil {
				return nil, httpErr(apierrors.InternalUnexpected)
			}
			uploadURL = u
		}

		// Presigning is a write: the attachment row is committed above,
		// and the URL is only what lets the bytes follow it.
		recordCalendarEventChange(ctx, deps, wsID, cal.ID, actorID, evt.PublicID, evt.Title, mutationlog.Mutation{
			EventType:    eventbus.CalEventAttachmentCreated,
			AuditAction:  "calendar.attachment.create",
			ResourceType: "calendar.attachment",
			ResourceID:   attPub.String(),
			Payload: map[string]any{
				"eventId":      input.EvtID,
				"calendarId":   input.CalID,
				"attachmentId": attPub.String(),
				"filename":     input.Body.Filename,
				"deduplicated": deduplicated,
			},
			CallSite: "calendars.PresignAttachment",
		})

		out := &PresignAttachmentOutput{}
		out.Body.UploadURL = uploadURL
		out.Body.StorageKey = storageKey
		out.Body.AttachmentID = attPub.String()
		out.Body.Deduplicated = deduplicated
		return out, nil
	}
}

// DownloadAttachment returns a signed GET URL for an event
// attachment. The relay emits Content-Disposition in RFC 5987 form so
// non-ASCII filenames (e.g. Japanese) survive the round trip without
// mojibake.
func DownloadAttachment(deps Deps) func(context.Context, *DownloadAttachmentInput) (*DownloadAttachmentOutput, error) {
	return func(ctx context.Context, input *DownloadAttachmentInput) (*DownloadAttachmentOutput, error) {
		if deps.Storage == nil {
			return nil, httpErr(apierrors.InternalStorageNotConfigured)
		}
		wsID, actorID, err := resolveWorkspace(ctx, deps.Queries, input.WsID)
		if err != nil {
			return nil, err
		}
		cal, _, err := resolveCalendar(ctx, deps.CalendarQueries, wsID, actorID, input.CalID)
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

		downloadURL, err := deps.Storage.SignedDownloadURL(att.StorageKey, att.Filename, calendarPresignExpiry)
		if err != nil {
			return nil, httpErr(apierrors.InternalUnexpected)
		}

		// Audit only, and there is no writer that owns an event for this
		// one: a download changes nothing about the workspace, and a
		// timeline row per file open would bury the changes that do. The
		// audit row is still required, because the URL carries the stored
		// bytes to whoever holds it — an administrator investigating an
		// exposure is asking exactly which files left, and only this row
		// answers.
		attPubID := att.PublicID.String()
		recordCalendarAudit(ctx, deps, wsID, actorID, mutationlog.Mutation{
			AuditAction:  "calendar.attachment.download",
			ResourceType: "calendar.attachment",
			ResourceID:   attPubID,
			Payload: map[string]any{
				"eventId":      input.EvtID,
				"calendarId":   input.CalID,
				"attachmentId": attPubID,
			},
			CallSite: "calendars.DownloadAttachment",
		})

		out := &DownloadAttachmentOutput{}
		out.Body.DownloadURL = downloadURL
		return out, nil
	}
}

// DeleteAttachment soft-deletes an event attachment, decrements the
// underlying storage_objects ref_count, and removes the MinIO blob if
// no other references remain. The DB updates and the blob's
// storage_blob_removals record commit in one transaction; the removal
// runs after commit, and on failure the record stays for the sweeper.
func DeleteAttachment(deps Deps) func(context.Context, *DeleteAttachmentInput) (*DeleteAttachmentOutput, error) {
	return func(ctx context.Context, input *DeleteAttachmentInput) (*DeleteAttachmentOutput, error) {
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

		// Pre-check ownership against the existing row outside the
		// transaction so the permission error remains the dominant
		// failure mode (and we do not even open a txn for forbidden
		// callers). The handler still re-reads inside the txn for the
		// storage_object_id needed by the ref-count drop.
		att, err := deps.CalendarQueries.FindCalendarEventAttachmentByPublicId(ctx, calendar.FindCalendarEventAttachmentByPublicIdParams{
			PublicID:    types.FromUUID(attUID),
			EventID:     handlerutil.NullInt32From(evt.ID),
			WorkspaceID: wsID,
		})
		if err != nil {
			return nil, httpErr(apierr.SpecForErrNoRows(err, apierrors.CalendarAttachmentNotFound, apierrors.CalendarAttachmentStoreReadInterrupted))
		}

		isUploader := att.UploaderID == actorID
		isCalOwner := cal.OwnerUserID.Valid && cal.OwnerUserID.Int32 == int32(actorID) //#nosec G115 -- actor user id sourced from session, fits int32 within realistic deployments
		if !isUploader && !isCalOwner {
			return nil, httpErr(apierrors.CalendarAttachmentUploaderOrOwnerRequired)
		}

		tx, err := deps.DB.BeginTx(ctx, nil)
		if err != nil {
			return nil, httpErr(apierrors.InternalUnexpected)
		}
		defer func() { _ = tx.Rollback() }()
		qtx := deps.Queries.WithTx(tx)
		cqtx := deps.CalendarQueries.WithTx(tx)

		rows, err := cqtx.DeleteCalendarEventAttachment(ctx, calendar.DeleteCalendarEventAttachmentParams{
			PublicID:    types.FromUUID(attUID),
			EventID:     handlerutil.NullInt32From(evt.ID),
			WorkspaceID: wsID,
		})
		if err != nil {
			return nil, httpErr(apierrors.CalendarAttachmentStoreDeleteInterrupted)
		}
		// Nothing was deleted, so the ref count below must not be
		// decremented -- doing that on a delete that did not happen is
		// how a blob still in use becomes eligible for collection.
		if rows == 0 {
			return nil, httpErr(apierrors.CalendarAttachmentNotFound)
		}

		decRes, err := qtx.DecrementStorageObjectRefCount(ctx, att.StorageObjectID)
		if err != nil {
			return nil, httpErr(apierrors.CalendarAttachmentStoreDeleteInterrupted)
		}
		if affected, _ := decRes.RowsAffected(); affected != 1 {
			slog.WarnContext(ctx, "storage object ref_count underflow",
				logutil.LogEntity("attachment", attUID),
				slog.String("handler", "calendars.DeleteAttachment"),
			)
		}

		// Pre-read the storage key so we still have it after a
		// successful unreferenced delete (the row is gone afterward).
		fullRow, lookupErr := qtx.FindStorageObjectByID(ctx, att.StorageObjectID)
		if lookupErr != nil {
			return nil, httpErr(apierrors.InternalUnexpected)
		}

		var storageKeyToRemove string
		gcRes, err := qtx.DeleteStorageObjectIfUnreferenced(ctx, att.StorageObjectID)
		if err != nil {
			return nil, httpErr(apierrors.CalendarAttachmentStoreDeleteInterrupted)
		}
		if affected, _ := gcRes.RowsAffected(); affected == 1 {
			storageKeyToRemove = fullRow.StorageKey
			if err := storagegc.Queue(ctx, qtx, storageKeyToRemove); err != nil {
				return nil, httpErr(apierrors.CalendarAttachmentStoreDeleteInterrupted)
			}
		}

		if err := tx.Commit(); err != nil {
			return nil, httpErr(apierrors.InternalUnexpected)
		}

		if storageKeyToRemove != "" && deps.Storage != nil {
			cleanupCtx, cleanupCancel := ctxutil.Cleanup(ctx, 5*time.Second)
			if rmErr := storagegc.RemoveQueued(cleanupCtx, deps.Queries, deps.Storage, storageKeyToRemove); rmErr != nil {
				slog.WarnContext(cleanupCtx, "calendar attachment delete: orphan cleanup failed; the sweeper will retry",
					slog.Any("err", rmErr),
					slog.String("storage_key", storageKeyToRemove),
				)
			}
			cleanupCancel()
		}

		out := &DeleteAttachmentOutput{}
		out.Body.Deleted = true

		recordCalendarEventChange(ctx, deps, wsID, cal.ID, actorID, evt.PublicID, evt.Title, mutationlog.Mutation{
			EventType:    eventbus.CalEventAttachmentDeleted,
			AuditAction:  "calendar.attachment.delete",
			ResourceType: "calendar.attachment",
			ResourceID:   input.AttID,
			Payload: map[string]any{
				"eventId":      input.EvtID,
				"calendarId":   input.CalID,
				"attachmentId": input.AttID,
			},
			CallSite: "calendars.DeleteAttachment",
		})

		return out, nil
	}
}
