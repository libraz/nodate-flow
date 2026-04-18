package calendars

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"github.com/google/uuid"

	generated "github.com/nodate-flow/nodate-flow/apps/time-api/internal/db/generated"
	"github.com/nodate-flow/nodate-flow/apps/time-api/internal/db/types"
	"github.com/nodate-flow/nodate-flow/apps/time-api/internal/eventbus"
)

// --- Input/Output types ---

// ListEventsInput is the input for the list events endpoint.
type ListEventsInput struct {
	WsId   string    `path:"wsId" doc:"Workspace public ID"`
	CalId  string    `path:"calId" doc:"Calendar public ID"`
	Start  time.Time `query:"start" doc:"Range start (inclusive)" required:"true"`
	End    time.Time `query:"end" doc:"Range end (exclusive)" required:"true"`
}

// EventResponse is the JSON representation of a calendar event.
type EventResponse struct {
	ID                 string           `json:"id"`
	Kind               string           `json:"kind"`
	Visibility         string           `json:"visibility"`
	ShowAs             string           `json:"showAs"`
	Title              string           `json:"title"`
	AllDay             bool             `json:"allDay"`
	StartAt            time.Time        `json:"startAt"`
	EndAt              time.Time        `json:"endAt"`
	Timezone           string           `json:"timezone"`
	Location           *string          `json:"location,omitempty"`
	Memo               *string          `json:"memo,omitempty"`
	Url                *string          `json:"url,omitempty"`
	BlockLabel         *string          `json:"blockLabel,omitempty"`
	RecurrenceRule       *json.RawMessage `json:"recurrenceRule,omitempty"`
	RecurrenceEnd        *time.Time       `json:"recurrenceEnd,omitempty"`
	RecurrenceExceptions *json.RawMessage `json:"recurrenceExceptions,omitempty"`
	NotificationOffset   *int32           `json:"notificationOffset,omitempty"`
	UpdatedAt            *time.Time       `json:"updatedAt,omitempty"`
	CreatedAt            time.Time        `json:"createdAt"`
}

// ListEventsOutput is the response for the list events endpoint.
type ListEventsOutput struct {
	Body struct {
		Events []EventResponse `json:"events"`
	}
}

// CreateEventInput is the input for the create event endpoint.
type CreateEventInput struct {
	WsId  string `path:"wsId" doc:"Workspace public ID"`
	CalId string `path:"calId" doc:"Calendar public ID"`
	Body  struct {
		Kind               string           `json:"kind" enum:"event,block,free" doc:"Event kind"`
		Visibility         string           `json:"visibility,omitempty" required:"false" enum:"default,public,private,confidential" doc:"Visibility"`
		ShowAs             string           `json:"showAs,omitempty" required:"false" enum:"busy,free,tentative,oof" doc:"Show-as status"`
		Title              string           `json:"title" minLength:"1" maxLength:"500" doc:"Event title"`
		AllDay             bool             `json:"allDay" required:"false" doc:"All-day event flag"`
		StartAt            time.Time        `json:"startAt" doc:"Start time"`
		EndAt              time.Time        `json:"endAt" doc:"End time"`
		Timezone           string           `json:"timezone" doc:"IANA timezone"`
		Location           *string          `json:"location,omitempty" required:"false" doc:"Location"`
		Memo               *string          `json:"memo,omitempty" required:"false" doc:"Memo / notes"`
		Url                *string          `json:"url,omitempty" required:"false" doc:"Related URL"`
		OwnerUserID        *string          `json:"ownerUserId,omitempty" required:"false" doc:"Owner user public ID (defaults to actor)"`
		BlockLabel         *string          `json:"blockLabel,omitempty" required:"false" doc:"Block label"`
		RecurrenceRule     *json.RawMessage `json:"recurrenceRule,omitempty" required:"false" doc:"RFC 5545 recurrence rule as JSON"`
		RecurrenceEnd      *time.Time       `json:"recurrenceEnd,omitempty" required:"false" doc:"Recurrence end date"`
		NotificationOffset *int32           `json:"notificationOffset,omitempty" required:"false" doc:"Notification offset in minutes"`
	}
}

// CreateEventOutput is the response for the create event endpoint.
type CreateEventOutput struct {
	Body EventResponse
}

