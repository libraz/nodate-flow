package calendars

import (
	"context"
	"database/sql"
	"encoding/hex"
	stderrors "errors"
	"log/slog"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/generated"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/generated/calendar"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/types"
	apierrors "github.com/libraz/nodate-flow/apps/flow-api/internal/errors"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/http/handlers/handlerutil"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/storage"
	"github.com/libraz/nodate-flow/packages/go-shared/apierr"
	"github.com/libraz/nodate-flow/packages/go-shared/ctxutil"
)

const calendarPresignExpiry = 15 * time.Minute

// calendarPresignMaxAttempts bounds the duplicate-entry retry loop in
// PresignAttachment. Mirrors tasks.presignMaxAttempts: under
// REPEATABLE READ a same-tx re-find after MySQL 1062 still misses the
// winning racer, so we roll the tx back and start fresh. Three is a
// defensive ceiling; convergence in practice happens by attempt 1.
const calendarPresignMaxAttempts = 3

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
		ContentType string `json:"contentType" minLength:"1" maxLength:"255" doc:"MIME type"`
		ByteSize    uint64 `json:"byteSize" minimum:"1" maximum:"104857600" doc:"File size in bytes (max 100 MB)"`
		Sha256      string `json:"sha256" minLength:"64" maxLength:"64" pattern:"^[0-9a-f]{64}$" doc:"Lowercase hex SHA-256 digest of the file body (64 chars). Drives content-addressed dedup."`
	}
}

