package calendars

import (
	"context"
	"database/sql"

	"github.com/google/uuid"

	"github.com/nodate-flow/nodate-flow/apps/flow-api/internal/db/generated/calendar"
	"github.com/nodate-flow/nodate-flow/apps/flow-api/internal/db/types"
	apierrors "github.com/nodate-flow/nodate-flow/apps/flow-api/internal/errors"
	"github.com/nodate-flow/nodate-flow/apps/flow-api/internal/http/handlers/handlerutil"
	"github.com/nodate-flow/nodate-flow/packages/go-shared/apierr"
)

// --- Input/Output types ---

// ListAttachmentsInput is the input for listing event attachments.
type ListAttachmentsInput struct {
	WsID  string `path:"wsId" doc:"Workspace public ID"`
	CalID string `path:"calId" doc:"Calendar public ID"`
	EvtID string `path:"evtId" doc:"Event public ID"`
}

// AttachmentResponse is the JSON representation of an event attachment.
type AttachmentResponse struct {
	ID           string `json:"id"`
	Filename     string `json:"filename"`
	ContentType  string `json:"contentType"`
	ByteSize     uint64 `json:"byteSize"`
	StorageKey   string `json:"storageKey"`
	UploaderID   string `json:"uploaderId"`
	UploaderName string `json:"uploaderName"`
	CreatedAt    int64  `json:"createdAt"`
}

// ListAttachmentsOutput is the response for the list attachments endpoint.
type ListAttachmentsOutput struct {
	Body struct {
		Attachments []AttachmentResponse `json:"attachments"`
	}
}

// CreateAttachmentInput is the input for creating an attachment metadata record.
type CreateAttachmentInput struct {
	WsID  string `path:"wsId" doc:"Workspace public ID"`
	CalID string `path:"calId" doc:"Calendar public ID"`
	EvtID string `path:"evtId" doc:"Event public ID"`
	Body  struct {
		Filename       string `json:"filename" minLength:"1" maxLength:"255" doc:"Original filename"`
		ContentType    string `json:"contentType" minLength:"1" maxLength:"255" doc:"MIME content type"`
		ByteSize       uint64 `json:"byteSize" doc:"File size in bytes"`
		StorageKey     string `json:"storageKey" minLength:"1" maxLength:"1024" doc:"S3 object key"`
		ChecksumSha256 string `json:"checksumSha256,omitempty" required:"false" maxLength:"64" doc:"SHA-256 checksum"`
	}
}

// CreateAttachmentOutput is the response for the create attachment endpoint.
type CreateAttachmentOutput struct {
	Body AttachmentResponse
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

// ListAttachments returns all attachments on an event.
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

		rows, err := deps.CalendarQueries.ListCalendarEventAttachments(ctx, evt.ID)
		if err != nil {
			return nil, httpErr(apierrors.CalendarAttachmentListQueryInterrupted)
		}

		out := &ListAttachmentsOutput{}
		out.Body.Attachments = make([]AttachmentResponse, len(rows))
		for i, r := range rows {
			out.Body.Attachments[i] = AttachmentResponse{
				ID:           r.PublicID.String(),
				Filename:     r.Filename,
				ContentType:  r.ContentType,
				ByteSize:     r.ByteSize,
				StorageKey:   r.StorageKey,
				UploaderID:   r.UserPublicID.String(),
				UploaderName: r.DisplayName,
				CreatedAt:    r.CreatedAt.Unix(),
			}
		}
		return out, nil
	}
}

// CreateAttachment records metadata for an uploaded file attachment on an event.
func CreateAttachment(deps Deps) func(context.Context, *CreateAttachmentInput) (*CreateAttachmentOutput, error) {
	return func(ctx context.Context, input *CreateAttachmentInput) (*CreateAttachmentOutput, error) {
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

		attPublicID := types.New()
		var checksum sql.NullString
		if input.Body.ChecksumSha256 != "" {
			checksum = sql.NullString{String: input.Body.ChecksumSha256, Valid: true}
		}

		_, err = deps.CalendarQueries.CreateCalendarEventAttachment(ctx, calendar.CreateCalendarEventAttachmentParams{
			PublicID:       attPublicID,
			WorkspaceID:    wsID,
			EventID:        evt.ID,
			UploaderID:     actorID,
			Filename:       input.Body.Filename,
			ContentType:    input.Body.ContentType,
			ByteSize:       input.Body.ByteSize,
			StorageKey:     input.Body.StorageKey,
			ChecksumSha256: checksum,
		})
		if err != nil {
			return nil, httpErr(apierrors.CalendarAttachmentStoreWriteInterrupted)
		}

		profile, err := deps.Queries.FindUserProfileById(ctx, actorID)
		if err != nil {
			return nil, httpErr(apierrors.CalendarUserProfileLookupInterrupted)
		}

		out := &CreateAttachmentOutput{}
		out.Body = AttachmentResponse{
			ID:           attPublicID.String(),
			Filename:     input.Body.Filename,
			ContentType:  input.Body.ContentType,
			ByteSize:     input.Body.ByteSize,
			StorageKey:   input.Body.StorageKey,
			UploaderID:   profile.PublicID.String(),
			UploaderName: profile.DisplayName,
			CreatedAt:    handlerutil.NowUnix(),
		}

		_ = appendCalendarEvent(ctx, deps.DB, wsID, "calendar.event.attachment.created", &actorID, map[string]any{
			"eventId":      input.EvtID,
			"calendarId":   input.CalID,
			"attachmentId": attPublicID.String(),
			"filename":     input.Body.Filename,
		})

		return out, nil
	}
}

// DeleteAttachment soft-deletes an attachment from an event.
func DeleteAttachment(deps Deps) func(context.Context, *DeleteAttachmentInput) (*DeleteAttachmentOutput, error) {
	return func(ctx context.Context, input *DeleteAttachmentInput) (*DeleteAttachmentOutput, error) {
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
			EventID:     evt.ID,
			WorkspaceID: wsID,
		})
		if err != nil {
			return nil, httpErr(apierr.SpecForErrNoRows(err, apierrors.CalendarAttachmentNotFound, apierrors.CalendarAttachmentStoreReadInterrupted))
		}

		isUploader := att.UploaderID == actorID
		// Subscription role has been dropped; fall back to calendar
		// ownership.
		isCalOwner := cal.OwnerUserID.Valid && cal.OwnerUserID.Int32 == int32(actorID)
		if !isUploader && !isCalOwner {
			return nil, httpErr(apierrors.CalendarAttachmentUploaderOrOwnerRequired)
		}

		err = deps.CalendarQueries.DisableCalendarEventAttachment(ctx, calendar.DisableCalendarEventAttachmentParams{
			PublicID:    types.FromUUID(attUID),
			EventID:     evt.ID,
			WorkspaceID: wsID,
		})
		if err != nil {
			return nil, httpErr(apierrors.CalendarAttachmentStoreDeleteInterrupted)
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