// GetEventInput is the input for the get event endpoint.
type GetEventInput struct {
	WsId  string `path:"wsId" doc:"Workspace public ID"`
	CalId string `path:"calId" doc:"Calendar public ID"`
	EvtId string `path:"evtId" doc:"Event public ID"`
}

// GetEventOutput is the response for the get event endpoint.
type GetEventOutput struct {
	Body EventResponse
}

// PatchEventInput is the input for the patch event endpoint.
type PatchEventInput struct {
	WsId  string `path:"wsId" doc:"Workspace public ID"`
	CalId string `path:"calId" doc:"Calendar public ID"`
	EvtId string `path:"evtId" doc:"Event public ID"`
	Body  struct {
		Kind               *string          `json:"kind,omitempty" required:"false" doc:"Event kind"`
		Visibility         *string          `json:"visibility,omitempty" required:"false" doc:"Visibility"`
		ShowAs             *string          `json:"showAs,omitempty" required:"false" doc:"Show-as status"`
		Title              *string          `json:"title,omitempty" required:"false" doc:"Event title"`
		AllDay             *bool            `json:"allDay,omitempty" required:"false" doc:"All-day flag"`
		StartAt            *time.Time       `json:"startAt,omitempty" required:"false" doc:"Start time"`
		EndAt              *time.Time       `json:"endAt,omitempty" required:"false" doc:"End time"`
		Timezone           *string          `json:"timezone,omitempty" required:"false" doc:"IANA timezone"`
		Location           *string          `json:"location,omitempty" required:"false" doc:"Location"`
		Memo               *string          `json:"memo,omitempty" required:"false" doc:"Memo"`
		Url                *string          `json:"url,omitempty" required:"false" doc:"Related URL"`
		BlockLabel         *string          `json:"blockLabel,omitempty" required:"false" doc:"Block label"`
		RecurrenceRule       *json.RawMessage `json:"recurrenceRule,omitempty" required:"false" doc:"Recurrence rule"`
		RecurrenceEnd        *time.Time       `json:"recurrenceEnd,omitempty" required:"false" doc:"Recurrence end"`
		RecurrenceExceptions *json.RawMessage `json:"recurrenceExceptions,omitempty" required:"false" doc:"Array of ISO 8601 dates/times to exclude from recurrence"`
		NotificationOffset   *int32           `json:"notificationOffset,omitempty" required:"false" doc:"Notification offset"`
	}
}

// PatchEventOutput is the response for the patch event endpoint.
type PatchEventOutput struct {
	Body EventResponse
}

// DeleteEventInput is the input for the delete event endpoint.
type DeleteEventInput struct {
	WsId  string `path:"wsId" doc:"Workspace public ID"`
	CalId string `path:"calId" doc:"Calendar public ID"`
	EvtId string `path:"evtId" doc:"Event public ID"`
}

// DeleteEventOutput is the response for the delete event endpoint.
type DeleteEventOutput struct {
	Body struct {
		Deleted bool `json:"deleted"`
	}
}

// ListCalendarEventsInput is the input for the cross-calendar event list endpoint.
type ListCalendarEventsInput struct {
	WsId  string `path:"wsId" doc:"Workspace public ID"`
	Start string `query:"start" doc:"Range start (inclusive, date or datetime)" required:"true"`
	End   string `query:"end" doc:"Range end (exclusive, date or datetime)" required:"true"`
}

// CrossCalendarEventResponse is the JSON representation of a cross-calendar event.
type CrossCalendarEventResponse struct {
	ID             string           `json:"id"`
	CalendarID     string           `json:"calendarId"`
	Kind           string           `json:"kind"`
	Visibility     string           `json:"visibility"`
	ShowAs         string           `json:"showAs"`
	Title          string           `json:"title"`
	AllDay         bool             `json:"allDay"`
	StartAt        time.Time        `json:"startAt"`
	EndAt          time.Time        `json:"endAt"`
	Timezone       string           `json:"timezone"`
	Location       *string          `json:"location,omitempty"`
	BlockLabel     *string          `json:"blockLabel,omitempty"`
	RecurrenceRule       *json.RawMessage `json:"recurrenceRule,omitempty"`
	RecurrenceEnd        *time.Time       `json:"recurrenceEnd,omitempty"`
	RecurrenceExceptions *json.RawMessage `json:"recurrenceExceptions,omitempty"`
	UpdatedAt            *time.Time       `json:"updatedAt,omitempty"`
	CreatedAt            time.Time        `json:"createdAt"`
}