// PresignAttachmentOutput is the response for the presign endpoint.
//
// RequiredHeaders mirrors tasks.PresignUploadOutputBody.RequiredHeaders:
// when non-empty the client MUST send each header verbatim alongside
// the PUT, since they are folded into the SigV4 signed-headers list
// and any mismatch causes the bucket to reject the upload. The server
// emits a single x-amz-content-sha256 entry to bind the upload body
// to the digest the client claimed, defeating content-vs-hash
// poisoning of the dedup row. Omitted on the deduplicated branch.
type PresignAttachmentOutput struct {
	Body struct {
		UploadURL       string            `json:"uploadUrl,omitempty" doc:"Presigned PUT URL. Empty when deduplicated=true."`
		StorageKey      string            `json:"storageKey" doc:"Object key (informational; the presigned URL already encodes it)."`
		AttachmentID    string            `json:"attachmentId" doc:"Public ID of the created attachment row"`
		Deduplicated    bool              `json:"deduplicated" doc:"True when the server reused an existing blob and no upload is needed."`
		RequiredHeaders map[string]string `json:"requiredHeaders,omitempty" doc:"Headers the client MUST send verbatim with the PUT. Currently emits x-amz-content-sha256 to bind the upload body to the claimed digest. Empty/omitted when deduplicated=true."`
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
	DownloadURL string `json:"downloadUrl" doc:"Presigned GET URL with Content-Disposition: attachment"`
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
// row and returns a presigned PUT URL.
func PresignAttachment(deps Deps) func(context.Context, *PresignAttachmentInput) (*PresignAttachmentOutput, error) {
	return func(ctx context.Context, input *PresignAttachmentInput) (*PresignAttachmentOutput, error) {
		if deps.Storage == nil {
			return nil, httpErr(apierrors.InternalStorageNotConfigured)
		}
		// Cross-surface upload validation: mirror the task presign path
		// so calendar attachments enforce the same MIME allowlist,
		// blocked-extension deny list, and per-file size ceiling.
		if !handlerutil.IsAllowedContentType(input.Body.ContentType) {
			return nil, httpErr(apierrors.ValidationFileTypeNotAllowed)
		}
		if handlerutil.HasBlockedExtension(input.Body.Filename) {
			return nil, httpErr(apierrors.ValidationFileTypeNotAllowed)
		}
		if input.Body.ByteSize > handlerutil.MaxUploadSize {
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

		// Build the content-addressed storage key. The wsId path
		// param is already the workspace public_id (UUID v7) and
		// resolveWorkspace above has authenticated it, so we can
		// derive the hex prefix straight from the input rather than
		// re-querying the row.
		wsUID, err := uuid.Parse(input.WsID)
		if err != nil {
			return nil, httpErr(apierrors.ValidationPathParamInvalid)
		}
		wsHex := hex.EncodeToString(wsUID[:])
		shaHex := strings.ToLower(input.Body.Sha256)
		baseStorageKey := storage.KeyForWorkspace(wsHex, shaHex)

		var (
			storageObjectID    uint32
			deduplicated       bool
			existingUploadedAt sql.NullTime
			storageKey         string
			attPub             = types.New()
			committed          bool
		)

	attempts:
		for attempt := 0; attempt < calendarPresignMaxAttempts; attempt++ {
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

			storageKey = baseStorageKey
			deduplicated = false

			existing, findErr := qtx.FindStorageObjectByWorkspaceSha(ctx, generated.FindStorageObjectByWorkspaceShaParams{
				WorkspaceID: handlerutil.NullInt32From(wsID),
				Sha256:      shaBytes,
			})
			switch {
			case findErr == nil:
				if err := qtx.IncrementStorageObjectRefCount(ctx, existing.ID); err != nil {
					rollback()
					return nil, httpErr(apierrors.CalendarAttachmentStoreWriteInterrupted)
				}
				storageObjectID = existing.ID
				storageKey = existing.StorageKey
				existingUploadedAt = existing.UploadedAt
				deduplicated = true
			case stderrors.Is(findErr, sql.ErrNoRows):
				// Miss: allocate a new storage_objects row with ref_count=1.
				soPub := types.New()
				insRes, insErr := qtx.InsertStorageObject(ctx, generated.InsertStorageObjectParams{
					PublicID:    soPub,
					WorkspaceID: handlerutil.NullInt32From(wsID),
					OwnerUserID: sql.NullInt32{},
					Sha256:      shaBytes,
					ByteSize:    input.Body.ByteSize,
					ContentType: input.Body.ContentType,
					StorageKey:  storageKey,
				})
				switch {
				case insErr == nil:
					lastID, idErr := insRes.LastInsertId()
					if idErr != nil || lastID <= 0 {
						rollback()
						return nil, httpErr(apierrors.CalendarAttachmentStoreWriteInterrupted)
					}
					storageObjectID = uint32(lastID) //#nosec G115 -- AUTO_INCREMENT id fits uint32 within realistic deployments
				case handlerutil.IsDuplicateEntry(insErr):
					// Race lost: roll back so the next attempt's
					// REPEATABLE READ snapshot can observe the
					// winning racer's committed row. A same-tx
					// re-find would still see ErrNoRows because the
					// snapshot was pinned at our first SELECT above.
					rollback()
					continue attempts
				default:
					rollback()
					return nil, httpErr(apierrors.CalendarAttachmentStoreWriteInterrupted)
				}
			default:
				rollback()
				return nil, httpErr(apierrors.CalendarAttachmentStoreReadInterrupted)
			}

			if _, err := cqtx.CreateCalendarEventAttachment(ctx, calendar.CreateCalendarEventAttachmentParams{
				PublicID:        attPub,
				WorkspaceID:     wsID,
				EventID:         handlerutil.NullInt32From(evt.ID),
				UploaderID:      actorID,
				StorageObjectID: storageObjectID,
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

		// A dedup hit promises the bytes are already stored, and until
		// now nothing checked whether they were. The storage_objects
		// row is committed at presign time, before the client has
		// uploaded anything, so an upload the client starts and never
		// finishes leaves a row claiming that content exists at a key
		// holding nothing. Every later upload of the same content then
		// dedups onto that row, is told it need not upload, and ends up
		// attached to an object that will never exist — the first
		// abandoned upload of a given file poisons that file for the
		// whole workspace, permanently.
		//
		// Asking the store settles it. A claim with no object behind it
		// is treated as a miss: the caller gets an upload URL for the
		// very same key, so the row it already points at is filled in
		// rather than duplicated. That repairs rows poisoned before
		// this check existed, on the next attempt to use them, without
		// a migration.
		//
		// The check is deliberately outside the transaction: it is a
		// network round trip to the object store, and holding a
		// database transaction open across one is how lock contention
		// becomes an outage.
		if deduplicated {
			// Two different ways the promise can be false, and neither
			// implies the other.
			//
			// A row with no uploaded_at is a reservation: it was
			// created to hand out an upload URL, and the only size
			// anyone has for it is the one the client declared. That is
			// exactly the number an attacker lies about — declare one
			// byte, send ten gigabytes, never confirm — so treating it
			// as stored content would let the lie be inherited by
			// everyone who uploads the same file afterwards.
			//
			// A row that was confirmed can still have lost its object
			// afterwards, to a bucket lifecycle rule or a partial
			// restore, and no column records that. Only the store knows.
			if !existingUploadedAt.Valid {
				deduplicated = false
			} else if _, statErr := deps.Storage.StatObject(ctx, storageKey); statErr != nil {
				deduplicated = false
			}
		}

		// Generate the presigned PUT URL only on the miss branch.
		// PresignPutWithSha256 binds x-amz-content-sha256 into the
		// SigV4 signed-headers list so the bucket rejects an upload
		// whose body hash does not match the value the client claimed
		// — which is precisely the value we keyed the dedup row on.
		// Without this binding a malicious client could upload bytes
		// B under a presign minted for sha=A, poisoning the row that
		// the next legitimate uploader of A would dedup onto.
		var (
			uploadURL       string
			requiredHeaders map[string]string
		)
		if !deduplicated {
			u, err := deps.Storage.PresignPutWithSha256(ctx, storageKey, shaHex, calendarPresignExpiry)
			if err != nil {
				return nil, httpErr(apierrors.InternalUnexpected)
			}
			uploadURL = u
			requiredHeaders = map[string]string{"x-amz-content-sha256": shaHex}
		}

		_ = appendCalendarEvent(ctx, deps.DB, wsID, "calendar.event.attachment.created", &actorID, map[string]any{
			"eventId":      input.EvtID,
			"calendarId":   input.CalID,
			"attachmentId": attPub.String(),
			"filename":     input.Body.Filename,
			"deduplicated": deduplicated,
		})

		out := &PresignAttachmentOutput{}
		out.Body.UploadURL = uploadURL
		out.Body.StorageKey = storageKey
		out.Body.AttachmentID = attPub.String()
		out.Body.Deduplicated = deduplicated
		out.Body.RequiredHeaders = requiredHeaders
		return out, nil
	}
}

// DownloadAttachment returns a presigned GET URL for an event
// attachment. The Content-Disposition header is RFC 5987 encoded so
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

		downloadURL, err := deps.Storage.PresignGet(ctx, att.StorageKey, att.Filename, calendarPresignExpiry)
		if err != nil {
			return nil, httpErr(apierrors.InternalUnexpected)
		}

		out := &DownloadAttachmentOutput{}
		out.Body.DownloadURL = downloadURL
		return out, nil
	}
}

// DeleteAttachment soft-deletes an event attachment, decrements the
// underlying storage_objects ref_count, and removes the MinIO blob if
// no other references remain. The DB updates run in a single
// transaction; the MinIO RemoveObject call happens after commit and
// emits a warn log on failure (the GC sweeper will pick up any
// orphans on a later pass).
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

		err = cqtx.DeleteCalendarEventAttachment(ctx, calendar.DeleteCalendarEventAttachmentParams{
			PublicID:    types.FromUUID(attUID),
			EventID:     handlerutil.NullInt32From(evt.ID),
			WorkspaceID: wsID,
		})
		if err != nil {
			return nil, httpErr(apierrors.CalendarAttachmentStoreDeleteInterrupted)
		}

		decRes, err := qtx.DecrementStorageObjectRefCount(ctx, att.StorageObjectID)
		if err != nil {
			return nil, httpErr(apierrors.CalendarAttachmentStoreDeleteInterrupted)
		}
		if affected, _ := decRes.RowsAffected(); affected != 1 {
			slog.WarnContext(ctx, "storage object ref_count underflow",
				slog.Uint64("storage_object_id", uint64(att.StorageObjectID)),
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
		}

		if err := tx.Commit(); err != nil {
			return nil, httpErr(apierrors.InternalUnexpected)
		}

		if storageKeyToRemove != "" && deps.Storage != nil {
			cleanupCtx, cleanupCancel := ctxutil.Cleanup(ctx, 5*time.Second)
			if rmErr := deps.Storage.RemoveObject(cleanupCtx, storageKeyToRemove); rmErr != nil {
				slog.WarnContext(cleanupCtx, "calendar attachment delete: orphan cleanup failed",
					slog.Any("err", rmErr),
					slog.String("storage_key", storageKeyToRemove),
				)
			}
			cleanupCancel()
		}

		out := &DeleteAttachmentOutput{}
		out.Body.Deleted = true

		_ = appendCalendarEvent(ctx, deps.DB, wsID, "calendar.event.attachment.deleted", &actorID, map[string]any{
			"eventId":      input.EvtID,
			"calendarId":   input.CalID,
			"attachmentId": input.AttID,
		})

		return out, nil
	}
}
