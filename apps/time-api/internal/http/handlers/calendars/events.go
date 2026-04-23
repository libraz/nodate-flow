package calendars

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/google/uuid"

	generated "github.com/nodate-flow/nodate-flow/apps/time-api/internal/db/generated"
	"github.com/nodate-flow/nodate-flow/apps/time-api/internal/db/types"
	apierrors "github.com/nodate-flow/nodate-flow/apps/time-api/internal/errors"
	"github.com/nodate-flow/nodate-flow/apps/time-api/internal/eventbus"
	"github.com/nodate-flow/nodate-flow/packages/go-shared/itemkit"
)

// --- Input/Output types ---

// ListEventsInput is the input for the list events endpoint.
type ListEventsInput struct {
	WsId  string    `path:"wsId" doc:"Workspace public ID"`
	CalId string    `path:"calId" doc:"Calendar public ID"`
	Start time.Time `query:"start" doc:"Range start (inclusive)" required:"true"`
	End   time.Time `query:"end" doc:"Range end (exclusive)" required:"true"`
}

// EventResponse is the JSON representation of a calendar event.
// StartAt / EndAt are nullable to support "planning stage" events
// that may be dateless until scheduled (see calendar_events.sql).
type EventResponse struct {
	ID                   string           `json:"id"`
	Kind                 string           `json:"kind"`
	Visibility           string           `json:"visibility"`
	ShowAs               string           `json:"showAs"`
	Title                string           `json:"title"`
	AllDay               bool             `json:"allDay"`
	StartAt              *int64           `json:"startAt,omitempty"`
	EndAt                *int64           `json:"endAt,omitempty"`
	Timezone             string           `json:"timezone"`
	Location             *string          `json:"location,omitempty"`
	Memo                 *string          `json:"memo,omitempty"`
	Url                  *string          `json:"url,omitempty"`
	BlockLabel           *string          `json:"blockLabel,omitempty"`
	RecurrenceRule       *json.RawMessage `json:"recurrenceRule,omitempty"`
	RecurrenceEnd        *int64           `json:"recurrenceEnd,omitempty"`
	RecurrenceExceptions *json.RawMessage `json:"recurrenceExceptions,omitempty"`
	NotificationOffset   *int32           `json:"notificationOffset,omitempty"`
	UpdatedAt            *int64           `json:"updatedAt,omitempty"`
	CreatedAt            int64            `json:"createdAt"`
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
		Kind               string           `json:"kind" enum:"event,block,free,milestone" doc:"Event kind"`
		Visibility         string           `json:"visibility,omitempty" required:"false" enum:"default,public,private,confidential" doc:"Visibility"`
		ShowAs             string           `json:"showAs,omitempty" required:"false" enum:"busy,free,tentative,oof" doc:"Show-as status"`
		Title              string           `json:"title" minLength:"1" maxLength:"500" doc:"Event title"`
		AllDay             bool             `json:"allDay" required:"false" doc:"All-day event flag"`
		StartAt            *int64           `json:"startAt,omitempty" required:"false" doc:"Start time as unix seconds (UTC); omit for a planning-stage (undated) event"`
		EndAt              *int64           `json:"endAt,omitempty" required:"false" doc:"End time as unix seconds (UTC); omit for a planning-stage (undated) event"`
		Timezone           string           `json:"timezone" doc:"IANA timezone"`
		Location           *string          `json:"location,omitempty" required:"false" doc:"Location"`
		Memo               *string          `json:"memo,omitempty" required:"false" doc:"Memo / notes"`
		Url                *string          `json:"url,omitempty" required:"false" doc:"Related URL"`
		OwnerUserID        *string          `json:"ownerUserId,omitempty" required:"false" doc:"Owner user public ID (defaults to actor)"`
		BlockLabel         *string          `json:"blockLabel,omitempty" required:"false" doc:"Block label"`
		RecurrenceRule     *json.RawMessage `json:"recurrenceRule,omitempty" required:"false" doc:"RFC 5545 recurrence rule as JSON"`
		RecurrenceEnd      *int64           `json:"recurrenceEnd,omitempty" required:"false" doc:"Recurrence end as unix seconds (UTC)"`
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
		Kind                 *string          `json:"kind,omitempty" required:"false" doc:"Event kind"`
		Visibility           *string          `json:"visibility,omitempty" required:"false" doc:"Visibility"`
		ShowAs               *string          `json:"showAs,omitempty" required:"false" doc:"Show-as status"`
		Title                *string          `json:"title,omitempty" required:"false" doc:"Event title"`
		AllDay               *bool            `json:"allDay,omitempty" required:"false" doc:"All-day flag"`
		StartAt              *int64           `json:"startAt,omitempty" required:"false" doc:"Start time as unix seconds (UTC)"`
		EndAt                *int64           `json:"endAt,omitempty" required:"false" doc:"End time as unix seconds (UTC)"`
		Timezone             *string          `json:"timezone,omitempty" required:"false" doc:"IANA timezone"`
		Location             *string          `json:"location,omitempty" required:"false" doc:"Location"`
		Memo                 *string          `json:"memo,omitempty" required:"false" doc:"Memo"`
		Url                  *string          `json:"url,omitempty" required:"false" doc:"Related URL"`
		BlockLabel           *string          `json:"blockLabel,omitempty" required:"false" doc:"Block label"`
		RecurrenceRule       *json.RawMessage `json:"recurrenceRule,omitempty" required:"false" doc:"Recurrence rule"`
		RecurrenceEnd        *int64           `json:"recurrenceEnd,omitempty" required:"false" doc:"Recurrence end as unix seconds (UTC)"`
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
// StartAt / EndAt are nullable (see EventResponse).
type CrossCalendarEventResponse struct {
	ID                   string           `json:"id"`
	CalendarID           string           `json:"calendarId"`
	Kind                 string           `json:"kind"`
	Visibility           string           `json:"visibility"`
	ShowAs               string           `json:"showAs"`
	Title                string           `json:"title"`
	AllDay               bool             `json:"allDay"`
	StartAt              *int64           `json:"startAt,omitempty"`
	EndAt                *int64           `json:"endAt,omitempty"`
	Timezone             string           `json:"timezone"`
	Location             *string          `json:"location,omitempty"`
	BlockLabel           *string          `json:"blockLabel,omitempty"`
	RecurrenceRule       *json.RawMessage `json:"recurrenceRule,omitempty"`
	RecurrenceEnd        *int64           `json:"recurrenceEnd,omitempty"`
	RecurrenceExceptions *json.RawMessage `json:"recurrenceExceptions,omitempty"`
	UpdatedAt            *int64           `json:"updatedAt,omitempty"`
	CreatedAt            int64            `json:"createdAt"`
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
			StartAt:    sql.NullTime{Time: input.End, Valid: true},
			EndAt:      sql.NullTime{Time: input.Start, Valid: true},
		})
		if err != nil {
			return nil, httpErr(apierrors.CalendarEventListQueryInterrupted)
		}

		// Recurring events whose recurrence window overlaps the query range.
		recurring, err := deps.Queries.ListRecurringCalendarEventsByRange(ctx, generated.ListRecurringCalendarEventsByRangeParams{
			CalendarID:    cal.ID,
			StartAt:       sql.NullTime{Time: input.End, Valid: true},
			RecurrenceEnd: sql.NullTime{Time: input.Start, Valid: true},
		})
		if err != nil {
			return nil, httpErr(apierrors.CalendarEventListQueryInterrupted)
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
				return nil, httpErr(apierrors.CalendarEventOwnerUserIdMalformed)
			}
			// Resolve the owner user's internal ID.
			var ownerInternal uint32
			row := deps.DB.QueryRowContext(ctx,
				`SELECT id FROM users WHERE public_id = ? AND enabled = TRUE LIMIT 1`,
				types.FromUUID(ownerUID))
			if scanErr := row.Scan(&ownerInternal); scanErr != nil {
				if errors.Is(scanErr, sql.ErrNoRows) {
					return nil, httpErr(apierrors.CalendarEventOwnerUserNotFound)
				}
				return nil, httpErr(apierrors.CalendarEventOwnerUserResolveInterrupted)
			}
			if !canSetOwner(actorID, ownerInternal, sub) {
				return nil, httpErr(apierrors.CalendarEventEditPermissionRequired)
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

		// Planning-stage undated events: both start and end may be omitted
		// (persisted as NULL). Requesting start without end (or vice versa)
		// is rejected to keep the pair invariant enforced by
		// chk_calendar_events_start_end_pair.
		if (input.Body.StartAt == nil) != (input.Body.EndAt == nil) {
			return nil, httpErr(apierrors.CalendarEventStartEndPairRequired)
		}
		var startAtNT, endAtNT sql.NullTime
		if input.Body.StartAt != nil {
			startAtNT = sql.NullTime{Time: time.Unix(*input.Body.StartAt, 0).UTC(), Valid: true}
			endAtNT = sql.NullTime{Time: time.Unix(*input.Body.EndAt, 0).UTC(), Valid: true}
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
			StartAt:         startAtNT,
			EndAt:           endAtNT,
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
			params.RecurrenceEnd = sql.NullTime{Time: time.Unix(*input.Body.RecurrenceEnd, 0).UTC(), Valid: true}
		}
		if input.Body.NotificationOffset != nil {
			params.NotificationOffset = sql.NullInt32{Int32: *input.Body.NotificationOffset, Valid: true}
		}

		_, err = deps.Queries.CreateCalendarEvent(ctx, params)
		if err != nil {
			return nil, httpErr(apierrors.CalendarEventStoreWriteInterrupted)
		}

		out := &CreateEventOutput{}
		out.Body = EventResponse{
			ID:         eventPublicID.String(),
			Kind:       input.Body.Kind,
			Visibility: string(visibility),
			ShowAs:     string(showAs),
			Title:      input.Body.Title,
			AllDay:     input.Body.AllDay,
			StartAt:    nullTimeUnixPtr(startAtNT),
			EndAt:      nullTimeUnixPtr(endAtNT),
			Timezone:   input.Body.Timezone,
			Location:   input.Body.Location,
			Memo:       input.Body.Memo,
			Url:        input.Body.Url,
			BlockLabel: input.Body.BlockLabel,
			CreatedAt:  time.Now().UTC().Unix(),
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

		eventBusPayload := map[string]any{
			"eventId":    eventPublicID.String(),
			"calendarId": input.CalId,
			"title":      input.Body.Title,
			"kind":       input.Body.Kind,
		}
		if startAtNT.Valid {
			eventBusPayload["startAt"] = startAtNT.Time
			eventBusPayload["endAt"] = endAtNT.Time
		}
		_ = eventbus.Append(ctx, deps.DB, wsID, "calendar.event.created", &actorID, eventBusPayload)

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
		cal, _, err := resolveCalendar(ctx, deps.Queries, wsID, actorID, input.CalId)
		if err != nil {
			return nil, err
		}

		evtUID, err := uuid.Parse(input.EvtId)
		if err != nil {
			return nil, errEventNotFound
		}
		evt, err := deps.Queries.FindCalendarEventByPublicId(ctx, generated.FindCalendarEventByPublicIdParams{
			PublicID:    types.FromUUID(evtUID),
			CalendarID:  cal.ID,
			WorkspaceID: wsID,
		})
		if err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return nil, errEventNotFound
			}
			return nil, httpErr(apierrors.CalendarEventStoreReadInterrupted)
		}

		// Visibility filtering: private events scrub memo/location/url for
		// ws members other than the owner. Event-level visibility is the
		// real ACL; ws membership is the edit gate.
		resp := eventFromFullRow(evt)
		if evt.Visibility == generated.CalendarEventsVisibilityPrivate && evt.OwnerUserID != actorID {
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
// When the event is task-linked (task_id set), title changes are routed
// through itemkit.RenameItem and start/end changes through
// itemkit.RescheduleEvent so the linked task stays in lockstep. Other
// fields (location, memo, visibility, etc.) are applied via the plain
// sqlc PATCH inside the same transaction.
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
			PublicID:    types.FromUUID(evtUID),
			CalendarID:  cal.ID,
			WorkspaceID: wsID,
		})
		if err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return nil, errEventNotFound
			}
			return nil, httpErr(apierrors.CalendarEventStoreReadInterrupted)
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
			return nil, httpErr(apierrors.CalendarEventEditPermissionRequired)
		}

		// Pair invariant: explicit partial start_at without end_at (or
		// vice versa) is ambiguous. Undated transitions are supported,
		// but they must come through unlink first.
		if (input.Body.StartAt == nil) != (input.Body.EndAt == nil) {
			return nil, httpErr(apierrors.CalendarEventStartEndPairRequired)
		}

		tx, err := deps.DB.BeginTx(ctx, nil)
		if err != nil {
			return nil, httpErr(apierrors.CalendarEventStoreWriteInterrupted)
		}
		defer func() { _ = tx.Rollback() }()
		qtx := deps.Queries.WithTx(tx)

		isLinked := evt.TaskID.Valid
		titleChanged := input.Body.Title != nil && *input.Body.Title != evt.Title
		timeChanged := input.Body.StartAt != nil // pair invariant guarantees EndAt also set

		// Linked title change → itemkit.RenameItem (propagates to task + siblings)
		if isLinked && titleChanged {
			if err := itemkit.RenameItem(ctx, tx, itemkit.RenameItemArgs{
				WorkspaceID: wsID,
				ActorUserID: actorID,
				NewTitle:    *input.Body.Title,
				EventID:     evt.ID,
			}); err != nil {
				return nil, translateItemkitError(ctx, "itemkit.RenameItem", err)
			}
			// Prevent the sqlc PATCH below from redundantly touching title.
			input.Body.Title = nil
		}
		// Linked time change → itemkit.RescheduleEvent (propagates to task date)
		if isLinked && timeChanged {
			if err := itemkit.RescheduleEvent(ctx, tx, itemkit.RescheduleEventArgs{
				WorkspaceID: wsID,
				EventID:     evt.ID,
				ActorUserID: actorID,
				StartAt:     time.Unix(*input.Body.StartAt, 0).UTC(),
				EndAt:       time.Unix(*input.Body.EndAt, 0).UTC(),
			}); err != nil {
				return nil, translateItemkitError(ctx, "itemkit.RescheduleEvent", err)
			}
			input.Body.StartAt = nil
			input.Body.EndAt = nil
		}

		params := generated.PatchCalendarEventParams{
			PublicID:    types.FromUUID(evtUID),
			CalendarID:  cal.ID,
			WorkspaceID: wsID,
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
			params.StartAt = sql.NullTime{Time: time.Unix(*input.Body.StartAt, 0).UTC(), Valid: true}
		}
		if input.Body.EndAt != nil {
			params.EndAt = sql.NullTime{Time: time.Unix(*input.Body.EndAt, 0).UTC(), Valid: true}
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
			// A task-linked event may not become recurring — invariant
			// enforced in itemkit. Flag here so the tx rolls back
			// deterministically.
			if isLinked {
				return nil, httpErr(apierrors.ItemItemkitRecurrenceWithTaskLink)
			}
			params.RecurrenceRule = json.RawMessage(*input.Body.RecurrenceRule)
		}
		if input.Body.RecurrenceEnd != nil {
			params.RecurrenceEnd = sql.NullTime{Time: time.Unix(*input.Body.RecurrenceEnd, 0).UTC(), Valid: true}
		}
		if input.Body.RecurrenceExceptions != nil {
			params.RecurrenceExceptions = json.RawMessage(*input.Body.RecurrenceExceptions)
		}
		if input.Body.NotificationOffset != nil {
			params.NotificationOffset = sql.NullInt32{Int32: *input.Body.NotificationOffset, Valid: true}
		}

		if err := qtx.PatchCalendarEvent(ctx, params); err != nil {
			return nil, httpErr(apierrors.CalendarEventStoreWriteInterrupted)
		}

		// Re-read inside the tx so the response reflects what itemkit
		// + sqlc jointly wrote.
		evt, err = qtx.FindCalendarEventByPublicId(ctx, generated.FindCalendarEventByPublicIdParams{
			PublicID:    types.FromUUID(evtUID),
			CalendarID:  cal.ID,
			WorkspaceID: wsID,
		})
		if err != nil {
			return nil, httpErr(apierrors.CalendarEventStoreReadInterrupted)
		}

		if err := tx.Commit(); err != nil {
			return nil, httpErr(apierrors.CalendarEventStoreWriteInterrupted)
		}

		out := &PatchEventOutput{}
		out.Body = eventFromFullRow(evt)

		// When itemkit fired item.rescheduled / item.renamed it already
		// dual-emitted the legacy calendar.event.updated kind, so no
		// extra append is needed for linked events. For unlinked events
		// we preserve the existing kind to keep webhook subscribers
		// working.
		if !isLinked {
			_ = eventbus.Append(ctx, deps.DB, wsID, "calendar.event.updated", &actorID, map[string]any{
				"eventId":    input.EvtId,
				"calendarId": input.CalId,
			})
		}

		return out, nil
	}
}