// ListCalendarEventsOutput is the response for the cross-calendar event list endpoint.
type ListCalendarEventsOutput struct {
	Body struct {
		Events []CrossCalendarEventResponse `json:"events"`
	}
}

// --- Handlers ---

// ListEvents returns events in a calendar within a time range, including
// both non-recurring and recurring events.
func ListEvents(deps Deps) func(context.Context, *ListEventsInput) (*ListEventsOutput, error) {
	return func(ctx context.Context, input *ListEventsInput) (*ListEventsOutput, error) {
		wsID, actorID, err := resolveWorkspace(ctx, deps.Queries, input.WsId)
		if err != nil {
			return nil, err
		}
		cal, _, err := resolveCalendar(ctx, deps.Queries, wsID, actorID, input.CalId)
		if err != nil {
			return nil, err
		}

		// Non-recurring events: start_at < end, end_at > start (overlap check).
		nonRecurring, err := deps.Queries.ListCalendarEventsByRange(ctx, generated.ListCalendarEventsByRangeParams{
			CalendarID: cal.ID,
			StartAt:    input.End,
			EndAt:      input.Start,
		})
		if err != nil {
			return nil, huma.Error500InternalServerError("Failed to list events", err)
		}

		// Recurring events whose recurrence window overlaps the query range.
		recurring, err := deps.Queries.ListRecurringCalendarEventsByRange(ctx, generated.ListRecurringCalendarEventsByRangeParams{
			CalendarID:    cal.ID,
			StartAt:       input.End,
			RecurrenceEnd: sql.NullTime{Time: input.Start, Valid: true},
		})
		if err != nil {
			return nil, huma.Error500InternalServerError("Failed to list recurring events", err)
		}

		out := &ListEventsOutput{}
		events := make([]EventResponse, 0, len(nonRecurring)+len(recurring))
		for _, e := range nonRecurring {
			events = append(events, eventFromRangeRow(e))
		}
		for _, e := range recurring {
			events = append(events, eventFromRecurringRow(e))
		}
		out.Body.Events = events
		return out, nil
	}
}

