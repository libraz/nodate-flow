package calendars

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/google/uuid"

	generated "github.com/nodate-flow/nodate-flow/apps/flow-api/internal/db/generated"
	"github.com/nodate-flow/nodate-flow/apps/flow-api/internal/db/types"
	apierrors "github.com/nodate-flow/nodate-flow/apps/flow-api/internal/errors"
)

// --- Input/Output types ---

// ListMemosInput is the input for the list memos endpoint.
type ListMemosInput struct {
	WsId  string `path:"wsId" doc:"Workspace public ID"`
	CalId string `path:"calId" doc:"Calendar public ID"`
}

// MemoResponse is the JSON representation of a calendar memo.
type MemoResponse struct {
	ID              string `json:"id"`
	Title           string `json:"title"`
	Done            bool   `json:"done"`
	SortWeight      int32  `json:"sortWeight"`
	UserPublicID    string `json:"userPublicId"`
	UserDisplayName string `json:"userDisplayName"`
	UpdatedAt       *int64 `json:"updatedAt,omitempty"`
	CreatedAt       int64  `json:"createdAt"`
}

// ListMemosOutput is the response for the list memos endpoint.
type ListMemosOutput struct {
	Body struct {
		Memos []MemoResponse `json:"memos"`
	}
}

// CreateMemoInput is the input for the create memo endpoint.
type CreateMemoInput struct {
	WsId  string `path:"wsId" doc:"Workspace public ID"`
	CalId string `path:"calId" doc:"Calendar public ID"`
	Body  struct {
		Title      string `json:"title" minLength:"1" maxLength:"500" doc:"Memo title"`
		SortWeight int32  `json:"sortWeight" required:"false" doc:"Sort weight for ordering"`
	}
}

// CreateMemoOutput is the response for the create memo endpoint.
type CreateMemoOutput struct {
	Body MemoResponse
}

// UpdateMemoInput is the input for the update memo endpoint.
type UpdateMemoInput struct {
	WsId   string `path:"wsId" doc:"Workspace public ID"`
	CalId  string `path:"calId" doc:"Calendar public ID"`
	MemoId string `path:"memoId" doc:"Memo public ID"`
	Body   struct {
		Title      *string `json:"title,omitempty" minLength:"1" maxLength:"500" required:"false" doc:"Memo title"`
		Done       *bool   `json:"done,omitempty" required:"false" doc:"Whether the memo is done"`
		SortWeight *int32  `json:"sortWeight,omitempty" required:"false" doc:"Sort weight for ordering"`
	}
}

// UpdateMemoOutput is the response for the update memo endpoint.
type UpdateMemoOutput struct {
	Body struct {
		Updated bool `json:"updated"`
	}
}

// DeleteMemoInput is the input for the delete memo endpoint.
type DeleteMemoInput struct {
	WsId   string `path:"wsId" doc:"Workspace public ID"`
	CalId  string `path:"calId" doc:"Calendar public ID"`
	MemoId string `path:"memoId" doc:"Memo public ID"`
}

// DeleteMemoOutput is the response for the delete memo endpoint.
type DeleteMemoOutput struct {
	Body struct {
		Deleted bool `json:"deleted"`
	}
}

// --- Handlers ---

// ListMemos returns all memos in a calendar.
func ListMemos(deps Deps) func(context.Context, *ListMemosInput) (*ListMemosOutput, error) {
	return func(ctx context.Context, input *ListMemosInput) (*ListMemosOutput, error) {
		wsID, actorID, err := resolveWorkspace(ctx, deps.Queries, input.WsId)
		if err != nil {
			return nil, err
		}
		cal, _, err := resolveCalendar(ctx, deps.Queries, wsID, actorID, input.CalId)
		if err != nil {
			return nil, err
		}

		rows, err := deps.Queries.ListCalendarMemos(ctx, cal.ID)
		if err != nil {
			return nil, httpErr(apierrors.CalendarMemoListQueryInterrupted)
		}

		out := &ListMemosOutput{}
		out.Body.Memos = make([]MemoResponse, len(rows))
		for i, r := range rows {
			resp := MemoResponse{
				ID:              r.PublicID.String(),
				Title:           r.Title,
				Done:            r.Done,
				SortWeight:      r.SortWeight,
				UserPublicID:    r.UserPublicID.String(),
				UserDisplayName: r.DisplayName,
				CreatedAt:       r.CreatedAt.Unix(),
			}
			if r.UpdatedAt.Valid {
				resp.UpdatedAt = int64Ptr(r.UpdatedAt.Time.Unix())
			}
			out.Body.Memos[i] = resp
		}
		return out, nil
	}
}