// translateItemkitError maps itemkit invariant / generic errors to
// time-api's apierrors spec set. The original error is logged at
// ErrorContext level so schema drift or other low-level failures do
// not disappear into a generic 500. Known sentinels (sql.ErrNoRows)
// are surfaced as 404. Invariant / recurrence messages are mapped to
// their dedicated 4xx codes. Anything else falls through to the
// generic store-write / store-delete 500 decided by the caller via
// fallback.
func translateItemkitError(ctx context.Context, op string, err error) error {
	if err == nil {
		return nil
	}
	slog.ErrorContext(ctx, "itemkit error", "op", op, "error", err.Error())

	if errors.Is(err, sql.ErrNoRows) {
		return httpErr(apierrors.CalendarEventNotFound)
	}

	msg := err.Error()
	switch {
	case strings.Contains(msg, "not found"):
		return httpErr(apierrors.CalendarEventNotFound)
	case strings.Contains(msg, "recurrence"):
		return httpErr(apierrors.ItemItemkitRecurrenceWithTaskLink)
	case strings.Contains(msg, "itemkit invariant"):
		return httpErr(apierrors.ItemItemkitInvariantViolation)
	}

	// Pick the fallback status based on the op so DELETE reports a
	// delete-failure and PATCH/POST reports a write-failure.
	if strings.Contains(op, "DeleteEvent") {
		return httpErr(apierrors.CalendarEventStoreDeleteInterrupted)
	}
	return httpErr(apierrors.CalendarEventStoreWriteInterrupted)
}

