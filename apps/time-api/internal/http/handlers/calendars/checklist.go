package calendars

import (
	"context"
	"database/sql"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"github.com/google/uuid"

	generated "github.com/nodate-flow/nodate-flow/apps/time-api/internal/db/generated"
	"github.com/nodate-flow/nodate-flow/apps/time-api/internal/db/types"
	"github.com/nodate-flow/nodate-flow/apps/time-api/internal/eventbus"
)

// --- Input/Output types ---

// ListChecklistInput is the input for listing checklist items.
type ListChecklistInput struct {
	WsId  string `path:"wsId" doc:"Workspace public ID"`
	CalId string `path:"calId" doc:"Calendar public ID"`
	EvtId string `path:"evtId" doc:"Event public ID"`
}

// ChecklistItemResponse is the JSON representation of a checklist item.
type ChecklistItemResponse struct {
	ID         string    `json:"id"`
	Title      string    `json:"title"`
	Done       bool      `json:"done"`
	SortWeight int32     `json:"sortWeight"`
	CreatedAt  time.Time `json:"createdAt"`
}

// ListChecklistOutput is the response for the list checklist endpoint.
type ListChecklistOutput struct {
	Body struct {
		Items []ChecklistItemResponse `json:"items"`
	}
}

// CreateChecklistItemInput is the input for creating a checklist item.
type CreateChecklistItemInput struct {
	WsId  string `path:"wsId" doc:"Workspace public ID"`
	CalId string `path:"calId" doc:"Calendar public ID"`
	EvtId string `path:"evtId" doc:"Event public ID"`
	Body  struct {
		Title      string `json:"title" minLength:"1" maxLength:"500" doc:"Item title"`
		SortWeight int32  `json:"sortWeight" required:"false" doc:"Sort weight for ordering"`
	}
}

// CreateChecklistItemOutput is the response for the create checklist item endpoint.
type CreateChecklistItemOutput struct {
	Body ChecklistItemResponse
}

// UpdateChecklistItemInput is the input for updating a checklist item.
type UpdateChecklistItemInput struct {
	WsId   string `path:"wsId" doc:"Workspace public ID"`
	CalId  string `path:"calId" doc:"Calendar public ID"`
	EvtId  string `path:"evtId" doc:"Event public ID"`
	ItemId string `path:"itemId" doc:"Checklist item public ID"`
	Body   struct {
		Title      *string `json:"title,omitempty" required:"false" doc:"Item title"`
		Done       *bool   `json:"done,omitempty" required:"false" doc:"Done flag"`
		SortWeight *int32  `json:"sortWeight,omitempty" required:"false" doc:"Sort weight"`
	}
}

// UpdateChecklistItemOutput is the response for the update checklist item endpoint.
type UpdateChecklistItemOutput struct {
	Body struct {
		Updated bool `json:"updated"`
	}
}

// DeleteChecklistItemInput is the input for deleting a checklist item.
type DeleteChecklistItemInput struct {
	WsId   string `path:"wsId" doc:"Workspace public ID"`
	CalId  string `path:"calId" doc:"Calendar public ID"`
	EvtId  string `path:"evtId" doc:"Event public ID"`
	ItemId string `path:"itemId" doc:"Checklist item public ID"`
}

// DeleteChecklistItemOutput is the response for the delete checklist item endpoint.
type DeleteChecklistItemOutput struct {
	Body struct {
		Deleted bool `json:"deleted"`
	}
}

// --- Handlers ---

// ListChecklist returns all checklist items for an event.
func ListChecklist(deps Deps) func(context.Context, *ListChecklistInput) (*ListChecklistOutput, error) {
	return func(ctx context.Context, input *ListChecklistInput) (*ListChecklistOutput, error) {
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

		rows, err := deps.Queries.ListCalendarChecklistItems(ctx, evt.ID)
		if err != nil {
			return nil, huma.Error500InternalServerError("Failed to list checklist items", err)
		}

		out := &ListChecklistOutput{}
		out.Body.Items = make([]ChecklistItemResponse, len(rows))
		for i, r := range rows {
			out.Body.Items[i] = ChecklistItemResponse{
				ID:         r.PublicID.String(),
				Title:      r.Title,
				Done:       r.Done,
				SortWeight: r.SortWeight,
				CreatedAt:  r.CreatedAt,
			}
		}
		return out, nil
	}
}

