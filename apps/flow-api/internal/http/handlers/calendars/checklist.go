package calendars

import (
	"context"
	"database/sql"

	"github.com/google/uuid"

	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/dbretry"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/generated/calendar"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/types"
	apierrors "github.com/libraz/nodate-flow/apps/flow-api/internal/errors"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/eventbus"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/http/handlers/handlerutil"
)

// --- Input/Output types ---

// ListChecklistInput is the input for listing checklist items.
type ListChecklistInput struct {
	WsID   string `path:"wsId" doc:"Workspace public ID"`
	CalID  string `path:"calId" doc:"Calendar public ID"`
	EvtID  string `path:"evtId" doc:"Event public ID"`
	Limit  int32  `query:"limit" minimum:"1" maximum:"200" default:"50"`
	Offset int32  `query:"offset" minimum:"0" default:"0"`
}

// ChecklistItemResponse is the JSON representation of a checklist item.
type ChecklistItemResponse struct {
	ID         string `json:"id"`
	Title      string `json:"title"`
	Done       bool   `json:"done"`
	SortWeight int32  `json:"sortWeight"`
	CreatedAt  int64  `json:"createdAt"`
}

// ListChecklistOutput is the response for the list checklist endpoint.
type ListChecklistOutput struct {
	Body struct {
		Total int64                   `json:"total" doc:"Total checklist items before paging"`
		Items []ChecklistItemResponse `json:"items"`
	}
}

// CreateChecklistItemInput is the input for creating a checklist item.
type CreateChecklistItemInput struct {
	WsID  string `path:"wsId" doc:"Workspace public ID"`
	CalID string `path:"calId" doc:"Calendar public ID"`
	EvtID string `path:"evtId" doc:"Event public ID"`
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
	WsID   string `path:"wsId" doc:"Workspace public ID"`
	CalID  string `path:"calId" doc:"Calendar public ID"`
	EvtID  string `path:"evtId" doc:"Event public ID"`
	ItemID string `path:"itemId" doc:"Checklist item public ID"`
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
	WsID   string `path:"wsId" doc:"Workspace public ID"`
	CalID  string `path:"calId" doc:"Calendar public ID"`
	EvtID  string `path:"evtId" doc:"Event public ID"`
	ItemID string `path:"itemId" doc:"Checklist item public ID"`
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

		page := handlerutil.Bind(input.Limit, input.Offset, handlerutil.DefaultListLimit, handlerutil.MaxListLimit)
		rows, err := deps.CalendarQueries.ListCalendarChecklistItems(ctx, calendar.ListCalendarChecklistItemsParams{
			EventID:     evt.ID,
			WorkspaceID: wsID,
			Limit:       page.Limit,
			Offset:      page.Offset,
		})
		if err != nil {
			return nil, httpErr(apierrors.CalendarChecklistListQueryInterrupted)
		}

		out := &ListChecklistOutput{}
		out.Body.Items = make([]ChecklistItemResponse, len(rows))
		for i, r := range rows {
			out.Body.Items[i] = ChecklistItemResponse{
				ID:         r.PublicID.String(),
				Title:      r.Title,
				Done:       r.Done,
				SortWeight: r.SortWeight,
				CreatedAt:  r.CreatedAt.Unix(),
			}
		}
		if len(rows) > 0 {
			out.Body.Total = handlerutil.TotalAsInt64(rows[0].Total)
		}
		return out, nil
	}
}