// CreateMemo creates a new memo in a calendar.
func CreateMemo(deps Deps) func(context.Context, *CreateMemoInput) (*CreateMemoOutput, error) {
	return func(ctx context.Context, input *CreateMemoInput) (*CreateMemoOutput, error) {
		wsID, actorID, err := resolveWorkspace(ctx, deps.Queries, input.WsId)
		if err != nil {
			return nil, err
		}
		cal, _, err := resolveCalendar(ctx, deps.Queries, wsID, actorID, input.CalId)
		if err != nil {
			return nil, err
		}

		memoPublicID := types.New()
		_, err = deps.Queries.CreateCalendarMemo(ctx, generated.CreateCalendarMemoParams{
			PublicID:        memoPublicID,
			WorkspaceID:     wsID,
			CalendarID:      cal.ID,
			CreatedByUserID: actorID,
			Title:           input.Body.Title,
			SortWeight:      input.Body.SortWeight,
		})
		if err != nil {
			return nil, httpErr(apierrors.CalendarMemoStoreWriteInterrupted)
		}

		out := &CreateMemoOutput{}
		out.Body = MemoResponse{
			ID:         memoPublicID.String(),
			Title:      input.Body.Title,
			Done:       false,
			SortWeight: input.Body.SortWeight,
			CreatedAt:  time.Now().UTC().Unix(),
		}

		_ = appendCalendarEvent(ctx, deps.DB, wsID, "calendar.memo.created", &actorID, map[string]any{
			"memoId":     memoPublicID.String(),
			"calendarId": input.CalId,
			"title":      input.Body.Title,
		})

		return out, nil
	}
}

// UpdateMemo updates a memo's title, done status, or sort weight.
func UpdateMemo(deps Deps) func(context.Context, *UpdateMemoInput) (*UpdateMemoOutput, error) {
	return func(ctx context.Context, input *UpdateMemoInput) (*UpdateMemoOutput, error) {
		wsID, actorID, err := resolveWorkspace(ctx, deps.Queries, input.WsId)
		if err != nil {
			return nil, err
		}
		cal, _, err := resolveCalendar(ctx, deps.Queries, wsID, actorID, input.CalId)
		if err != nil {
			return nil, err
		}

		memoUID, err := uuid.Parse(input.MemoId)
		if err != nil {
			return nil, httpErr(apierrors.CalendarMemoNotFound)
		}

		_, err = deps.Queries.FindCalendarMemoByPublicId(ctx, generated.FindCalendarMemoByPublicIdParams{
			PublicID:    types.FromUUID(memoUID),
			CalendarID:  cal.ID,
			WorkspaceID: wsID,
		})
		if err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return nil, httpErr(apierrors.CalendarMemoNotFound)
			}
			return nil, httpErr(apierrors.CalendarMemoStoreReadInterrupted)
		}

		params := generated.UpdateCalendarMemoParams{
			PublicID:    types.FromUUID(memoUID),
			CalendarID:  cal.ID,
			WorkspaceID: wsID,
		}
		if input.Body.Title != nil {
			params.Title = sql.NullString{String: *input.Body.Title, Valid: true}
		}
		if input.Body.Done != nil {
			params.Done = sql.NullBool{Bool: *input.Body.Done, Valid: true}
		}
		if input.Body.SortWeight != nil {
			params.SortWeight = sql.NullInt32{Int32: *input.Body.SortWeight, Valid: true}
		}

		err = deps.Queries.UpdateCalendarMemo(ctx, params)
		if err != nil {
			return nil, httpErr(apierrors.CalendarMemoStoreWriteInterrupted)
		}

		out := &UpdateMemoOutput{}
		out.Body.Updated = true

		_ = appendCalendarEvent(ctx, deps.DB, wsID, "calendar.memo.updated", &actorID, map[string]any{
			"memoId":     input.MemoId,
			"calendarId": input.CalId,
		})

		return out, nil
	}
}

// DeleteMemo soft-deletes a memo from a calendar.
func DeleteMemo(deps Deps) func(context.Context, *DeleteMemoInput) (*DeleteMemoOutput, error) {
	return func(ctx context.Context, input *DeleteMemoInput) (*DeleteMemoOutput, error) {
		wsID, actorID, err := resolveWorkspace(ctx, deps.Queries, input.WsId)
		if err != nil {
			return nil, err
		}
		cal, _, err := resolveCalendar(ctx, deps.Queries, wsID, actorID, input.CalId)
		if err != nil {
			return nil, err
		}

		memoUID, err := uuid.Parse(input.MemoId)
		if err != nil {
			return nil, httpErr(apierrors.CalendarMemoNotFound)
		}

		_, err = deps.Queries.FindCalendarMemoByPublicId(ctx, generated.FindCalendarMemoByPublicIdParams{
			PublicID:    types.FromUUID(memoUID),
			CalendarID:  cal.ID,
			WorkspaceID: wsID,
		})
		if err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return nil, httpErr(apierrors.CalendarMemoNotFound)
			}
			return nil, httpErr(apierrors.CalendarMemoStoreReadInterrupted)
		}

		err = deps.Queries.DisableCalendarMemo(ctx, generated.DisableCalendarMemoParams{
			PublicID:    types.FromUUID(memoUID),
			CalendarID:  cal.ID,
			WorkspaceID: wsID,
		})
		if err != nil {
			return nil, httpErr(apierrors.CalendarMemoStoreDeleteInterrupted)
		}

		out := &DeleteMemoOutput{}
		out.Body.Deleted = true

		_ = appendCalendarEvent(ctx, deps.DB, wsID, "calendar.memo.deleted", &actorID, map[string]any{
			"memoId":     input.MemoId,
			"calendarId": input.CalId,
		})

		return out, nil
	}
}
