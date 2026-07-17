package calendars

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/nodate-flow/nodate-flow/apps/flow-api/internal/audit"
	"github.com/nodate-flow/nodate-flow/apps/flow-api/internal/db/generated/calendar"
	"github.com/nodate-flow/nodate-flow/apps/flow-api/internal/db/types"
	apierrors "github.com/nodate-flow/nodate-flow/apps/flow-api/internal/errors"
	"github.com/nodate-flow/nodate-flow/apps/flow-api/internal/http/handlers/handlerutil"
	"github.com/nodate-flow/nodate-flow/apps/flow-api/internal/itemkit"
	"github.com/nodate-flow/nodate-flow/packages/go-shared/dbtype"
)

// --- Input/Output types ---

// ListEventsInput is the input for the list events endpoint.
//
// Start and End accept either RFC 3339 datetime strings or YYYY-MM-DD
// dates. Plain dates are interpreted as UTC midnight, matching
// ListCalendarEventsInput so callers can use a single date format for
// both per-calendar and cross-calendar list endpoints.
type ListEventsInput struct {
	WsID  string `path:"wsId" doc:"Workspace public ID"`
	CalID string `path:"calId" doc:"Calendar public ID"`
	Start string `query:"start" doc:"Range start (inclusive, RFC 3339 datetime or YYYY-MM-DD)" required:"true" minLength:"1"`
	End   string `query:"end" doc:"Range end (exclusive, RFC 3339 datetime or YYYY-MM-DD)" required:"true" minLength:"1"`
}

// EventResponse is the JSON representation of a calendar event.
// StartAt / EndAt are nullable to support "planning stage" events
// that may be dateless until scheduled (see calendar_events.sql).
//
// CreatorID / CreatorDisplayName / CreatorAvatarURL surface the event's
// actual creator (calendar_events.created_by_user_id), which may differ
// from the owner under manager delegation. Only the creator's public_id,
// display name, and (nullable) avatar URL are exposed — never the
// internal user id or email (Critical Pattern #18). The shape mirrors the
// author summary on TaskComment so flow clients can render a consistent
// user reference.
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
	URL                  *string          `json:"url,omitempty"`
	CreatorID            string           `json:"creatorId,omitempty"`
	CreatorDisplayName   string           `json:"creatorDisplayName,omitempty"`
	CreatorAvatarURL     *string          `json:"creatorAvatarUrl,omitempty"`
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
	WsID  string `path:"wsId" doc:"Workspace public ID"`
	CalID string `path:"calId" doc:"Calendar public ID"`
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
		URL                *string          `json:"url,omitempty" required:"false" doc:"Related URL"`
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
	WsID  string `path:"wsId" doc:"Workspace public ID"`
	CalID string `path:"calId" doc:"Calendar public ID"`
	EvtID string `path:"evtId" doc:"Event public ID"`
}

// GetEventOutput is the response for the get event endpoint.
type GetEventOutput struct {
	Body EventResponse
}

// PatchEventInput is the input for the patch event endpoint.
type PatchEventInput struct {
	WsID  string `path:"wsId" doc:"Workspace public ID"`
	CalID string `path:"calId" doc:"Calendar public ID"`
	EvtID string `path:"evtId" doc:"Event public ID"`
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
		URL                  *string          `json:"url,omitempty" required:"false" doc:"Related URL"`
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
	WsID  string `path:"wsId" doc:"Workspace public ID"`
	CalID string `path:"calId" doc:"Calendar public ID"`
	EvtID string `path:"evtId" doc:"Event public ID"`
}

// DeleteEventOutput is the response for the delete event endpoint.
type DeleteEventOutput struct {
	Body struct {
		Deleted bool `json:"deleted"`
	}
}