// CreateChecklistItem adds a checklist item to an event.
func CreateChecklistItem(deps Deps) func(context.Context, *CreateChecklistItemInput) (*CreateChecklistItemOutput, error) {
	return func(ctx context.Context, input *CreateChecklistItemInput) (*CreateChecklistItemOutput, error) {
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

		itemPublicID := types.New()
		_, err = deps.Queries.CreateCalendarChecklistItem(ctx, generated.CreateCalendarChecklistItemParams{
			PublicID:        itemPublicID,
			WorkspaceID:     wsID,
			EventID:         evt.ID,
			CreatedByUserID: actorID,
			Title:           input.Body.Title,
			SortWeight:      input.Body.SortWeight,
		})
		if err != nil {
			return nil, huma.Error500InternalServerError("Failed to create checklist item", err)
		}

		out := &CreateChecklistItemOutput{}
		out.Body = ChecklistItemResponse{
			ID:         itemPublicID.String(),
			Title:      input.Body.Title,
			Done:       false,
			SortWeight: input.Body.SortWeight,
			CreatedAt:  time.Now().UTC(),
		}

		_ = eventbus.Append(ctx, deps.DB, wsID, "calendar.event.checklist.created", &actorID, map[string]any{
			"eventId":    input.EvtId,
			"calendarId": input.CalId,
			"itemId":     itemPublicID.String(),
		})

		return out, nil
	}
}

// UpdateChecklistItem updates a checklist item's title, done status, or sort weight.
func UpdateChecklistItem(deps Deps) func(context.Context, *UpdateChecklistItemInput) (*UpdateChecklistItemOutput, error) {
	return func(ctx context.Context, input *UpdateChecklistItemInput) (*UpdateChecklistItemOutput, error) {
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

		itemUID, err := uuid.Parse(input.ItemId)
		if err != nil {
			return nil, huma.Error404NotFound("Checklist item not found")
		}

		params := generated.UpdateCalendarChecklistItemParams{
			PublicID: types.FromUUID(itemUID),
			EventID:  evt.ID,
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

		err = deps.Queries.UpdateCalendarChecklistItem(ctx, params)
		if err != nil {
			return nil, huma.Error500InternalServerError("Failed to update checklist item", err)
		}

		_ = eventbus.Append(ctx, deps.DB, wsID, "calendar.event.checklist.updated", &actorID, map[string]any{
			"eventId":    input.EvtId,
			"calendarId": input.CalId,
			"itemId":     input.ItemId,
		})

		out := &UpdateChecklistItemOutput{}
		out.Body.Updated = true
		return out, nil
	}
}

// DeleteChecklistItem soft-deletes a checklist item.
func DeleteChecklistItem(deps Deps) func(context.Context, *DeleteChecklistItemInput) (*DeleteChecklistItemOutput, error) {
	return func(ctx context.Context, input *DeleteChecklistItemInput) (*DeleteChecklistItemOutput, error) {
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

		itemUID, err := uuid.Parse(input.ItemId)
		if err != nil {
			return nil, huma.Error404NotFound("Checklist item not found")
		}

		err = deps.Queries.DisableCalendarChecklistItem(ctx, generated.DisableCalendarChecklistItemParams{
			PublicID: types.FromUUID(itemUID),
			EventID:  evt.ID,
		})
		if err != nil {
			return nil, huma.Error500InternalServerError("Failed to delete checklist item", err)
		}

		out := &DeleteChecklistItemOutput{}
		out.Body.Deleted = true

		_ = eventbus.Append(ctx, deps.DB, wsID, "calendar.event.checklist.deleted", &actorID, map[string]any{
			"eventId":    input.EvtId,
			"calendarId": input.CalId,
			"itemId":     input.ItemId,
		})

		return out, nil
	}
}
