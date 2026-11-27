package calendars

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"

	"github.com/google/uuid"

	"github.com/libraz/nodate-flow/apps/flow-api/internal/calendarlabels"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/calendaroccurrence"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/calendarrules"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/dbretry"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/generated/calendar"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/types"
	apierrors "github.com/libraz/nodate-flow/apps/flow-api/internal/errors"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/eventbus"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/http/handlers/handlerutil"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/itemkit"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/mutationlog"
	"github.com/libraz/nodate-flow/packages/go-shared/dbtype"
	"github.com/libraz/nodate-flow/packages/go-shared/eventacl"
)

// --- Input/Output types ---

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
	Flexibility          string           `json:"flexibility"`
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
	Color                string           `json:"color"`
	OwnerColor           string           `json:"ownerColor"`
	LabelID              *string          `json:"labelId"`
	LabelName            *string          `json:"labelName"`
	DetailsHidden        bool             `json:"detailsHidden"`
	CanEdit              bool             `json:"canEdit"`
}

// CreateEventInput is the input for the create event endpoint.
//
// The maxLength on each textual field is the width of the column behind
// it (calendar_events.title / location / url / timezone / block_label).
// Stating it here is what refuses an over-long value as a validation
// error naming the field; without it the only thing left refusing it is
// the driver, which answers as a write failure for input this API
// described as acceptable. Memo is the one whose bound is not its
// column's: it is MEDIUMTEXT, so the column would draw the line at 16 MB
// of prose, and the number stated is the one the tools writing the same
// column state.
type CreateEventInput struct {
	WsID           string `path:"wsId" doc:"Workspace public ID"`
	CalID          string `path:"calId" doc:"Calendar public ID"`
	IdempotencyKey string `header:"Idempotency-Key" maxLength:"128" pattern:"^[A-Za-z0-9._~-]{1,128}$" doc:"Optional key for safely retrying event creation"`
	Body           struct {
		Kind               string           `json:"kind" enum:"event,block,free,milestone" doc:"Event kind"`
		Visibility         string           `json:"visibility,omitempty" required:"false" enum:"default,public,private,confidential" doc:"Visibility"`
		ShowAs             string           `json:"showAs,omitempty" required:"false" enum:"busy,free,tentative,oof" doc:"Show-as status"`
		Flexibility        string           `json:"flexibility,omitempty" required:"false" enum:"fixed,negotiable,conditional" doc:"Whether the commitment can be moved; independent of showAs, which only says whether the time reads as taken"`
		Title              string           `json:"title" minLength:"1" maxLength:"500" doc:"Event title"`
		AllDay             bool             `json:"allDay" required:"false" doc:"All-day event flag"`
		StartAt            *int64           `json:"startAt,omitempty" required:"false" doc:"Start time as unix seconds (UTC); omit for a planning-stage (undated) event. Not accepted with allDay."`
		EndAt              *int64           `json:"endAt,omitempty" required:"false" doc:"End time as unix seconds (UTC); omit for a planning-stage (undated) event. Not accepted with allDay."`
		StartDate          *string          `json:"startDate,omitempty" required:"false" pattern:"^[0-9]{4}-[0-9]{2}-[0-9]{2}$" doc:"First day of an all-day event (YYYY-MM-DD). Only with allDay."`
		EndDate            *string          `json:"endDate,omitempty" required:"false" pattern:"^[0-9]{4}-[0-9]{2}-[0-9]{2}$" doc:"Day after the last day of an all-day event (YYYY-MM-DD, exclusive); equal to startDate for a milestone. Only with allDay."`
		Timezone           string           `json:"timezone" maxLength:"64" doc:"IANA timezone"`
		Location           *string          `json:"location,omitempty" required:"false" maxLength:"500" doc:"Location"`
		Memo               *string          `json:"memo,omitempty" required:"false" maxLength:"10000" doc:"Memo / notes"`
		URL                *string          `json:"url,omitempty" required:"false" maxLength:"2048" doc:"Related URL"`
		OwnerUserID        *string          `json:"ownerUserId,omitempty" required:"false" doc:"Owner user public ID (defaults to actor)"`
		BlockLabel         *string          `json:"blockLabel,omitempty" required:"false" maxLength:"100" doc:"Block label"`
		RecurrenceRule     *json.RawMessage `json:"recurrenceRule,omitempty" required:"false" doc:"RFC 5545 recurrence rule as JSON"`
		RecurrenceEnd      *int64           `json:"recurrenceEnd,omitempty" required:"false" doc:"Recurrence end as unix seconds (UTC)"`
		NotificationOffset *int32           `json:"notificationOffset,omitempty" required:"false" doc:"Notification offset in minutes"`
		LabelID            *string          `json:"labelId,omitempty" required:"false" doc:"Calendar label public ID; empty means no label"`
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

// DeleteEventInput is the input for the delete event endpoint.
//
// Scope says which occurrences of a recurring series the delete reaches,
// and OccurrenceStart says which occurrence it starts from. A delete
// carries no body, so both arrive as query parameters.
//
// An omitted Scope is the whole series, which is what this route has
// always done.
//
// OccurrenceStart has no absent value of its own: a query parameter may
// not be a pointer, so zero stands for omitted. The instant that
// displaces is 1970-01-01T00:00:00Z, which is not an occurrence any
// series reachable through this API produces.
type DeleteEventInput struct {
	WsID            string `path:"wsId" doc:"Workspace public ID"`
	CalID           string `path:"calId" doc:"Calendar public ID"`
	EvtID           string `path:"evtId" doc:"Event public ID"`
	Scope           string `query:"scope" required:"false" enum:"series,occurrence,thisAndFollowing" doc:"Which occurrences of a recurring series this delete reaches. Omitted means the whole series."`
	OccurrenceStart int64  `query:"occurrenceStart" required:"false" doc:"The occurrence's start under the series rule, as unix seconds (UTC). Required when scope is not series."`
}

// DeleteEventOutput is the response for the delete event endpoint.
type DeleteEventOutput struct {
	Body struct {
		Deleted bool `json:"deleted"`
	}
}

// --- Handlers ---

// requireNotificationOffset refuses a reminder offset outside the range
// the column's CHECK accepts.
func requireNotificationOffset(offset *int32) error {
	if offset == nil || (*offset >= 0 && *offset <= maxNotificationOffset) {
		return nil
	}
	return httpErr(apierrors.CalendarEventNotificationOffsetOutOfRange)
}

// maxNotificationOffset is seven days in minutes.
const maxNotificationOffset = 10080

// findActorAttendee returns the actor's enabled attendee row for an event,
// or nil when there is none. An override that holds attendee rows of its
// own, enabled or not, is judged on them alone; one with none reads its
// series' rows.
func findActorAttendee(
	ctx context.Context,
	q *calendar.Queries,
	actorID, eventID uint32,
	parentID sql.NullInt32,
) (*calendar.FindCalendarEventAttendeeRow, error) {
	target := handlerutil.NullInt32From(eventID)
	if parentID.Valid {
		own, err := q.HasCalendarEventAttendeeRows(ctx, target)
		if err != nil {
			return nil, err
		}
		if !own {
			target = parentID
		}
	}
	row, err := q.FindCalendarEventAttendee(ctx, calendar.FindCalendarEventAttendeeParams{
		EventID: target,
		UserID:  actorID,
	})
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &row, nil
}

// CreateEvent creates a new event in a calendar.
func CreateEvent(deps Deps) func(context.Context, *CreateEventInput) (*CreateEventOutput, error) {
	return func(ctx context.Context, input *CreateEventInput) (*CreateEventOutput, error) {
		wsID, actorID, err := resolveWorkspace(ctx, deps.Queries, input.WsID)
		if err != nil {
			return nil, err
		}
		cal, sub, err := resolveCalendarWrite(ctx, deps.CalendarQueries, wsID, actorID, input.CalID)
		if err != nil {
			return nil, err
		}

		ownerUserID := actorID
		if input.IdempotencyKey == "" {
			ownerUserID, err = resolveEventCreateOwner(ctx, deps.Queries, wsID, actorID, sub, input.Body.OwnerUserID)
			if err != nil {
				if !isHTTPStatusError(err) {
					return nil, httpErr(apierrors.CalendarEventOwnerUserResolveInterrupted)
				}
				return nil, err
			}
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
		// Default to 'fixed'. An event created by a client that does not
		// know about the column must not read as movable, because that
		// would advertise availability its owner never offered.
		flexibility := calendar.CalendarEventsFlexibilityFixed
		if input.Body.Flexibility != "" {
			flexibility = calendar.CalendarEventsFlexibility(input.Body.Flexibility)
		}

		// Planning-stage undated events: both start and end may be omitted
		// (persisted as NULL). Requesting start without end (or vice versa)
		// is rejected to keep the pair invariant enforced by
		// chk_calendar_events_start_end_pair.
		//
		// An all-day event is named by dates, a timed one by instants; the
		// dates become the stored window here so everything below sees one
		// shape.
		startUnix, endUnix := input.Body.StartAt, input.Body.EndAt
		if refusal := calendarrules.RequireAllDayInput(input.Body.AllDay,
			startUnix != nil || endUnix != nil,
			input.Body.StartDate != nil || input.Body.EndDate != nil); refusal != nil {
			return nil, handlerutil.HTTPErrFromAPIError(refusal)
		}
		if input.Body.AllDay && (input.Body.StartDate != nil || input.Body.EndDate != nil) {
			startUnix, endUnix, err = resolveAllDayDates(input.Body.Kind,
				input.Body.StartDate, input.Body.EndDate, sql.NullTime{}, sql.NullTime{})
			if err != nil {
				return nil, err
			}
		}
		if err := requireEventStartEndPair(startUnix, endUnix); err != nil {
			return nil, err
		}
		if err := requireEventChronology(startUnix, endUnix); err != nil {
			return nil, err
		}
		if err := requireDatetime("recurrenceEnd", input.Body.RecurrenceEnd); err != nil {
			return nil, err
		}
		if err := requireNotificationOffset(input.Body.NotificationOffset); err != nil {
			return nil, err
		}
		if err := requireEventShape(input.Body.Kind, startUnix != nil,
			input.Body.RecurrenceRule != nil && calendaroccurrence.HasRule(*input.Body.RecurrenceRule),
			input.Body.NotificationOffset != nil); err != nil {
			return nil, err
		}

		// An explicit timezone has to resolve, or the row is stored with a
		// zone no grid can place the event in; an omitted one falls back
		// to the caller's preference and then the workspace's.
		timezone, tzErr := resolveEffectiveTimezone(ctx, deps.Queries, wsID, actorID, input.Body.Timezone)
		if tzErr != nil {
			return nil, tzErr
		}

		var startAtNT, endAtNT sql.NullTime
		if startUnix != nil {
			startAtNT = sql.NullTime{Time: handlerutil.UnixToTime(*startUnix), Valid: true}
			endAtNT = sql.NullTime{Time: handlerutil.UnixToTime(*endUnix), Valid: true}
		}
		params := calendar.CreateCalendarEventParams{
			PublicID:        eventPublicID,
			WorkspaceID:     wsID,
			CalendarID:      cal.ID,
			Kind:            calendar.CalendarEventsKind(input.Body.Kind),
			Visibility:      visibility,
			ShowAs:          showAs,
			Flexibility:     flexibility,
			Title:           input.Body.Title,
			AllDay:          input.Body.AllDay,
			StartAt:         startAtNT,
			EndAt:           endAtNT,
			Timezone:        timezone.Name(),
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
		if input.IdempotencyKey != "" {
			return createEventWithIdempotency(ctx, deps, input, wsID, actorID, cal, params)
		}

		if input.Body.LabelID != nil && *input.Body.LabelID != "" {
			labelID, refusal, rerr := calendarlabels.ResolveLabel(ctx, deps.CalendarQueries, cal.ID, *input.Body.LabelID)
			if rerr != nil {
				return nil, httpErr(apierrors.CalendarEventStoreReadInterrupted)
			}
			if refusal != nil {
				return nil, handlerutil.HTTPErrFromAPIError(refusal)
			}
			err = createEventWithLabel(ctx, deps, wsID, params, labelID)
		} else {
			_, err = deps.CalendarQueries.CreateCalendarEvent(ctx, params)
		}
		if err != nil {
			if isHTTPStatusError(err) {
				return nil, err
			}
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
		// The creator is a party to the event and may edit it.
		out.Body = eventFromFullRow(created, eventViewer{
			UserID:          actorID,
			CalendarDefault: string(cal.DefaultEventVisibility),
			CanEdit:         true,
		})

		payload := map[string]any{
			"eventId":    eventPublicID.String(),
			"calendarId": input.CalID,
			"title":      input.Body.Title,
			"kind":       input.Body.Kind,
		}
		if startAtNT.Valid {
			payload["startAt"] = startAtNT.Time
			payload["endAt"] = endAtNT.Time
		}
		recordCalendarEventChange(ctx, deps, wsID, cal.ID, actorID, created.PublicID, created.Title, mutationlog.Mutation{
			EventType:    eventbus.CalEventCreated,
			AuditAction:  "calendar.event.create",
			ResourceType: "calendar.event",
			ResourceID:   eventPublicID.String(),
			Payload:      payload,
			CallSite:     "calendars.CreateEvent",
		})

		return out, nil
	}
}

// createEventWithLabel inserts the event and its label link in one
// transaction, so a link refused at write time leaves no event behind.
func createEventWithLabel(ctx context.Context, deps Deps, wsID uint32, params calendar.CreateCalendarEventParams, labelID uint32) error {
	var answered error
	err := dbretry.InTx(ctx, deps.DB, "calendars.CreateEvent", nil, func(ctx context.Context, tx *dbretry.Tx) error {
		answered = nil
		cq := deps.CalendarQueries.WithTx(tx.RawTx())
		id, err := cq.CreateCalendarEvent(ctx, params)
		if err != nil {
			return err
		}
		_, refusal, err := calendarlabels.SetLink(ctx, cq, wsID, uint32(id), labelID) //#nosec G115 -- LastInsertId of an INT UNSIGNED key
		if refusal != nil {
			answered = handlerutil.HTTPErrFromAPIError(refusal)
			return refusal
		}
		return err
	})
	if answered != nil {
		return answered
	}
	return err
}

// GetEvent returns a single event by its public ID.
func GetEvent(deps Deps) func(context.Context, *GetEventInput) (*GetEventOutput, error) {
	return func(ctx context.Context, input *GetEventInput) (*GetEventOutput, error) {
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

		// Attendance decides whether a private event's room and link are
		// this viewer's to read, so it is resolved before either check.
		// One lookup is affordable here; the list paths get the same
		// answer from the is_attendee column instead of a query per row.
		// An override with attendee rows of its own replaces its series'
		// list; one without reads the series'.
		attendanceRow, aerr := findActorAttendee(ctx, deps.CalendarQueries, actorID, evt.ID, evt.RecurrenceParentID)
		if aerr != nil {
			return nil, httpErr(apierrors.CalendarEventStoreReadInterrupted)
		}
		isAttendee := attendanceRow != nil

		aclEvent := eventacl.Event{
			Visibility:      eventacl.Visibility(evt.Visibility),
			OwnerUserID:     evt.OwnerUserID,
			CreatedByUserID: evt.CreatedByUserID,
			CalendarDefault: eventacl.Visibility(cal.DefaultEventVisibility),
		}
		aclActor := eventacl.Actor{UserID: actorID, IsAttendee: isAttendee}

		// A confidential event is not this viewer's to know about, so it
		// answers as an unknown id rather than as a refusal — a 403 here
		// would confirm that the executive has something at that hour,
		// which is the fact the setting exists to hide. The list queries
		// drop the same rows via eventacl.RowVisibilitySQL.
		if !eventacl.CanSee(aclEvent, aclActor) {
			return nil, errEventNotFound
		}

		out := &GetEventOutput{}
		out.Body = eventFromFullRow(evt, eventViewer{
			UserID:          actorID,
			IsAttendee:      isAttendee,
			CalendarDefault: string(cal.DefaultEventVisibility),
			CanEdit:         canEditEvent(actorID, evt, sub, attendanceRow),
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
//
// A non-series scope deletes part of a recurring series instead: the
// master survives and the occurrences named stop being produced. Those
// paths patch recurrence_exceptions or recurrence_end and leave every
// other column to COALESCE, so the route carries no window and no
// all-day flag of its own:
//
// calendar-precondition: chronology not-applicable — this route sends no start and no end, so it has no window to order
// calendar-precondition: all-day-bounds not-applicable — this route sends no all-day flag and no window, so it has no bounds to pin
//
// The task column it does write is cleared rather than set. A task with
// no due date has no ordering to break, which is the answer
// taskrules.DateOrder itself gives a NULL side:
//
// task-precondition: date-order not-applicable — this route only clears due_on, and a task with no due date has no pair to order
func DeleteEvent(deps Deps) func(context.Context, *DeleteEventInput) (*DeleteEventOutput, error) {
	return func(ctx context.Context, input *DeleteEventInput) (*DeleteEventOutput, error) {
		wsID, actorID, err := resolveWorkspace(ctx, deps.Queries, input.WsID)
		if err != nil {
			return nil, err
		}
		cal, sub, err := resolveCalendarWrite(ctx, deps.CalendarQueries, wsID, actorID, input.CalID)
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

		attendee, attErr := findActorAttendee(ctx, deps.CalendarQueries, actorID, evt.ID, evt.RecurrenceParentID)
		if attErr != nil {
			return nil, httpErr(apierrors.CalendarEventStoreReadInterrupted)
		}
		if !canEditEvent(actorID, evt, sub, attendee) {
			return nil, httpErr(apierrors.CalendarEventEditPermissionRequired)
		}

		scope, scopeErr := decodeOccurrenceScope(&input.Scope)
		if scopeErr != nil {
			// The scope arrives in the query string here, so the refusal
			// names the parameter the caller actually sent.
			return nil, invalidQueryField("scope")
		}
		// The parent link is read only when its answer can change the
		// outcome. A series delete reaches no refusal that depends on it,
		// and asking anyway would put a query on the common path for a
		// question it never has.
		var parentID sql.NullInt32
		if scope != scopeSeries {
			link, linkErr := deps.CalendarQueries.FindCalendarEventRecurrenceLink(ctx, calendar.FindCalendarEventRecurrenceLinkParams{
				WorkspaceID: wsID,
				PublicID:    evt.PublicID,
			})
			if linkErr != nil {
				return nil, httpErr(apierrors.CalendarEventStoreReadInterrupted)
			}
			parentID = link.RecurrenceParentID
		}
		if err := requireDeleteOccurrenceScope(scope, input.OccurrenceStart, evt, parentID); err != nil {
			return nil, err
		}
		if !dbtype.DatetimeRepresentable(input.OccurrenceStart) {
			return nil, handlerutil.HTTPErrFromAPIError(
				apierrors.New(apierrors.ValidationQueryFieldInvalid).WithDetail("field", "occurrenceStart"))
		}

		if scope != scopeSeries {
			occurrenceStart := handlerutil.UnixToTime(input.OccurrenceStart)
			var partErr error
			if scope == scopeOccurrence {
				partErr = deleteEventOccurrence(ctx, deps, wsID, evt, occurrenceStart)
			} else {
				partErr = deleteEventFollowing(ctx, deps, wsID, actorID, cal, evt, occurrenceStart)
			}
			if partErr != nil {
				return nil, partErr
			}
			recordOccurrenceDelete(ctx, deps, wsID, actorID, input, scope, evt, cal.ID)

			out := &DeleteEventOutput{}
			out.Body.Deleted = true
			return out, nil
		}

		var answered error
		txErr := dbretry.InTx(ctx, deps.DB, "calendars.DeleteEvent", nil, func(ctx context.Context, tx *dbretry.Tx) error {
			answered = nil
			// A deleted override takes its occurrence with it rather than
			// handing the slot back to the master.
			if err := calendaroccurrence.CancelOverride(ctx, seriesQueries(deps, tx), wsID, evt); err != nil {
				return err
			}
			if err := itemkit.DeleteEvent(ctx, tx, wsID, evt.ID, actorID); err != nil {
				// Mirror the unwrapped pattern used by the other itemkit
				// call sites (RenameItem / RescheduleEvent above): the
				// translator's structured log already records the op name,
				// and the classifier matches on the original error's
				// message so wrapping would only duplicate context.
				answered = translateItemkitError(ctx, "itemkit.DeleteEvent", err)
				return err
			}
			return nil
		})
		if answered != nil {
			return nil, answered
		}
		if txErr != nil {
			return nil, httpErr(apierrors.CalendarEventStoreDeleteInterrupted)
		}

		out := &DeleteEventOutput{}
		out.Body.Deleted = true

		// Audit only: itemkit.DeleteEvent emitted item.unscheduled and
		// the legacy calendar.event.deleted kind inside the transaction
		// above, which is what makes the delete and its event atomic. A
		// second append here would show the deletion twice.
		recordCalendarAudit(ctx, deps, wsID, actorID, mutationlog.Mutation{
			AuditAction:  "calendar.event.delete",
			ResourceType: "calendar.event",
			ResourceID:   input.EvtID,
			Payload: map[string]any{
				"calendarId": input.CalID,
				"title":      evt.Title,
			},
			CallSite: "calendars.DeleteEvent",
		})

		return out, nil
	}
}