// ListCalendarEventsInput is the input for the cross-calendar event list endpoint.
type ListCalendarEventsInput struct {
	WsID  string `path:"wsId" doc:"Workspace public ID"`
	Start string `query:"start" doc:"Range start (inclusive, RFC 3339 datetime or YYYY-MM-DD)" required:"true" minLength:"1"`
	End   string `query:"end" doc:"Range end (exclusive, RFC 3339 datetime or YYYY-MM-DD)" required:"true" minLength:"1"`
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
		wsID, actorID, err := resolveWorkspace(ctx, deps.Queries, input.WsID)
		if err != nil {
			return nil, err
		}
		cal, _, err := resolveCalendar(ctx, deps.CalendarQueries, wsID, actorID, input.CalID)
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

		// Non-recurring events: start_at < end, end_at > start (overlap check).
		nonRecurring, err := deps.CalendarQueries.ListCalendarEventsByRange(ctx, calendar.ListCalendarEventsByRangeParams{
			CalendarID: cal.ID,
			StartAt:    sql.NullTime{Time: endTime, Valid: true},
			EndAt:      sql.NullTime{Time: startTime, Valid: true},
		})
		if err != nil {
			return nil, httpErr(apierrors.CalendarEventListQueryInterrupted)
		}

		// Recurring events whose recurrence window overlaps the query range.
		recurring, err := deps.CalendarQueries.ListRecurringCalendarEventsByRange(ctx, calendar.ListRecurringCalendarEventsByRangeParams{
			CalendarID:    cal.ID,
			StartAt:       sql.NullTime{Time: endTime, Valid: true},
			RecurrenceEnd: sql.NullTime{Time: startTime, Valid: true},
		})
		if err != nil {
			return nil, httpErr(apierrors.CalendarEventListQueryInterrupted)
		}

		out := &ListEventsOutput{}
		events := make([]EventResponse, 0, len(nonRecurring)+len(recurring))
		for _, e := range nonRecurring {
			events = append(events, eventFromRangeRow(e, actorID))
		}
		for _, e := range recurring {
			events = append(events, eventFromRecurringRow(e, actorID))
		}
		out.Body.Events = events
		return out, nil
	}
}