// CreateEvent creates a new event in a calendar.
func CreateEvent(deps Deps) func(context.Context, *CreateEventInput) (*CreateEventOutput, error) {
	return func(ctx context.Context, input *CreateEventInput) (*CreateEventOutput, error) {
		wsID, actorID, err := resolveWorkspace(ctx, deps.Queries, input.WsId)
		if err != nil {
			return nil, err
		}
		cal, sub, err := resolveCalendar(ctx, deps.Queries, wsID, actorID, input.CalId)
		if err != nil {
			return nil, err
		}

		ownerUserID := actorID
		if input.Body.OwnerUserID != nil {
			ownerUID, parseErr := uuid.Parse(*input.Body.OwnerUserID)
			if parseErr != nil {
				return nil, huma.Error400BadRequest("Invalid ownerUserId")
			}
			// Resolve the owner user's internal ID.
			var ownerInternal uint32
			row := deps.DB.QueryRowContext(ctx,
				`SELECT id FROM users WHERE public_id = ? AND enabled = TRUE LIMIT 1`,
				types.FromUUID(ownerUID))
			if scanErr := row.Scan(&ownerInternal); scanErr != nil {
				if errors.Is(scanErr, sql.ErrNoRows) {
					return nil, huma.Error400BadRequest("Owner user not found")
				}
				return nil, huma.Error500InternalServerError("Failed to resolve owner user", scanErr)
			}
			if !canSetOwner(actorID, ownerInternal, sub) {
				return nil, errForbidden
			}
			ownerUserID = ownerInternal
		}

		eventPublicID := types.New()

		visibility := generated.CalendarEventsVisibilityDefault
		if input.Body.Visibility != "" {
			visibility = generated.CalendarEventsVisibility(input.Body.Visibility)
		}
		showAs := generated.CalendarEventsShowAsBusy
		if input.Body.ShowAs != "" {
			showAs = generated.CalendarEventsShowAs(input.Body.ShowAs)
		}

		params := generated.CreateCalendarEventParams{
			PublicID:        eventPublicID,
			WorkspaceID:     wsID,
			CalendarID:      cal.ID,
			Kind:            generated.CalendarEventsKind(input.Body.Kind),
			Visibility:      visibility,
			ShowAs:          showAs,
			Title:           input.Body.Title,
			AllDay:          input.Body.AllDay,
			StartAt:         input.Body.StartAt,
			EndAt:           input.Body.EndAt,
			Timezone:        input.Body.Timezone,
			OwnerUserID:     ownerUserID,
			CreatedByUserID: actorID,
		}
		if input.Body.Location != nil {
			params.Location = sql.NullString{String: *input.Body.Location, Valid: true}
		}
		if input.Body.Memo != nil {
			params.Memo = sql.NullString{String: *input.Body.Memo, Valid: true}
		}
		if input.Body.Url != nil {
			params.Url = sql.NullString{String: *input.Body.Url, Valid: true}
		}
		if input.Body.BlockLabel != nil {
			params.BlockLabel = sql.NullString{String: *input.Body.BlockLabel, Valid: true}
		}
		if input.Body.RecurrenceRule != nil {
			params.RecurrenceRule = json.RawMessage(*input.Body.RecurrenceRule)
		}
		if input.Body.RecurrenceEnd != nil {
			params.RecurrenceEnd = sql.NullTime{Time: *input.Body.RecurrenceEnd, Valid: true}
		}
		if input.Body.NotificationOffset != nil {
			params.NotificationOffset = sql.NullInt32{Int32: *input.Body.NotificationOffset, Valid: true}
		}

		_, err = deps.Queries.CreateCalendarEvent(ctx, params)
		if err != nil {
			return nil, huma.Error500InternalServerError("Failed to create event", err)
		}

		out := &CreateEventOutput{}
		out.Body = EventResponse{
			ID:         eventPublicID.String(),
			Kind:       input.Body.Kind,
			Visibility: string(visibility),
			ShowAs:     string(showAs),
			Title:      input.Body.Title,
			AllDay:     input.Body.AllDay,
			StartAt:    input.Body.StartAt,
			EndAt:      input.Body.EndAt,
			Timezone:   input.Body.Timezone,
			Location:   input.Body.Location,
			Memo:       input.Body.Memo,
			Url:        input.Body.Url,
			BlockLabel: input.Body.BlockLabel,
			CreatedAt:  time.Now().UTC(),
		}
		if input.Body.RecurrenceRule != nil {
			out.Body.RecurrenceRule = input.Body.RecurrenceRule
		}
		if input.Body.RecurrenceEnd != nil {
			out.Body.RecurrenceEnd = input.Body.RecurrenceEnd
		}
		if input.Body.NotificationOffset != nil {
			out.Body.NotificationOffset = input.Body.NotificationOffset
		}

		_ = eventbus.Append(ctx, deps.DB, wsID, "calendar.event.created", &actorID, map[string]any{
			"eventId":    eventPublicID.String(),
			"calendarId": input.CalId,
			"title":      input.Body.Title,
			"startAt":    input.Body.StartAt,
			"endAt":      input.Body.EndAt,
			"kind":       input.Body.Kind,
		})

		return out, nil
	}
}

// GetEvent returns a single event by its public ID.
func GetEvent(deps Deps) func(context.Context, *GetEventInput) (*GetEventOutput, error) {
	return func(ctx context.Context, input *GetEventInput) (*GetEventOutput, error) {
		wsID, actorID, err := resolveWorkspace(ctx, deps.Queries, input.WsId)
		if err != nil {
			return nil, err
		}
		cal, sub, err := resolveCalendar(ctx, deps.Queries, wsID, actorID, input.CalId)
		if err != nil {
			return nil, err
		}

		evtUID, err := uuid.Parse(input.EvtId)
		if err != nil {
			return nil, errEventNotFound
		}
		evt, err := deps.Queries.FindCalendarEventByPublicId(ctx, generated.FindCalendarEventByPublicIdParams{
			PublicID:   types.FromUUID(evtUID),
			CalendarID: cal.ID,
		})
		if err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return nil, errEventNotFound
			}
			return nil, huma.Error500InternalServerError("Failed to get event", err)
		}

		// Visibility filtering: private events show only title/times to non-owners.
		resp := eventFromFullRow(evt)
		if evt.Visibility == generated.CalendarEventsVisibilityPrivate &&
			evt.OwnerUserID != actorID &&
			!isOwnerOrManager(sub) {
			resp.Memo = nil
			resp.Location = nil
			resp.Url = nil
		}

		out := &GetEventOutput{}
		out.Body = resp
		return out, nil
	}
}