// CreateChecklistItem adds a checklist item to an event.
func CreateChecklistItem(deps Deps) func(context.Context, *CreateChecklistItemInput) (*CreateChecklistItemOutput, error) {
	return func(ctx context.Context, input *CreateChecklistItemInput) (*CreateChecklistItemOutput, error) {
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

		itemPublicID := types.New()
		_, err = deps.CalendarQueries.CreateCalendarChecklistItem(ctx, calendar.CreateCalendarChecklistItemParams{
			PublicID:        itemPublicID,
			WorkspaceID:     wsID,
			EventID:         evt.ID,
			CreatedByUserID: actorID,
			Title:           input.Body.Title,
			SortWeight:      input.Body.SortWeight,
		})
		if err != nil {
			return nil, httpErr(apierrors.CalendarChecklistStoreWriteInterrupted)
		}

		out := &CreateChecklistItemOutput{}
		out.Body = ChecklistItemResponse{
			ID:         itemPublicID.String(),
			Title:      input.Body.Title,
			Done:       false,
			SortWeight: input.Body.SortWeight,
			CreatedAt:  handlerutil.NowUnix(),
		}

		appendCalendarEvent(ctx, dbretry.AutoCommit(deps.DB), wsID, cal.ID, eventbus.CalEventChecklistCreated, &actorID, map[string]any{
			"eventId":    input.EvtID,
			"calendarId": input.CalID,
			"itemId":     itemPublicID.String(),
		}, "calendars.CreateChecklistItem")

		return out, nil
	}
}

// UpdateChecklistItem updates a checklist item's title, done status, or sort weight.
func UpdateChecklistItem(deps Deps) func(context.Context, *UpdateChecklistItemInput) (*UpdateChecklistItemOutput, error) {
	return func(ctx context.Context, input *UpdateChecklistItemInput) (*UpdateChecklistItemOutput, error) {
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

		itemUID, err := uuid.Parse(input.ItemID)
		if err != nil {
			return nil, httpErr(apierrors.CalendarChecklistItemNotFound)
		}

		params := calendar.UpdateCalendarChecklistItemParams{
			PublicID:    types.FromUUID(itemUID),
			EventID:     evt.ID,
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

		// Not an existence check: MySQL counts changed rows, so a PATCH
		// that re-sends the item's current title / done / order reports
		// zero. The owning event was resolved above.
		_, err = deps.CalendarQueries.UpdateCalendarChecklistItem(ctx, params)
		if err != nil {
			return nil, httpErr(apierrors.CalendarChecklistStoreWriteInterrupted)
		}

		appendCalendarEvent(ctx, dbretry.AutoCommit(deps.DB), wsID, cal.ID, eventbus.CalEventChecklistUpdated, &actorID, map[string]any{
			"eventId":    input.EvtID,
			"calendarId": input.CalID,
			"itemId":     input.ItemID,
		}, "calendars.UpdateChecklistItem")

		out := &UpdateChecklistItemOutput{}
		out.Body.Updated = true
		return out, nil
	}
}

// DeleteChecklistItem soft-deletes a checklist item.
func DeleteChecklistItem(deps Deps) func(context.Context, *DeleteChecklistItemInput) (*DeleteChecklistItemOutput, error) {
	return func(ctx context.Context, input *DeleteChecklistItemInput) (*DeleteChecklistItemOutput, error) {
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

		itemUID, err := uuid.Parse(input.ItemID)
		if err != nil {
			return nil, httpErr(apierrors.CalendarChecklistItemNotFound)
		}

		// Nothing resolves the item before this, so the count is the only
		// thing that knows whether it was there: a well-formed id for an
		// item on some other event -- or none at all -- used to answer
		// deleted: true.
		rows, err := deps.CalendarQueries.DisableCalendarChecklistItem(ctx, calendar.DisableCalendarChecklistItemParams{
			PublicID:    types.FromUUID(itemUID),
			EventID:     evt.ID,
			WorkspaceID: wsID,
		})
		if err != nil {
			return nil, httpErr(apierrors.CalendarChecklistStoreDeleteInterrupted)
		}
		if rows == 0 {
			return nil, httpErr(apierrors.CalendarChecklistItemNotFound)
		}

		out := &DeleteChecklistItemOutput{}
		out.Body.Deleted = true

		appendCalendarEvent(ctx, dbretry.AutoCommit(deps.DB), wsID, cal.ID, eventbus.CalEventChecklistDeleted, &actorID, map[string]any{
			"eventId":    input.EvtID,
			"calendarId": input.CalID,
			"itemId":     input.ItemID,
		}, "calendars.DeleteChecklistItem")

		return out, nil
	}
}