// CreateEvent creates a new event in a calendar.
func CreateEvent(deps Deps) func(context.Context, *CreateEventInput) (*CreateEventOutput, error) {
	return func(ctx context.Context, input *CreateEventInput) (*CreateEventOutput, error) {
		wsID, actorID, err := resolveWorkspace(ctx, deps.Queries, input.WsID)
		if err != nil {
			return nil, err
		}
		cal, sub, err := resolveCalendar(ctx, deps.CalendarQueries, wsID, actorID, input.CalID)
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

		visibility := calendar.CalendarEventsVisibilityDefault
		if input.Body.Visibility != "" {
			visibility = calendar.CalendarEventsVisibility(input.Body.Visibility)
		}
		showAs := calendar.CalendarEventsShowAsBusy
		if input.Body.ShowAs != "" {
			showAs = calendar.CalendarEventsShowAs(input.Body.ShowAs)
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
			startAtNT = sql.NullTime{Time: handlerutil.UnixToTime(*input.Body.StartAt), Valid: true}
			endAtNT = sql.NullTime{Time: handlerutil.UnixToTime(*input.Body.EndAt), Valid: true}
		}
		params := calendar.CreateCalendarEventParams{
			PublicID:        eventPublicID,
			WorkspaceID:     wsID,
			CalendarID:      cal.ID,
			Kind:            calendar.CalendarEventsKind(input.Body.Kind),
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
		if input.Body.URL != nil {
			params.Url = sql.NullString{String: *input.Body.URL, Valid: true}
		}
		if input.Body.BlockLabel != nil {
			params.BlockLabel = sql.NullString{String: *input.Body.BlockLabel, Valid: true}
		}
		if input.Body.RecurrenceRule != nil {
			if spec := validateRecurrenceRule(input.Body.RecurrenceRule); spec != nil {
				return nil, httpErr(spec)
			}
			params.RecurrenceRule = json.RawMessage(*input.Body.RecurrenceRule)
		}
		if input.Body.RecurrenceEnd != nil {
			params.RecurrenceEnd = sql.NullTime{Time: handlerutil.UnixToTime(*input.Body.RecurrenceEnd), Valid: true}
		}
		if input.Body.NotificationOffset != nil {
			params.NotificationOffset = sql.NullInt32{Int32: *input.Body.NotificationOffset, Valid: true}
		}

		_, err = deps.CalendarQueries.CreateCalendarEvent(ctx, params)
		if err != nil {
			return nil, httpErr(apierrors.CalendarEventStoreWriteInterrupted)
		}

		// Re-read through the creator-joined query so the response carries
		// the same creator summary (id / display name / avatar) as Get and
		// Patch, instead of hand-building a partial DTO here.
		created, err := deps.CalendarQueries.FindCalendarEventByPublicId(ctx, calendar.FindCalendarEventByPublicIdParams{
			PublicID:    eventPublicID,
			CalendarID:  cal.ID,
			WorkspaceID: wsID,
		})
		if err != nil {
			return nil, httpErr(apierrors.CalendarEventStoreReadInterrupted)
		}

		out := &CreateEventOutput{}
		out.Body = eventFromFullRow(created)

		eventBusPayload := map[string]any{
			"eventId":    eventPublicID.String(),
			"calendarId": input.CalID,
			"title":      input.Body.Title,
			"kind":       input.Body.Kind,
		}
		if startAtNT.Valid {
			eventBusPayload["startAt"] = startAtNT.Time
			eventBusPayload["endAt"] = endAtNT.Time
		}
		_ = appendCalendarEvent(ctx, deps.DB, wsID, "calendar.event.created", &actorID, eventBusPayload)

		deps.Audit.Record(ctx, audit.Entry{
			Action:       "calendar.event.create",
			ActorID:      actorID,
			WorkspaceID:  wsID,
			ResourceType: "calendar.event",
			ResourceID:   eventPublicID.String(),
			Metadata: map[string]any{
				"calendarId": input.CalID,
				"title":      input.Body.Title,
				"kind":       input.Body.Kind,
			},
		})

		return out, nil
	}
}

// GetEvent returns a single event by its public ID.
func GetEvent(deps Deps) func(context.Context, *GetEventInput) (*GetEventOutput, error) {
	return func(ctx context.Context, input *GetEventInput) (*GetEventOutput, error) {
		wsID, actorID, err := resolveWorkspace(ctx, deps.Queries, input.WsID)
		if err != nil {
			return nil, err
		}
		cal, _, err := resolveCalendar(ctx, deps.CalendarQueries, wsID, actorID, input.CalID)
		if err != nil {
			return nil, err
		}

		evtUID, err := uuid.Parse(input.EvtID)
		if err != nil {
			return nil, errEventNotFound
		}
		evt, err := deps.CalendarQueries.FindCalendarEventByPublicId(ctx, calendar.FindCalendarEventByPublicIdParams{
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
		// real ACL; ws membership is the edit gate. Routed through the
		// shared scrub helper so every read path applies the same rule.
		resp := eventFromFullRow(evt)
		scrubPrivateEvent(string(evt.Visibility), evt.OwnerUserID, actorID, &resp.Location, &resp.Memo, &resp.URL)

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
		wsID, actorID, err := resolveWorkspace(ctx, deps.Queries, input.WsID)
		if err != nil {
			return nil, err
		}
		cal, sub, err := resolveCalendar(ctx, deps.CalendarQueries, wsID, actorID, input.CalID)
		if err != nil {
			return nil, err
		}

		evtUID, err := uuid.Parse(input.EvtID)
		if err != nil {
			return nil, errEventNotFound
		}
		evt, err := deps.CalendarQueries.FindCalendarEventByPublicId(ctx, calendar.FindCalendarEventByPublicIdParams{
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
		var attendee *calendar.FindCalendarEventAttendeeRow
		att, attErr := deps.CalendarQueries.FindCalendarEventAttendee(ctx, calendar.FindCalendarEventAttendeeParams{
			EventID: handlerutil.NullInt32From(evt.ID),
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
		cqtx := deps.CalendarQueries.WithTx(tx)

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
				StartAt:     handlerutil.UnixToTime(*input.Body.StartAt),
				EndAt:       handlerutil.UnixToTime(*input.Body.EndAt),
			}); err != nil {
				return nil, translateItemkitError(ctx, "itemkit.RescheduleEvent", err)
			}
			input.Body.StartAt = nil
			input.Body.EndAt = nil
		}

		params := calendar.PatchCalendarEventParams{
			PublicID:    types.FromUUID(evtUID),
			CalendarID:  cal.ID,
			WorkspaceID: wsID,
		}
		if input.Body.Kind != nil {
			params.Kind = calendar.NullCalendarEventsKind{
				CalendarEventsKind: calendar.CalendarEventsKind(*input.Body.Kind),
				Valid:              true,
			}
		}
		if input.Body.Visibility != nil {
			params.Visibility = calendar.NullCalendarEventsVisibility{
				CalendarEventsVisibility: calendar.CalendarEventsVisibility(*input.Body.Visibility),
				Valid:                    true,
			}
		}
		if input.Body.ShowAs != nil {
			params.ShowAs = calendar.NullCalendarEventsShowAs{
				CalendarEventsShowAs: calendar.CalendarEventsShowAs(*input.Body.ShowAs),
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
			params.StartAt = sql.NullTime{Time: handlerutil.UnixToTime(*input.Body.StartAt), Valid: true}
		}
		if input.Body.EndAt != nil {
			params.EndAt = sql.NullTime{Time: handlerutil.UnixToTime(*input.Body.EndAt), Valid: true}
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
		if input.Body.URL != nil {
			params.Url = sql.NullString{String: *input.Body.URL, Valid: true}
		}
		if input.Body.BlockLabel != nil {
			params.BlockLabel = sql.NullString{String: *input.Body.BlockLabel, Valid: true}
		}
		if input.Body.RecurrenceRule != nil {
			if spec := validateRecurrenceRule(input.Body.RecurrenceRule); spec != nil {
				return nil, httpErr(spec)
			}
			// A task-linked event may not become recurring — invariant
			// enforced in itemkit. Flag here so the tx rolls back
			// deterministically.
			if isLinked {
				return nil, httpErr(apierrors.ItemItemkitRecurrenceWithTaskLink)
			}
			params.RecurrenceRule = json.RawMessage(*input.Body.RecurrenceRule)
		}
		if input.Body.RecurrenceEnd != nil {
			params.RecurrenceEnd = sql.NullTime{Time: handlerutil.UnixToTime(*input.Body.RecurrenceEnd), Valid: true}
		}
		if input.Body.RecurrenceExceptions != nil {
			params.RecurrenceExceptions = json.RawMessage(*input.Body.RecurrenceExceptions)
		}
		if input.Body.NotificationOffset != nil {
			params.NotificationOffset = sql.NullInt32{Int32: *input.Body.NotificationOffset, Valid: true}
		}

		if err := cqtx.PatchCalendarEvent(ctx, params); err != nil {
			return nil, httpErr(apierrors.CalendarEventStoreWriteInterrupted)
		}

		// Re-read inside the tx so the response reflects what itemkit
		// + sqlc jointly wrote.
		evt, err = cqtx.FindCalendarEventByPublicId(ctx, calendar.FindCalendarEventByPublicIdParams{
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
			_ = appendCalendarEvent(ctx, deps.DB, wsID, "calendar.event.updated", &actorID, map[string]any{
				"eventId":    input.EvtID,
				"calendarId": input.CalID,
			})
		}

		deps.Audit.Record(ctx, audit.Entry{
			Action:       "calendar.event.update",
			ActorID:      actorID,
			WorkspaceID:  wsID,
			ResourceType: "calendar.event",
			ResourceID:   input.EvtID,
			Metadata: map[string]any{
				"calendarId": input.CalID,
			},
		})

		return out, nil
	}
}

// translateItemkitError maps itemkit invariant / generic errors to the
// calendar apierrors spec set. The original error is logged via the
// shared classifier in handlerutil so schema drift or other low-level
// failures do not disappear into a generic 500. Known sentinels
// (sql.ErrNoRows) are surfaced as 404. Invariant / recurrence
// messages are mapped to their dedicated 4xx codes. Anything else
// falls through to the op-specific 5xx (delete vs. write) — DELETE
// reports a delete-failure and PATCH/POST reports a write-failure.
//
// Implementation detail: the classifier itself lives in handlerutil
// so the task-domain translator stays in lockstep on which itemkit
// messages map to public codes.
func translateItemkitError(ctx context.Context, op string, err error) error {
	fallback := apierrors.CalendarEventStoreWriteInterrupted
	if strings.Contains(op, "DeleteEvent") {
		fallback = apierrors.CalendarEventStoreDeleteInterrupted
	}
	return handlerutil.TranslateCalendarItemkitError(ctx, op, err, fallback)
}

// DeleteEvent soft-deletes a calendar event. Requires edit permission.
// Delegates to itemkit.DeleteEvent which clears the corresponding
// tasks.due_on column when the event was task-linked
// (task_role = 'due'), leaving the task itself intact.
func DeleteEvent(deps Deps) func(context.Context, *DeleteEventInput) (*DeleteEventOutput, error) {
	return func(ctx context.Context, input *DeleteEventInput) (*DeleteEventOutput, error) {
		wsID, actorID, err := resolveWorkspace(ctx, deps.Queries, input.WsID)
		if err != nil {
			return nil, err
		}
		cal, sub, err := resolveCalendar(ctx, deps.CalendarQueries, wsID, actorID, input.CalID)
		if err != nil {
			return nil, err
		}

		evtUID, err := uuid.Parse(input.EvtID)
		if err != nil {
			return nil, errEventNotFound
		}
		evt, err := deps.CalendarQueries.FindCalendarEventByPublicId(ctx, calendar.FindCalendarEventByPublicIdParams{
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

		var attendee *calendar.FindCalendarEventAttendeeRow
		att, attErr := deps.CalendarQueries.FindCalendarEventAttendee(ctx, calendar.FindCalendarEventAttendeeParams{
			EventID: handlerutil.NullInt32From(evt.ID),
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
			// Mirror the unwrapped pattern used by the other itemkit
			// call sites (RenameItem / RescheduleEvent above): the
			// translator's structured log already records the op name,
			// and the classifier matches on the original error's
			// message so wrapping would only duplicate context.
			return nil, translateItemkitError(ctx, "itemkit.DeleteEvent", err)
		}
		if err := tx.Commit(); err != nil {
			return nil, httpErr(apierrors.CalendarEventStoreDeleteInterrupted)
		}

		out := &DeleteEventOutput{}
		out.Body.Deleted = true

		// itemkit already emitted item.unscheduled + legacy
		// calendar.event.deleted. No extra append.

		deps.Audit.Record(ctx, audit.Entry{
			Action:       "calendar.event.delete",
			ActorID:      actorID,
			WorkspaceID:  wsID,
			ResourceType: "calendar.event",
			ResourceID:   input.EvtID,
			Metadata: map[string]any{
				"calendarId": input.CalID,
				"title":      evt.Title,
			},
		})

		return out, nil
	}
}

// ListCalendarEvents returns events across all calendars the user subscribes to
// in a workspace within a time range.
func ListCalendarEvents(deps Deps) func(context.Context, *ListCalendarEventsInput) (*ListCalendarEventsOutput, error) {
	return func(ctx context.Context, input *ListCalendarEventsInput) (*ListCalendarEventsOutput, error) {
		wsID, actorID, err := resolveWorkspace(ctx, deps.Queries, input.WsID)
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
		rows, err := deps.CalendarQueries.ListCalendarEventsAcrossCalendars(ctx, calendar.ListCalendarEventsAcrossCalendarsParams{
			UserID:      actorID,
			WorkspaceID: wsID,
			StartAt:     sql.NullTime{Time: endTime, Valid: true},
			EndAt:       sql.NullTime{Time: startTime, Valid: true},
		})
		if err != nil {
			return nil, httpErr(apierrors.CalendarEventListQueryInterrupted)
		}

		// Recurring events whose recurrence window overlaps the query range
		recurringRows, err := deps.CalendarQueries.ListRecurringCalendarEventsAcrossCalendars(ctx, calendar.ListRecurringCalendarEventsAcrossCalendarsParams{
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
			resp.Location = dbtype.PtrFromNullString(r.Location)
			resp.BlockLabel = dbtype.PtrFromNullString(r.BlockLabel)
			resp.UpdatedAt = dbtype.UnixSecondsFromNullTime(r.UpdatedAt)
			scrubPrivateEvent(string(r.Visibility), r.OwnerUserID, actorID, &resp.Location, nil, nil)
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
			resp.Location = dbtype.PtrFromNullString(r.Location)
			resp.BlockLabel = dbtype.PtrFromNullString(r.BlockLabel)
			if r.RecurrenceRule != nil {
				raw := json.RawMessage(r.RecurrenceRule)
				resp.RecurrenceRule = &raw
			}
			resp.RecurrenceEnd = dbtype.UnixSecondsFromNullTime(r.RecurrenceEnd)
			if r.RecurrenceExceptions != nil {
				raw := json.RawMessage(r.RecurrenceExceptions)
				resp.RecurrenceExceptions = &raw
			}
			resp.UpdatedAt = dbtype.UnixSecondsFromNullTime(r.UpdatedAt)
			scrubPrivateEvent(string(r.Visibility), r.OwnerUserID, actorID, &resp.Location, nil, nil)
			out.Body.Events = append(out.Body.Events, resp)
		}

		return out, nil
	}
}

// parseFlexibleTime parses a date string ("2006-01-02") or a full
// RFC 3339 datetime string into a time.Time. The returned error is an
// internal sentinel; every caller in this package wraps it with
// httpErr(apierrors.CalendarEventDateRangeUnparseable) before returning
// to the HTTP layer, so the sentinel itself never reaches a client.
func parseFlexibleTime(s string) (time.Time, error) {
	if t, err := time.Parse(time.RFC3339, s); err == nil {
		return t, nil
	}
	if t, err := time.Parse("2006-01-02", s); err == nil {
		return t, nil
	}
	return time.Time{}, errInvalidFlexibleTime
}

// errInvalidFlexibleTime is the internal sentinel returned by
// parseFlexibleTime when neither RFC 3339 nor YYYY-MM-DD parsing
// succeeds. Handlers translate this to
// apierrors.CalendarEventDateRangeUnparseable.
var errInvalidFlexibleTime = errors.New("calendar: cannot parse as date or datetime")