// PatchEvent updates mutable event fields. Requires edit permission.
func PatchEvent(deps Deps) func(context.Context, *PatchEventInput) (*PatchEventOutput, error) {
	return func(ctx context.Context, input *PatchEventInput) (*PatchEventOutput, error) {
		wsID, actorID, err := resolveWorkspace(ctx, deps.Queries, input.WsId)
		if err != nil {
			return nil, err
		}
		cal, sub, err := resolveCalendar(ctx, deps.Queries, wsID, actorID, input.CalId)
		if err != nil {
			return nil, err
		}

		evtUID, err := uuid.Parse(input.EvtId)
		if err != nil {
			return nil, errEventNotFound
		}
		evt, err := deps.Queries.FindCalendarEventByPublicId(ctx, generated.FindCalendarEventByPublicIdParams{
			PublicID:   types.FromUUID(evtUID),
			CalendarID: cal.ID,
		})
		if err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return nil, errEventNotFound
			}
			return nil, huma.Error500InternalServerError("Failed to get event", err)
		}

		// Check edit permission (try to find attendee record for the actor).
		var attendee *generated.FindCalendarEventAttendeeRow
		att, attErr := deps.Queries.FindCalendarEventAttendee(ctx, generated.FindCalendarEventAttendeeParams{
			EventID: evt.ID,
			UserID:  actorID,
		})
		if attErr == nil {
			attendee = &att
		}
		if !canEditEvent(actorID, evt, sub, attendee) {
			return nil, errForbidden
		}

		params := generated.PatchCalendarEventParams{
			PublicID:   types.FromUUID(evtUID),
			CalendarID: cal.ID,
		}
		if input.Body.Kind != nil {
			params.Kind = generated.NullCalendarEventsKind{
				CalendarEventsKind: generated.CalendarEventsKind(*input.Body.Kind),
				Valid:              true,
			}
		}
		if input.Body.Visibility != nil {
			params.Visibility = generated.NullCalendarEventsVisibility{
				CalendarEventsVisibility: generated.CalendarEventsVisibility(*input.Body.Visibility),
				Valid:                    true,
			}
		}
		if input.Body.ShowAs != nil {
			params.ShowAs = generated.NullCalendarEventsShowAs{
				CalendarEventsShowAs: generated.CalendarEventsShowAs(*input.Body.ShowAs),
				Valid:                true,
			}
		}
		if input.Body.Title != nil {
			params.Title = sql.NullString{String: *input.Body.Title, Valid: true}
		}
		if input.Body.AllDay != nil {
			params.AllDay = sql.NullBool{Bool: *input.Body.AllDay, Valid: true}
		}
		if input.Body.StartAt != nil {
			params.StartAt = sql.NullTime{Time: *input.Body.StartAt, Valid: true}
		}
		if input.Body.EndAt != nil {
			params.EndAt = sql.NullTime{Time: *input.Body.EndAt, Valid: true}
		}
		if input.Body.Timezone != nil {
			params.Timezone = sql.NullString{String: *input.Body.Timezone, Valid: true}
		}
		if input.Body.Location != nil {
			params.Location = sql.NullString{String: *input.Body.Location, Valid: true}
		}
		if input.Body.Memo != nil {
			params.Memo = sql.NullString{String: *input.Body.Memo, Valid: true}
		}
		if input.Body.Url != nil {
			params.Url = sql.NullString{String: *input.Body.Url, Valid: true}
		}
		if input.Body.BlockLabel != nil {
			params.BlockLabel = sql.NullString{String: *input.Body.BlockLabel, Valid: true}
		}
		if input.Body.RecurrenceRule != nil {
			params.RecurrenceRule = json.RawMessage(*input.Body.RecurrenceRule)
		}
		if input.Body.RecurrenceEnd != nil {
			params.RecurrenceEnd = sql.NullTime{Time: *input.Body.RecurrenceEnd, Valid: true}
		}
		if input.Body.RecurrenceExceptions != nil {
			params.RecurrenceExceptions = json.RawMessage(*input.Body.RecurrenceExceptions)
		}
		if input.Body.NotificationOffset != nil {
			params.NotificationOffset = sql.NullInt32{Int32: *input.Body.NotificationOffset, Valid: true}
		}

		err = deps.Queries.PatchCalendarEvent(ctx, params)
		if err != nil {
			return nil, huma.Error500InternalServerError("Failed to update event", err)
		}

		// Re-read.
		evt, err = deps.Queries.FindCalendarEventByPublicId(ctx, generated.FindCalendarEventByPublicIdParams{
			PublicID:   types.FromUUID(evtUID),
			CalendarID: cal.ID,
		})
		if err != nil {
			return nil, huma.Error500InternalServerError("Failed to read updated event", err)
		}

		out := &PatchEventOutput{}
		out.Body = eventFromFullRow(evt)

		_ = eventbus.Append(ctx, deps.DB, wsID, "calendar.event.updated", &actorID, map[string]any{
			"eventId":    input.EvtId,
			"calendarId": input.CalId,
		})

		return out, nil
	}
}

