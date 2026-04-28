package calendars

import (
	"context"

	"github.com/google/uuid"

	"github.com/nodate-flow/nodate-flow/apps/flow-api/internal/db/generated/calendar"
	"github.com/nodate-flow/nodate-flow/apps/flow-api/internal/db/types"
	apierrors "github.com/nodate-flow/nodate-flow/apps/flow-api/internal/errors"
	"github.com/nodate-flow/nodate-flow/apps/flow-api/internal/http/handlers/handlerutil"
	"github.com/nodate-flow/nodate-flow/packages/go-shared/apierr"
	"github.com/nodate-flow/nodate-flow/packages/go-shared/dbtype"
)

// --- Input/Output types ---

// ListCommentsInput is the input for listing event comments.
type ListCommentsInput struct {
	WsID  string `path:"wsId" doc:"Workspace public ID"`
	CalID string `path:"calId" doc:"Calendar public ID"`
	EvtID string `path:"evtId" doc:"Event public ID"`
}

// CommentResponse is the JSON representation of an event comment.
type CommentResponse struct {
	ID          string  `json:"id"`
	UserID      string  `json:"userId"`
	DisplayName string  `json:"displayName"`
	AvatarURL   *string `json:"avatarUrl,omitempty"`
	Body        string  `json:"body"`
	EditedAt    *int64  `json:"editedAt,omitempty"`
	CreatedAt   int64   `json:"createdAt"`
}

// ListCommentsOutput is the response for the list comments endpoint.
type ListCommentsOutput struct {
	Body struct {
		Comments []CommentResponse `json:"comments"`
	}
}

// CreateCommentInput is the input for creating an event comment.
type CreateCommentInput struct {
	WsID  string `path:"wsId" doc:"Workspace public ID"`
	CalID string `path:"calId" doc:"Calendar public ID"`
	EvtID string `path:"evtId" doc:"Event public ID"`
	Body  struct {
		Body string `json:"body" minLength:"1" maxLength:"5000" doc:"Comment text"`
	}
}

// CreateCommentOutput is the response for the create comment endpoint.
type CreateCommentOutput struct {
	Body CommentResponse
}

// EditCommentInput is the input for editing a comment.
type EditCommentInput struct {
	WsID  string `path:"wsId" doc:"Workspace public ID"`
	CalID string `path:"calId" doc:"Calendar public ID"`
	EvtID string `path:"evtId" doc:"Event public ID"`
	CId   string `path:"cId" doc:"Comment public ID"`
	Body  struct {
		Body string `json:"body" minLength:"1" maxLength:"5000" doc:"Updated comment text"`
	}
}

// EditCommentOutput is the response for the edit comment endpoint.
type EditCommentOutput struct {
	Body struct {
		Updated bool `json:"updated"`
	}
}

// DeleteCommentInput is the input for deleting a comment.
type DeleteCommentInput struct {
	WsID  string `path:"wsId" doc:"Workspace public ID"`
	CalID string `path:"calId" doc:"Calendar public ID"`
	EvtID string `path:"evtId" doc:"Event public ID"`
	CId   string `path:"cId" doc:"Comment public ID"`
}

// DeleteCommentOutput is the response for the delete comment endpoint.
type DeleteCommentOutput struct {
	Body struct {
		Deleted bool `json:"deleted"`
	}
}

// --- Handlers ---

// ListComments returns all comments on an event in chronological order.
func ListComments(deps Deps) func(context.Context, *ListCommentsInput) (*ListCommentsOutput, error) {
	return func(ctx context.Context, input *ListCommentsInput) (*ListCommentsOutput, error) {
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

		rows, err := deps.CalendarQueries.ListCalendarEventComments(ctx, handlerutil.NullInt32From(evt.ID))
		if err != nil {
			return nil, httpErr(apierrors.CalendarCommentListQueryInterrupted)
		}

		out := &ListCommentsOutput{}
		out.Body.Comments = make([]CommentResponse, len(rows))
		for i, r := range rows {
			resp := CommentResponse{
				ID:          r.PublicID.String(),
				UserID:      r.UserPublicID.String(),
				DisplayName: r.DisplayName,
				Body:        r.Body,
				CreatedAt:   r.CreatedAt.Unix(),
			}
			resp.AvatarURL = dbtype.PtrFromNullString(r.AvatarUrl)
			resp.EditedAt = dbtype.UnixSecondsFromNullTime(r.EditedAt)
			out.Body.Comments[i] = resp
		}
		return out, nil
	}
}

