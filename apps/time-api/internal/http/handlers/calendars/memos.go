package calendars

import (
	"context"
	"time"

	"github.com/danielgtaylor/huma/v2"

	generated "github.com/nodate-flow/nodate-flow/apps/time-api/internal/db/generated"
	"github.com/nodate-flow/nodate-flow/apps/time-api/internal/db/types"
	"github.com/nodate-flow/nodate-flow/apps/time-api/internal/eventbus"
)

// --- Input/Output types ---

// ListMemosInput is the input for the list memos endpoint.
type ListMemosInput struct {
	WsId  string `path:"wsId" doc:"Workspace public ID"`
	CalId string `path:"calId" doc:"Calendar public ID"`
}

// MemoResponse is the JSON representation of a calendar memo.
type MemoResponse struct {
	ID              string     `json:"id"`
	Title           string     `json:"title"`
	Done            bool       `json:"done"`
	SortWeight      int32      `json:"sortWeight"`
	UserPublicID    string     `json:"userPublicId"`
	UserDisplayName string     `json:"userDisplayName"`
	UpdatedAt       *time.Time `json:"updatedAt,omitempty"`
	CreatedAt       time.Time  `json:"createdAt"`
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
			return nil, huma.Error500InternalServerError("Failed to list memos", err)
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
				CreatedAt:       r.CreatedAt,
			}
			if r.UpdatedAt.Valid {
				resp.UpdatedAt = &r.UpdatedAt.Time
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
			return nil, huma.Error500InternalServerError("Failed to create memo", err)
		}

		out := &CreateMemoOutput{}
		out.Body = MemoResponse{
			ID:         memoPublicID.String(),
			Title:      input.Body.Title,
			Done:       false,
			SortWeight: input.Body.SortWeight,
			CreatedAt:  time.Now().UTC(),
		}

		_ = eventbus.Append(ctx, deps.DB, wsID, "calendar.memo.created", &actorID, map[string]any{
			"memoId":     memoPublicID.String(),
			"calendarId": input.CalId,
			"title":      input.Body.Title,
		})

		return out, nil
	}
}