// DeleteEvent soft-deletes a calendar event. Requires edit permission.
func DeleteEvent(deps Deps) func(context.Context, *DeleteEventInput) (*DeleteEventOutput, error) {
	return func(ctx context.Context, input *DeleteEventInput) (*DeleteEventOutput, error) {
		wsID, actorID, err := resolveWorkspace(ctx, deps.Queries, input.WsId)
		if err != nil {
			return nil, err
		}
		cal, sub, err := resolveCalendar(ctx, deps.Queries, wsID, actorID, input.CalId)
		if err != nil {
			return nil, err
		}

		evtUID, err := uuid.Parse(input.EvtId)
		if err != nil {
			return nil, errEventNotFound
		}
		evt, err := deps.Queries.FindCalendarEventByPublicId(ctx, generated.FindCalendarEventByPublicIdParams{
			PublicID:   types.FromUUID(evtUID),
			CalendarID: cal.ID,
		})
		if err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return nil, errEventNotFound
			}
			return nil, huma.Error500InternalServerError("Failed to get event", err)
		}

		var attendee *generated.FindCalendarEventAttendeeRow
		att, attErr := deps.Queries.FindCalendarEventAttendee(ctx, generated.FindCalendarEventAttendeeParams{
			EventID: evt.ID,
			UserID:  actorID,
		})
		if attErr == nil {
			attendee = &att
		}
		if !canEditEvent(actorID, evt, sub, attendee) {
			return nil, errForbidden
		}

		err = deps.Queries.DisableCalendarEvent(ctx, generated.DisableCalendarEventParams{
			PublicID:   types.FromUUID(evtUID),
			CalendarID: cal.ID,
		})
		if err != nil {
			return nil, huma.Error500InternalServerError("Failed to delete event", err)
		}

		out := &DeleteEventOutput{}
		out.Body.Deleted = true

		_ = eventbus.Append(ctx, deps.DB, wsID, "calendar.event.deleted", &actorID, map[string]any{
			"eventId":    input.EvtId,
			"calendarId": input.CalId,
		})

		return out, nil
	}
}