// CreateComment adds a comment to an event.
func CreateComment(deps Deps) func(context.Context, *CreateCommentInput) (*CreateCommentOutput, error) {
	return func(ctx context.Context, input *CreateCommentInput) (*CreateCommentOutput, error) {
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

		commentPublicID := types.New()
		_, err = deps.CalendarQueries.CreateCalendarEventComment(ctx, calendar.CreateCalendarEventCommentParams{
			PublicID:    commentPublicID,
			WorkspaceID: wsID,
			EventID:     handlerutil.NullInt32From(evt.ID),
			AuthorID:    actorID,
			Body:        input.Body.Body,
		})
		if err != nil {
			return nil, httpErr(apierrors.CalendarCommentStoreWriteInterrupted)
		}

		profile, err := deps.Queries.FindUserProfileById(ctx, actorID)
		if err != nil {
			return nil, httpErr(apierrors.CalendarUserProfileLookupInterrupted)
		}

		out := &CreateCommentOutput{}
		out.Body = CommentResponse{
			ID:          commentPublicID.String(),
			UserID:      profile.PublicID.String(),
			DisplayName: profile.DisplayName,
			Body:        input.Body.Body,
			CreatedAt:   handlerutil.NowUnix(),
		}
		out.Body.AvatarURL = dbtype.PtrFromNullString(profile.AvatarUrl)

		_ = appendCalendarEvent(ctx, deps.DB, wsID, "calendar.event.comment.created", &actorID, map[string]any{
			"eventId":    input.EvtID,
			"calendarId": input.CalID,
			"commentId":  commentPublicID.String(),
		})

		return out, nil
	}
}

// EditComment edits a comment. Only the author can edit their own comment.
func EditComment(deps Deps) func(context.Context, *EditCommentInput) (*EditCommentOutput, error) {
	return func(ctx context.Context, input *EditCommentInput) (*EditCommentOutput, error) {
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

		commentUID, err := uuid.Parse(input.CId)
		if err != nil {
			return nil, httpErr(apierrors.CalendarCommentNotFound)
		}

		err = deps.CalendarQueries.UpdateCalendarEventComment(ctx, calendar.UpdateCalendarEventCommentParams{
			Body:        input.Body.Body,
			PublicID:    types.FromUUID(commentUID),
			EventID:     handlerutil.NullInt32From(evt.ID),
			WorkspaceID: wsID,
			AuthorID:    actorID,
		})
		if err != nil {
			return nil, httpErr(apierrors.CalendarCommentStoreWriteInterrupted)
		}

		_ = appendCalendarEvent(ctx, deps.DB, wsID, "calendar.event.comment.updated", &actorID, map[string]any{
			"eventId":    input.EvtID,
			"calendarId": input.CalID,
			"commentId":  input.CId,
		})

		out := &EditCommentOutput{}
		out.Body.Updated = true
		return out, nil
	}
}

// DeleteComment deletes a comment. The author or a calendar owner can delete.
func DeleteComment(deps Deps) func(context.Context, *DeleteCommentInput) (*DeleteCommentOutput, error) {
	return func(ctx context.Context, input *DeleteCommentInput) (*DeleteCommentOutput, error) {
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

		commentUID, err := uuid.Parse(input.CId)
		if err != nil {
			return nil, httpErr(apierrors.CalendarCommentNotFound)
		}

		comment, err := deps.CalendarQueries.FindCalendarEventCommentByPublicId(ctx, calendar.FindCalendarEventCommentByPublicIdParams{
			PublicID:    types.FromUUID(commentUID),
			EventID:     handlerutil.NullInt32From(evt.ID),
			WorkspaceID: wsID,
		})
		if err != nil {
			return nil, httpErr(apierr.SpecForErrNoRows(err, apierrors.CalendarCommentNotFound, apierrors.CalendarCommentStoreReadInterrupted))
		}

		isAuthor := comment.AuthorID == actorID
		// Subscription role has been dropped; fall back to calendar
		// ownership.
		isCalOwner := cal.OwnerUserID.Valid && cal.OwnerUserID.Int32 == int32(actorID) //#nosec G115 -- actor user id sourced from session, fits int32 within realistic deployments
		if !isAuthor && !isCalOwner {
			return nil, httpErr(apierrors.CalendarCommentAuthorOrOwnerRequired)
		}

		err = deps.CalendarQueries.DisableCalendarEventComment(ctx, calendar.DisableCalendarEventCommentParams{
			PublicID:    types.FromUUID(commentUID),
			EventID:     handlerutil.NullInt32From(evt.ID),
			WorkspaceID: wsID,
		})
		if err != nil {
			return nil, httpErr(apierrors.CalendarCommentStoreDeleteInterrupted)
		}

		out := &DeleteCommentOutput{}
		out.Body.Deleted = true

		_ = appendCalendarEvent(ctx, deps.DB, wsID, "calendar.event.comment.deleted", &actorID, map[string]any{
			"eventId":    input.EvtID,
			"calendarId": input.CalID,
			"commentId":  input.CId,
		})

		return out, nil
	}
}
