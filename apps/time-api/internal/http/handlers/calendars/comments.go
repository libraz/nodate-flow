package calendars

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"github.com/google/uuid"

	generated "github.com/nodate-flow/nodate-flow/apps/time-api/internal/db/generated"
	"github.com/nodate-flow/nodate-flow/apps/time-api/internal/db/types"
	"github.com/nodate-flow/nodate-flow/apps/time-api/internal/eventbus"
)

// --- Input/Output types ---

// ListCommentsInput is the input for listing event comments.
type ListCommentsInput struct {
	WsId  string `path:"wsId" doc:"Workspace public ID"`
	CalId string `path:"calId" doc:"Calendar public ID"`
	EvtId string `path:"evtId" doc:"Event public ID"`
}

// CommentResponse is the JSON representation of an event comment.
type CommentResponse struct {
	ID          string     `json:"id"`
	UserID      string     `json:"userId"`
	DisplayName string     `json:"displayName"`
	AvatarUrl   *string    `json:"avatarUrl,omitempty"`
	Body        string     `json:"body"`
	EditedAt    *time.Time `json:"editedAt,omitempty"`
	CreatedAt   time.Time  `json:"createdAt"`
}

// ListCommentsOutput is the response for the list comments endpoint.
type ListCommentsOutput struct {
	Body struct {
		Comments []CommentResponse `json:"comments"`
	}
}

// CreateCommentInput is the input for creating an event comment.
type CreateCommentInput struct {
	WsId  string `path:"wsId" doc:"Workspace public ID"`
	CalId string `path:"calId" doc:"Calendar public ID"`
	EvtId string `path:"evtId" doc:"Event public ID"`
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
	WsId  string `path:"wsId" doc:"Workspace public ID"`
	CalId string `path:"calId" doc:"Calendar public ID"`
	EvtId string `path:"evtId" doc:"Event public ID"`
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
	WsId  string `path:"wsId" doc:"Workspace public ID"`
	CalId string `path:"calId" doc:"Calendar public ID"`
	EvtId string `path:"evtId" doc:"Event public ID"`
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
		wsID, actorID, err := resolveWorkspace(ctx, deps.Queries, input.WsId)
		if err != nil {
			return nil, err
		}
		cal, _, err := resolveCalendar(ctx, deps.Queries, wsID, actorID, input.CalId)
		if err != nil {
			return nil, err
		}

		evt, err := resolveEvent(ctx, deps.Queries, cal.ID, input.EvtId)
		if err != nil {
			return nil, err
		}

		rows, err := deps.Queries.ListCalendarEventComments(ctx, evt.ID)
		if err != nil {
			return nil, huma.Error500InternalServerError("Failed to list comments", err)
		}

		out := &ListCommentsOutput{}
		out.Body.Comments = make([]CommentResponse, len(rows))
		for i, r := range rows {
			resp := CommentResponse{
				ID:          r.PublicID.String(),
				UserID:      r.UserPublicID.String(),
				DisplayName: r.DisplayName,
				Body:        r.Body,
				CreatedAt:   r.CreatedAt,
			}
			if r.AvatarUrl.Valid {
				resp.AvatarUrl = &r.AvatarUrl.String
			}
			if r.EditedAt.Valid {
				resp.EditedAt = &r.EditedAt.Time
			}
			out.Body.Comments[i] = resp
		}
		return out, nil
	}
}

// CreateComment adds a comment to an event.
func CreateComment(deps Deps) func(context.Context, *CreateCommentInput) (*CreateCommentOutput, error) {
	return func(ctx context.Context, input *CreateCommentInput) (*CreateCommentOutput, error) {
		wsID, actorID, err := resolveWorkspace(ctx, deps.Queries, input.WsId)
		if err != nil {
			return nil, err
		}
		cal, _, err := resolveCalendar(ctx, deps.Queries, wsID, actorID, input.CalId)
		if err != nil {
			return nil, err
		}

		evt, err := resolveEvent(ctx, deps.Queries, cal.ID, input.EvtId)
		if err != nil {
			return nil, err
		}

		commentPublicID := types.New()
		_, err = deps.Queries.CreateCalendarEventComment(ctx, generated.CreateCalendarEventCommentParams{
			PublicID:    commentPublicID,
			WorkspaceID: wsID,
			EventID:     evt.ID,
			AuthorID:    actorID,
			Body:        input.Body.Body,
		})
		if err != nil {
			return nil, huma.Error500InternalServerError("Failed to create comment", err)
		}

		profile, err := deps.Queries.FindUserProfileById(ctx, actorID)
		if err != nil {
			return nil, huma.Error500InternalServerError("Failed to get user profile", err)
		}

		out := &CreateCommentOutput{}
		out.Body = CommentResponse{
			ID:          commentPublicID.String(),
			UserID:      profile.PublicID.String(),
			DisplayName: profile.DisplayName,
			Body:        input.Body.Body,
			CreatedAt:   time.Now().UTC(),
		}
		if profile.AvatarUrl.Valid {
			out.Body.AvatarUrl = &profile.AvatarUrl.String
		}

		_ = eventbus.Append(ctx, deps.DB, wsID, "calendar.event.comment.created", &actorID, map[string]any{
			"eventId":    input.EvtId,
			"calendarId": input.CalId,
			"commentId":  commentPublicID.String(),
		})

		return out, nil
	}
}