// ListCalendarEvents returns events across all calendars the user subscribes to
// in a workspace within a time range.
func ListCalendarEvents(deps Deps) func(context.Context, *ListCalendarEventsInput) (*ListCalendarEventsOutput, error) {
	return func(ctx context.Context, input *ListCalendarEventsInput) (*ListCalendarEventsOutput, error) {
		wsID, actorID, err := resolveWorkspace(ctx, deps.Queries, input.WsId)
		if err != nil {
			return nil, err
		}

		startTime, err := parseFlexibleTime(input.Start)
		if err != nil {
			return nil, huma.Error422UnprocessableEntity("invalid start date/time", err)
		}
		endTime, err := parseFlexibleTime(input.End)
		if err != nil {
			return nil, huma.Error422UnprocessableEntity("invalid end date/time", err)
		}

		// Non-recurring events
		rows, err := deps.Queries.ListCalendarEventsAcrossCalendars(ctx, generated.ListCalendarEventsAcrossCalendarsParams{
			UserID:      actorID,
			WorkspaceID: wsID,
			StartAt:     endTime,
			EndAt:       startTime,
		})
		if err != nil {
			return nil, huma.Error500InternalServerError("Failed to list calendar events", err)
		}

		// Recurring events whose recurrence window overlaps the query range
		recurringRows, err := deps.Queries.ListRecurringCalendarEventsAcrossCalendars(ctx, generated.ListRecurringCalendarEventsAcrossCalendarsParams{
			UserID:        actorID,
			WorkspaceID:   wsID,
			StartAt:       endTime,
			RecurrenceEnd: sql.NullTime{Time: startTime, Valid: true},
		})
		if err != nil {
			return nil, huma.Error500InternalServerError("Failed to list recurring calendar events", err)
		}

		out := &ListCalendarEventsOutput{}
		out.Body.Events = make([]CrossCalendarEventResponse, 0, len(rows)+len(recurringRows))

		for _, r := range rows {
			resp := CrossCalendarEventResponse{
				ID:         r.PublicID.String(),
				CalendarID: r.CalendarPublicID.String(),
				Kind:       string(r.Kind),
				Visibility: string(r.Visibility),
				ShowAs:     string(r.ShowAs),
				Title:      r.Title,
				AllDay:     r.AllDay,
				StartAt:    r.StartAt,
				EndAt:      r.EndAt,
				Timezone:   r.Timezone,
				CreatedAt:  r.CreatedAt,
			}
			if r.Location.Valid {
				resp.Location = &r.Location.String
			}
			if r.BlockLabel.Valid {
				resp.BlockLabel = &r.BlockLabel.String
			}
			if r.UpdatedAt.Valid {
				resp.UpdatedAt = &r.UpdatedAt.Time
			}
			out.Body.Events = append(out.Body.Events, resp)
		}

		for _, r := range recurringRows {
			resp := CrossCalendarEventResponse{
				ID:         r.PublicID.String(),
				CalendarID: r.CalendarPublicID.String(),
				Kind:       string(r.Kind),
				Visibility: string(r.Visibility),
				ShowAs:     string(r.ShowAs),
				Title:      r.Title,
				AllDay:     r.AllDay,
				StartAt:    r.StartAt,
				EndAt:      r.EndAt,
				Timezone:   r.Timezone,
				CreatedAt:  r.CreatedAt,
			}
			if r.Location.Valid {
				resp.Location = &r.Location.String
			}
			if r.BlockLabel.Valid {
				resp.BlockLabel = &r.BlockLabel.String
			}
			if r.RecurrenceRule != nil {
				raw := json.RawMessage(r.RecurrenceRule)
				resp.RecurrenceRule = &raw
			}
			if r.RecurrenceEnd.Valid {
				resp.RecurrenceEnd = &r.RecurrenceEnd.Time
			}
			if r.RecurrenceExceptions != nil {
				raw := json.RawMessage(r.RecurrenceExceptions)
				resp.RecurrenceExceptions = &raw
			}
			if r.UpdatedAt.Valid {
				resp.UpdatedAt = &r.UpdatedAt.Time
			}
			out.Body.Events = append(out.Body.Events, resp)
		}

		return out, nil
	}
}

// --- Mapping helpers ---