// DeleteEvent soft-deletes a calendar event. Requires edit permission.
// Delegates to itemkit.DeleteEvent which clears the corresponding
// tasks.due_on column when the event was task-linked
// (task_role = 'due'), leaving the task itself intact.
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
			PublicID:    types.FromUUID(evtUID),
			CalendarID:  cal.ID,
			WorkspaceID: wsID,
		})
		if err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return nil, errEventNotFound
			}
			return nil, httpErr(apierrors.CalendarEventStoreReadInterrupted)
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
			return nil, httpErr(apierrors.CalendarEventEditPermissionRequired)
		}

		tx, err := deps.DB.BeginTx(ctx, nil)
		if err != nil {
			return nil, httpErr(apierrors.CalendarEventStoreDeleteInterrupted)
		}
		defer func() { _ = tx.Rollback() }()

		if err := itemkit.DeleteEvent(ctx, tx, wsID, evt.ID, actorID); err != nil {
			return nil, translateItemkitError(ctx, "itemkit.DeleteEvent", fmt.Errorf("itemkit: delete event: %w", err))
		}
		if err := tx.Commit(); err != nil {
			return nil, httpErr(apierrors.CalendarEventStoreDeleteInterrupted)
		}

		out := &DeleteEventOutput{}
		out.Body.Deleted = true

		// itemkit already emitted item.unscheduled + legacy
		// calendar.event.deleted. No extra append.

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
			return nil, httpErr(apierrors.CalendarEventDateRangeUnparseable)
		}
		endTime, err := parseFlexibleTime(input.End)
		if err != nil {
			return nil, httpErr(apierrors.CalendarEventDateRangeUnparseable)
		}

		// Non-recurring events
		rows, err := deps.Queries.ListCalendarEventsAcrossCalendars(ctx, generated.ListCalendarEventsAcrossCalendarsParams{
			UserID:      actorID,
			WorkspaceID: wsID,
			StartAt:     sql.NullTime{Time: endTime, Valid: true},
			EndAt:       sql.NullTime{Time: startTime, Valid: true},
		})
		if err != nil {
			return nil, httpErr(apierrors.CalendarEventListQueryInterrupted)
		}

		// Recurring events whose recurrence window overlaps the query range
		recurringRows, err := deps.Queries.ListRecurringCalendarEventsAcrossCalendars(ctx, generated.ListRecurringCalendarEventsAcrossCalendarsParams{
			UserID:        actorID,
			WorkspaceID:   wsID,
			StartAt:       sql.NullTime{Time: endTime, Valid: true},
			RecurrenceEnd: sql.NullTime{Time: startTime, Valid: true},
		})
		if err != nil {
			return nil, httpErr(apierrors.CalendarEventListQueryInterrupted)
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
				StartAt:    nullTimeUnixPtr(r.StartAt),
				EndAt:      nullTimeUnixPtr(r.EndAt),
				Timezone:   r.Timezone,
				CreatedAt:  r.CreatedAt.Unix(),
			}
			if r.Location.Valid {
				resp.Location = &r.Location.String
			}
			if r.BlockLabel.Valid {
				resp.BlockLabel = &r.BlockLabel.String
			}
			if r.UpdatedAt.Valid {
				resp.UpdatedAt = int64Ptr(r.UpdatedAt.Time.Unix())
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
				StartAt:    nullTimeUnixPtr(r.StartAt),
				EndAt:      nullTimeUnixPtr(r.EndAt),
				Timezone:   r.Timezone,
				CreatedAt:  r.CreatedAt.Unix(),
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
				resp.RecurrenceEnd = int64Ptr(r.RecurrenceEnd.Time.Unix())
			}
			if r.RecurrenceExceptions != nil {
				raw := json.RawMessage(r.RecurrenceExceptions)
				resp.RecurrenceExceptions = &raw
			}
			if r.UpdatedAt.Valid {
				resp.UpdatedAt = int64Ptr(r.UpdatedAt.Time.Unix())
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
		StartAt:    nullTimeUnixPtr(e.StartAt),
		EndAt:      nullTimeUnixPtr(e.EndAt),
		Timezone:   e.Timezone,
		CreatedAt:  e.CreatedAt.Unix(),
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
		resp.UpdatedAt = int64Ptr(e.UpdatedAt.Time.Unix())
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
		StartAt:    nullTimeUnixPtr(e.StartAt),
		EndAt:      nullTimeUnixPtr(e.EndAt),
		Timezone:   e.Timezone,
		CreatedAt:  e.CreatedAt.Unix(),
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
		resp.RecurrenceEnd = int64Ptr(e.RecurrenceEnd.Time.Unix())
	}
	if e.RecurrenceExceptions != nil {
		raw := json.RawMessage(e.RecurrenceExceptions)
		resp.RecurrenceExceptions = &raw
	}
	if e.NotificationOffset.Valid {
		resp.NotificationOffset = &e.NotificationOffset.Int32
	}
	if e.UpdatedAt.Valid {
		resp.UpdatedAt = int64Ptr(e.UpdatedAt.Time.Unix())
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
		StartAt:    nullTimeUnixPtr(e.StartAt),
		EndAt:      nullTimeUnixPtr(e.EndAt),
		Timezone:   e.Timezone,
		CreatedAt:  e.CreatedAt.Unix(),
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
		resp.RecurrenceEnd = int64Ptr(e.RecurrenceEnd.Time.Unix())
	}
	if e.RecurrenceExceptions != nil {
		raw := json.RawMessage(e.RecurrenceExceptions)
		resp.RecurrenceExceptions = &raw
	}
	if e.NotificationOffset.Valid {
		resp.NotificationOffset = &e.NotificationOffset.Int32
	}
	if e.UpdatedAt.Valid {
		resp.UpdatedAt = int64Ptr(e.UpdatedAt.Time.Unix())
	}
	return resp
}