// EditComment edits a comment. Only the author can edit their own comment.
func EditComment(deps Deps) func(context.Context, *EditCommentInput) (*EditCommentOutput, error) {
	return func(ctx context.Context, input *EditCommentInput) (*EditCommentOutput, error) {
		wsID, actorID, err := resolveWorkspace(ctx, deps.Queries, input.WsId)
		if err != nil {
			return nil, err
		}
		cal, _, err := resolveCalendar(ctx, deps.Queries, wsID, actorID, input.CalId)
		if err != nil {
			return nil, err
		}

		evt, err := resolveEvent(ctx, deps.Queries, cal.ID, input.EvtId)
		if err != nil {
			return nil, err
		}

		commentUID, err := uuid.Parse(input.CId)
		if err != nil {
			return nil, huma.Error404NotFound("Comment not found")
		}

		err = deps.Queries.UpdateCalendarEventComment(ctx, generated.UpdateCalendarEventCommentParams{
			Body:     input.Body.Body,
			PublicID: types.FromUUID(commentUID),
			EventID:  evt.ID,
			AuthorID: actorID,
		})
		if err != nil {
			return nil, huma.Error500InternalServerError("Failed to update comment", err)
		}

		out := &EditCommentOutput{}
		out.Body.Updated = true
		return out, nil
	}
}

// DeleteComment deletes a comment. The author or a calendar owner can delete.
func DeleteComment(deps Deps) func(context.Context, *DeleteCommentInput) (*DeleteCommentOutput, error) {
	return func(ctx context.Context, input *DeleteCommentInput) (*DeleteCommentOutput, error) {
		wsID, actorID, err := resolveWorkspace(ctx, deps.Queries, input.WsId)
		if err != nil {
			return nil, err
		}
		cal, sub, err := resolveCalendar(ctx, deps.Queries, wsID, actorID, input.CalId)
		if err != nil {
			return nil, err
		}

		evt, err := resolveEvent(ctx, deps.Queries, cal.ID, input.EvtId)
		if err != nil {
			return nil, err
		}

		commentUID, err := uuid.Parse(input.CId)
		if err != nil {
			return nil, huma.Error404NotFound("Comment not found")
		}

		comment, err := deps.Queries.FindCalendarEventCommentByPublicId(ctx, generated.FindCalendarEventCommentByPublicIdParams{
			PublicID: types.FromUUID(commentUID),
			EventID:  evt.ID,
		})
		if err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return nil, huma.Error404NotFound("Comment not found")
			}
			return nil, huma.Error500InternalServerError("Failed to find comment", err)
		}

		isAuthor := comment.AuthorID == actorID
		isCalOwner := sub.Role == generated.CalendarSubscriptionsRoleOwner
		if !isAuthor && !isCalOwner {
			return nil, errForbidden
		}

		err = deps.Queries.DisableCalendarEventComment(ctx, generated.DisableCalendarEventCommentParams{
			PublicID: types.FromUUID(commentUID),
			EventID:  evt.ID,
		})
		if err != nil {
			return nil, huma.Error500InternalServerError("Failed to delete comment", err)
		}

		out := &DeleteCommentOutput{}
		out.Body.Deleted = true

		_ = eventbus.Append(ctx, deps.DB, wsID, "calendar.event.comment.deleted", &actorID, map[string]any{
			"eventId":    input.EvtId,
			"calendarId": input.CalId,
			"commentId":  input.CId,
		})

		return out, nil
	}
}