func eventFromRangeRow(e generated.ListCalendarEventsByRangeRow) EventResponse {
	resp := EventResponse{
		ID:         e.PublicID.String(),
		Kind:       string(e.Kind),
		Visibility: string(e.Visibility),
		ShowAs:     string(e.ShowAs),
		Title:      e.Title,
		AllDay:     e.AllDay,
		StartAt:    e.StartAt,
		EndAt:      e.EndAt,
		Timezone:   e.Timezone,
		CreatedAt:  e.CreatedAt,
	}
	if e.Location.Valid {
		resp.Location = &e.Location.String
	}
	if e.Memo.Valid {
		resp.Memo = &e.Memo.String
	}
	if e.Url.Valid {
		resp.Url = &e.Url.String
	}
	if e.BlockLabel.Valid {
		resp.BlockLabel = &e.BlockLabel.String
	}
	if e.NotificationOffset.Valid {
		resp.NotificationOffset = &e.NotificationOffset.Int32
	}
	if e.UpdatedAt.Valid {
		resp.UpdatedAt = &e.UpdatedAt.Time
	}
	return resp
}

func eventFromRecurringRow(e generated.ListRecurringCalendarEventsByRangeRow) EventResponse {
	resp := EventResponse{
		ID:         e.PublicID.String(),
		Kind:       string(e.Kind),
		Visibility: string(e.Visibility),
		ShowAs:     string(e.ShowAs),
		Title:      e.Title,
		AllDay:     e.AllDay,
		StartAt:    e.StartAt,
		EndAt:      e.EndAt,
		Timezone:   e.Timezone,
		CreatedAt:  e.CreatedAt,
	}
	if e.Location.Valid {
		resp.Location = &e.Location.String
	}
	if e.Memo.Valid {
		resp.Memo = &e.Memo.String
	}
	if e.Url.Valid {
		resp.Url = &e.Url.String
	}
	if e.BlockLabel.Valid {
		resp.BlockLabel = &e.BlockLabel.String
	}
	if e.RecurrenceRule != nil {
		raw := json.RawMessage(e.RecurrenceRule)
		resp.RecurrenceRule = &raw
	}
	if e.RecurrenceEnd.Valid {
		resp.RecurrenceEnd = &e.RecurrenceEnd.Time
	}
	if e.RecurrenceExceptions != nil {
		raw := json.RawMessage(e.RecurrenceExceptions)
		resp.RecurrenceExceptions = &raw
	}
	if e.NotificationOffset.Valid {
		resp.NotificationOffset = &e.NotificationOffset.Int32
	}
	if e.UpdatedAt.Valid {
		resp.UpdatedAt = &e.UpdatedAt.Time
	}
	return resp
}

// parseFlexibleTime parses a date string ("2006-01-02") or a full
// RFC 3339 datetime string into a time.Time.
func parseFlexibleTime(s string) (time.Time, error) {
	if t, err := time.Parse(time.RFC3339, s); err == nil {
		return t, nil
	}
	if t, err := time.Parse("2006-01-02", s); err == nil {
		return t, nil
	}
	return time.Time{}, fmt.Errorf("cannot parse %q as date or datetime", s)
}

func eventFromFullRow(e generated.FindCalendarEventByPublicIdRow) EventResponse {
	resp := EventResponse{
		ID:         e.PublicID.String(),
		Kind:       string(e.Kind),
		Visibility: string(e.Visibility),
		ShowAs:     string(e.ShowAs),
		Title:      e.Title,
		AllDay:     e.AllDay,
		StartAt:    e.StartAt,
		EndAt:      e.EndAt,
		Timezone:   e.Timezone,
		CreatedAt:  e.CreatedAt,
	}
	if e.Location.Valid {
		resp.Location = &e.Location.String
	}
	if e.Memo.Valid {
		resp.Memo = &e.Memo.String
	}
	if e.Url.Valid {
		resp.Url = &e.Url.String
	}
	if e.BlockLabel.Valid {
		resp.BlockLabel = &e.BlockLabel.String
	}
	if e.RecurrenceRule != nil {
		raw := json.RawMessage(e.RecurrenceRule)
		resp.RecurrenceRule = &raw
	}
	if e.RecurrenceEnd.Valid {
		resp.RecurrenceEnd = &e.RecurrenceEnd.Time
	}
	if e.RecurrenceExceptions != nil {
		raw := json.RawMessage(e.RecurrenceExceptions)
		resp.RecurrenceExceptions = &raw
	}
	if e.NotificationOffset.Valid {
		resp.NotificationOffset = &e.NotificationOffset.Int32
	}
	if e.UpdatedAt.Valid {
		resp.UpdatedAt = &e.UpdatedAt.Time
	}
	return resp
}
