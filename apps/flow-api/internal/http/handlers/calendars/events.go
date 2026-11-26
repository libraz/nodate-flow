package calendars

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"reflect"
	"slices"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/libraz/nodate-flow/apps/flow-api/internal/ai/embed"
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
	"github.com/libraz/nodate-flow/apps/flow-api/internal/taskrules"
	"github.com/libraz/nodate-flow/packages/go-shared/dbtype"
	"github.com/libraz/nodate-flow/packages/go-shared/eventacl"
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

// ListEventsOutput is the response for the list events endpoint.
type ListEventsOutput struct {
	Body struct {
		Events []EventResponse `json:"events"`
	}
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

// PatchEventInput is the input for the patch event endpoint.
//
// The enum-backed fields carry the same value sets as CreateEventInput.
// They are the column's own sets, and stating them here is what refuses an
// unrecognised value with a validation error naming the field. Without the
// constraint the only thing left refusing it is the ENUM itself, which
// answers as a write failure the caller cannot act on — and every reader
// downstream has to treat a value it does not know as the safest one it
// can, because it arrived through a route that never checked.
//
// The textual fields carry the same maxLength values as CreateEventInput,
// for the same reason: a bound that only one of the two write routes
// states is not a bound on the column.
//
// Title also carries minLength, because an omitted field is already how
// this route says "leave it alone" — so an explicit "" is a value, and
// without the constraint it is written to a NOT NULL column that create
// refuses to leave empty, leaving an event nothing can name.
type PatchEventInput struct {
	WsID  string `path:"wsId" doc:"Workspace public ID"`
	CalID string `path:"calId" doc:"Calendar public ID"`
	EvtID string `path:"evtId" doc:"Event public ID"`
	Body  struct {
		Kind                 *string          `json:"kind,omitempty" required:"false" enum:"event,block,free,milestone" doc:"Event kind"`
		Visibility           *string          `json:"visibility,omitempty" required:"false" enum:"default,public,private,confidential" doc:"Visibility"`
		ShowAs               *string          `json:"showAs,omitempty" required:"false" enum:"busy,free,tentative,oof" doc:"Show-as status"`
		Flexibility          *string          `json:"flexibility,omitempty" required:"false" enum:"fixed,negotiable,conditional" doc:"Whether the commitment can be moved; independent of showAs, which only says whether the time reads as taken"`
		Title                *string          `json:"title,omitempty" required:"false" minLength:"1" maxLength:"500" doc:"Event title"`
		AllDay               *bool            `json:"allDay,omitempty" required:"false" doc:"All-day flag"`
		StartAt              *int64           `json:"startAt,omitempty" required:"false" doc:"Start time as unix seconds (UTC). Not accepted for an all-day event."`
		EndAt                *int64           `json:"endAt,omitempty" required:"false" doc:"End time as unix seconds (UTC). Not accepted for an all-day event."`
		StartDate            *string          `json:"startDate,omitempty" required:"false" pattern:"^[0-9]{4}-[0-9]{2}-[0-9]{2}$" doc:"First day of an all-day event (YYYY-MM-DD). Only for an all-day event; switching allDay needs both dates."`
		EndDate              *string          `json:"endDate,omitempty" required:"false" pattern:"^[0-9]{4}-[0-9]{2}-[0-9]{2}$" doc:"Day after the last day of an all-day event (YYYY-MM-DD, exclusive); equal to startDate for a milestone."`
		Timezone             *string          `json:"timezone,omitempty" required:"false" maxLength:"64" doc:"IANA timezone"`
		Location             *string          `json:"location,omitempty" required:"false" maxLength:"500" doc:"Location"`
		Memo                 *string          `json:"memo,omitempty" required:"false" maxLength:"10000" doc:"Memo"`
		URL                  *string          `json:"url,omitempty" required:"false" maxLength:"2048" doc:"Related URL"`
		BlockLabel           *string          `json:"blockLabel,omitempty" required:"false" maxLength:"100" doc:"Block label"`
		RecurrenceRule       *json.RawMessage `json:"recurrenceRule,omitempty" required:"false" doc:"Recurrence rule"`
		RecurrenceEnd        *int64           `json:"recurrenceEnd,omitempty" required:"false" doc:"Recurrence end as unix seconds (UTC)"`
		RecurrenceExceptions *json.RawMessage `json:"recurrenceExceptions,omitempty" required:"false" doc:"Array of ISO 8601 dates/times to exclude from recurrence"`
		NotificationOffset   *int32           `json:"notificationOffset,omitempty" required:"false" doc:"Notification offset"`
		LabelID              *string          `json:"labelId,omitempty" required:"false" doc:"Calendar label public ID to file the event under"`
		// Clear names the nullable fields to set back to nothing.
		//
		// A PATCH that carries only values cannot express removal: the
		// field the caller left out and the field they want emptied
		// arrive identically. So every nullable column was write-once
		// through this route — the dialog's "no repeat" saved
		// successfully and the meeting kept recurring, and a location
		// typed by mistake stayed for good.
		//
		// Clearing recurrenceRule clears recurrenceEnd and
		// recurrenceExceptions with it: they describe a series, and
		// leaving them on a row that no longer has one keeps state
		// nothing reads and the next writer has to reason about.
		Clear []string `json:"clear,omitempty" required:"false" enum:"location,memo,url,blockLabel,recurrenceRule,recurrenceEnd,recurrenceExceptions,notificationOffset,labelId" doc:"Nullable fields to clear. Takes precedence over a value sent for the same field in this request."`
		// Scope says which occurrences of a recurring series the patch
		// reaches, and OccurrenceStart says which occurrence it starts
		// from.
		//
		// An omitted Scope is the whole series, which is what this route
		// has always done: a body that carries neither member patches the
		// master and every occurrence its rule produces.
		Scope           *string `json:"scope,omitempty" required:"false" enum:"series,occurrence,thisAndFollowing" doc:"Which occurrences of a recurring series this patch reaches. Omitted means the whole series."`
		OccurrenceStart *int64  `json:"occurrenceStart,omitempty" required:"false" doc:"The occurrence's start under the series rule, as unix seconds (UTC). Required when scope is not series; identifies the occurrence even after the edit moves it."`
	}
}

// clearableEventFields maps the API names accepted in PatchEventInput's
// Clear list to the flags the patch query takes.
type clearableEventFields struct {
	location             int64
	memo                 int64
	url                  int64
	blockLabel           int64
	recurrenceRule       int64
	recurrenceEnd        int64
	recurrenceExceptions int64
	notificationOffset   int64
	labelID              int64
}

// parseClearFields reads the Clear list. An unknown name is rejected
// rather than ignored: a caller who misspells a field would otherwise
// get a success response for a removal that did not happen, which is the
// failure this whole mechanism exists to remove.
func parseClearFields(names []string) (clearableEventFields, error) {
	var out clearableEventFields
	for _, name := range names {
		switch name {
		case "location":
			out.location = 1
		case "memo":
			out.memo = 1
		case "url":
			out.url = 1
		case "blockLabel":
			out.blockLabel = 1
		case "recurrenceRule":
			out.recurrenceRule = 1
		case "recurrenceEnd":
			out.recurrenceEnd = 1
		case "recurrenceExceptions":
			out.recurrenceExceptions = 1
		case "notificationOffset":
			out.notificationOffset = 1
		case "labelId":
			out.labelID = 1
		default:
			return clearableEventFields{}, httpErr(apierrors.ValidationBodyFieldInvalid)
		}
	}
	return out, nil
}

// PatchEventOutput is the response for the patch event endpoint.
type PatchEventOutput struct {
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

// ListCalendarEventsInput is the input for the cross-calendar event list endpoint.
type ListCalendarEventsInput struct {
	WsID  string `path:"wsId" doc:"Workspace public ID"`
	Start string `query:"start" doc:"Range start (inclusive, RFC 3339 datetime or YYYY-MM-DD)" required:"true" minLength:"1"`
	End   string `query:"end" doc:"Range end (exclusive, RFC 3339 datetime or YYYY-MM-DD)" required:"true" minLength:"1"`
}

// CrossCalendarEventResponse is the JSON representation of a cross-calendar event.
// StartAt / EndAt are nullable (see EventResponse).
//
// OverriddenStarts carries ISO-8601 instants rather than the unix seconds
// the rest of this DTO uses for time. It is read by the same client-side
// parser as RecurrenceExceptions, which is the stored ISO-8601 list served
// through verbatim, and the two say different things about the same
// occurrence: an exception says it does not happen, an overridden start
// says a separate override row draws it instead. Spelling one of them in
// seconds would need a second parser for a list the expander already reads
// through one.
type CrossCalendarEventResponse struct {
	ID                   string           `json:"id"`
	CalendarID           string           `json:"calendarId"`
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
	BlockLabel           *string          `json:"blockLabel,omitempty"`
	RecurrenceRule       *json.RawMessage `json:"recurrenceRule,omitempty"`
	RecurrenceEnd        *int64           `json:"recurrenceEnd,omitempty"`
	RecurrenceExceptions *json.RawMessage `json:"recurrenceExceptions,omitempty"`
	OverriddenStarts     []string         `json:"overriddenStarts,omitempty" doc:"Occurrence starts a separate override row already stands in for (RFC 3339 UTC). Recurring masters only."`
	UpdatedAt            *int64           `json:"updatedAt,omitempty"`
	CreatedAt            int64            `json:"createdAt"`
	Color                string           `json:"color"`
	OwnerColor           string           `json:"ownerColor"`
	LabelID              *string          `json:"labelId"`
	LabelName            *string          `json:"labelName"`
	DetailsHidden        bool             `json:"detailsHidden"`
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
			ViewerUserID: actorID,
			CalendarID:   cal.ID,
			RangeEnd:     sql.NullTime{Time: endTime, Valid: true},
			RangeStart:   sql.NullTime{Time: startTime, Valid: true},
		})
		if err != nil {
			return nil, httpErr(apierrors.CalendarEventListQueryInterrupted)
		}

		// Recurring events whose recurrence window overlaps the query range.
		recurring, err := deps.CalendarQueries.ListRecurringCalendarEventsByRange(ctx, calendar.ListRecurringCalendarEventsByRangeParams{
			ViewerUserID:  actorID,
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

// allDayDateLayout is the wire form of an all-day date.
const allDayDateLayout = "2006-01-02"

// minAllDayDate is the first day an all-day date may name; the last is
// 9999-12-31, the end of what time.Parse reads.
var minAllDayDate = time.Date(1000, 1, 1, 0, 0, 0, 0, time.UTC)

// parseAllDayDate reads one startDate / endDate member as that day's UTC
// midnight, in unix seconds.
func parseAllDayDate(field string, raw *string) (*int64, error) {
	if raw == nil {
		return nil, nil
	}
	day, err := time.Parse(allDayDateLayout, *raw)
	if err != nil || day.Before(minAllDayDate) {
		return nil, invalidBodyField(field)
	}
	unix := day.Unix()
	return &unix, nil
}

// resolveAllDayDates turns the dates of an all-day write into the stored
// window, and refuses a window that is not the canonical shape. A member
// that is not sent comes from the stored row when fallback is valid;
// otherwise both must be sent. An end equal to the start is a milestone's
// and anything else not after it is DateRangeInvalid.
func resolveAllDayDates(kind string, startDate, endDate *string, fallbackStart, fallbackEnd sql.NullTime) (start, end *int64, err error) {
	if start, err = parseAllDayDate("startDate", startDate); err != nil {
		return nil, nil, err
	}
	if end, err = parseAllDayDate("endDate", endDate); err != nil {
		return nil, nil, err
	}
	if start == nil {
		if !fallbackStart.Valid {
			return nil, nil, invalidBodyField("startDate")
		}
		unix := fallbackStart.Time.Unix()
		start = &unix
	}
	if end == nil {
		if !fallbackEnd.Valid {
			return nil, nil, invalidBodyField("endDate")
		}
		unix := fallbackEnd.Time.Unix()
		end = &unix
	}
	window := func(unix int64) sql.NullTime {
		return sql.NullTime{Time: handlerutil.UnixToTime(unix), Valid: true}
	}
	if refusal := calendarrules.RequireAllDayWindow(true, kind, window(*start), window(*end)); refusal != nil {
		return nil, nil, handlerutil.HTTPErrFromAPIError(refusal)
	}
	return start, end, nil
}

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

// truncateToUTCDay returns midnight UTC on the calendar day t falls on
// in UTC.
func truncateToUTCDay(t time.Time) time.Time {
	return calendarrules.TruncateToUTCDay(t)
}

// resolvePatchAllDay applies the all-day rules to a PATCH body: an
// all-day row is named by dates and a timed one by instants, switching
// the form needs the new form's whole window, and a series that names
// occurrences cannot switch at all. The dates it accepts are written back
// to the body as the stored window, so everything after it sees one shape.
// wholeSeries is false when the patch reaches a single occurrence or a
// split, which have no stored window to fall back on for a missing end.
func resolvePatchAllDay(
	ctx context.Context,
	deps Deps,
	wsID uint32,
	evt calendar.FindCalendarEventByPublicIdRow,
	input *PatchEventInput,
	wholeSeries bool,
) error {
	body := &input.Body
	allDay := evt.AllDay
	toggled := body.AllDay != nil && *body.AllDay != evt.AllDay
	if toggled {
		allDay = *body.AllDay
		if calendaroccurrence.HasRule(evt.RecurrenceRule) {
			named, err := seriesNamesOccurrences(ctx, deps.CalendarQueries, wsID, evt)
			if err != nil {
				return httpErr(apierrors.CalendarEventStoreReadInterrupted)
			}
			if named {
				return httpErr(apierrors.CalendarEventAllDayToggleWithExceptions)
			}
		}
	}

	instants := body.StartAt != nil || body.EndAt != nil
	dates := body.StartDate != nil || body.EndDate != nil
	if refusal := calendarrules.RequireAllDayInput(allDay, instants, dates); refusal != nil {
		return handlerutil.HTTPErrFromAPIError(refusal)
	}
	newForm := instants
	if allDay {
		newForm = dates
	}
	if toggled && evt.StartAt.Valid && !newForm {
		return httpErr(apierrors.CalendarEventAllDayShape)
	}

	kind := string(evt.Kind)
	if body.Kind != nil {
		kind = *body.Kind
	}
	switch {
	case allDay && dates:
		var storedStart, storedEnd sql.NullTime
		if wholeSeries && !toggled {
			storedStart, storedEnd = evt.StartAt, evt.EndAt
		}
		start, end, err := resolveAllDayDates(kind, body.StartDate, body.EndDate, storedStart, storedEnd)
		if err != nil {
			return err
		}
		body.StartAt, body.EndAt = start, end
	case allDay && !toggled && body.Kind != nil && wholeSeries && evt.StartAt.Valid:
		// A kind change alone can turn the stored window into a wrong one,
		// a milestone being a point.
		if refusal := calendarrules.RequireAllDayWindow(true, kind, evt.StartAt, evt.EndAt); refusal != nil {
			return handlerutil.HTTPErrFromAPIError(refusal)
		}
	}
	return nil
}

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

		// Check edit permission (try to find attendee record for the actor).
		attendee, attErr := findActorAttendee(ctx, deps.CalendarQueries, actorID, evt.ID, evt.RecurrenceParentID)
		if attErr != nil {
			return nil, httpErr(apierrors.CalendarEventStoreReadInterrupted)
		}
		if !canEditEvent(actorID, evt, sub, attendee) {
			return nil, httpErr(apierrors.CalendarEventEditPermissionRequired)
		}
		patchViewer := func(row calendar.FindCalendarEventByPublicIdRow, att *calendar.FindCalendarEventAttendeeRow) eventViewer {
			return eventViewer{
				UserID:          actorID,
				IsAttendee:      att != nil,
				CalendarDefault: string(cal.DefaultEventVisibility),
				CanEdit:         canEditEvent(actorID, row, sub, att),
			}
		}

		// Pair invariant: explicit partial start_at without end_at (or
		// vice versa) is ambiguous. Undated transitions are supported,
		// but they must come through unlink first.
		if err := requireEventStartEndPair(input.Body.StartAt, input.Body.EndAt); err != nil {
			return nil, err
		}
		if err := requireEventChronology(input.Body.StartAt, input.Body.EndAt); err != nil {
			return nil, err
		}
		if err := requireDatetime("recurrenceEnd", input.Body.RecurrenceEnd); err != nil {
			return nil, err
		}
		if err := requireDatetime("occurrenceStart", input.Body.OccurrenceStart); err != nil {
			return nil, err
		}
		if err := requireNotificationOffset(input.Body.NotificationOffset); err != nil {
			return nil, err
		}
		if input.Body.Timezone != nil {
			if err := requireValidTimezone("timezone", *input.Body.Timezone); err != nil {
				return nil, err
			}
		}

		scope, err := decodeOccurrenceScope(input.Body.Scope)
		if err != nil {
			return nil, err
		}
		if err := resolvePatchAllDay(ctx, deps, wsID, evt, input, scope == scopeSeries); err != nil {
			return nil, err
		}
		// The parent link is read only when its answer can change the
		// outcome. A series-scoped patch that leaves the recurrence
		// columns alone reaches no refusal that depends on it, and asking
		// anyway would put a query on the common path for a question it
		// never has.
		var parentID sql.NullInt32
		if scope != scopeSeries || patchTouchesRecurrenceFields(input) {
			link, linkErr := deps.CalendarQueries.FindCalendarEventRecurrenceLink(ctx, calendar.FindCalendarEventRecurrenceLinkParams{
				WorkspaceID: wsID,
				PublicID:    evt.PublicID,
			})
			if linkErr != nil {
				return nil, httpErr(apierrors.CalendarEventStoreReadInterrupted)
			}
			parentID = link.RecurrenceParentID
		}
		if err := requireOccurrenceScope(scope, input, evt, parentID); err != nil {
			return nil, err
		}

		labelChange, err := resolvePatchLabel(ctx, deps, wsID, actorID, cal, evt, attendee, scope, input)
		if err != nil {
			return nil, err
		}

		if scope != scopeSeries {
			// The rule a "this and following" edit carries into the
			// continuing series is checked before any write, so a
			// malformed one is a validation error rather than a series
			// truncated for a remainder that could not be created.
			if spec := validateRecurrenceRule(input.Body.RecurrenceRule); spec != nil {
				return nil, httpErr(spec)
			}
			if spec := validateRecurrenceExceptions(input.Body.RecurrenceExceptions); spec != nil {
				return nil, httpErr(spec)
			}

			originalStart := handlerutil.UnixToTime(*input.Body.OccurrenceStart)
			var written calendar.FindCalendarEventByPublicIdRow
			var scopeErr error
			if scope == scopeOccurrence {
				written, scopeErr = patchEventOccurrence(ctx, deps, wsID, actorID, cal, evt, input, originalStart, labelChange)
			} else {
				written, scopeErr = patchEventFollowing(ctx, deps, wsID, actorID, cal, evt, input, originalStart, labelChange)
			}
			if scopeErr != nil {
				return nil, scopeErr
			}

			writtenAttendee, aerr := findActorAttendee(ctx, deps.CalendarQueries, actorID, written.ID, written.RecurrenceParentID)
			if aerr != nil {
				return nil, httpErr(apierrors.CalendarEventStoreReadInterrupted)
			}
			out := &PatchEventOutput{}
			out.Body = eventFromFullRow(written, patchViewer(written, writtenAttendee))
			recordOccurrencePatch(ctx, deps, wsID, actorID, input, scope, written, cal.ID)
			return out, nil
		}

		isLinked := evt.TaskID.Valid
		titleChanged := input.Body.Title != nil && *input.Body.Title != evt.Title
		timeChanged := input.Body.StartAt != nil // pair invariant guarantees EndAt also set

		// Resolved before the transaction so a refusal is answered as a
		// validation error rather than surfacing from inside a retryable
		// unit of work.
		var renameTitle taskrules.Title
		if isLinked && titleChanged {
			var terr error
			renameTitle, terr = requireLinkedTitle("title", *input.Body.Title)
			if terr != nil {
				return nil, terr
			}
		}

		// itemkit owns the propagation of a linked event's title and
		// times to the task, so those fields drop out of the direct PATCH
		// rather than being written twice. They are decided here rather
		// than by clearing input.Body inside the transaction: the
		// transaction is retried on a deadlock and would then re-read a
		// request body its own first attempt had emptied.
		patchTitle := input.Body.Title
		patchStartAt := input.Body.StartAt
		patchEndAt := input.Body.EndAt
		if isLinked && titleChanged {
			patchTitle = nil
		}
		if isLinked && timeChanged {
			patchStartAt = nil
			patchEndAt = nil
		}

		cleared, cerr := parseClearFields(input.Body.Clear)
		if cerr != nil {
			return nil, cerr
		}
		// The row as it will stand: what the request sends or clears over
		// what is stored. An unlinked event stays dated or undated here; a
		// linked one's times move through itemkit, which keeps it dated.
		shapeKind := string(evt.Kind)
		if input.Body.Kind != nil {
			shapeKind = *input.Body.Kind
		}
		shapeRecurring := calendaroccurrence.HasRule(evt.RecurrenceRule)
		switch {
		case cleared.recurrenceRule == 1:
			shapeRecurring = false
		case input.Body.RecurrenceRule != nil:
			shapeRecurring = calendaroccurrence.HasRule(*input.Body.RecurrenceRule)
		}
		shapeReminded := evt.NotificationOffset.Valid
		switch {
		case cleared.notificationOffset == 1:
			shapeReminded = false
		case input.Body.NotificationOffset != nil:
			shapeReminded = true
		}
		if err := requireEventShape(shapeKind, input.Body.StartAt != nil || evt.StartAt.Valid,
			shapeRecurring, shapeReminded); err != nil {
			return nil, err
		}
		params := calendar.PatchCalendarEventParams{
			PublicID:    types.FromUUID(evtUID),
			CalendarID:  cal.ID,
			WorkspaceID: wsID,

			ClearLocation:             cleared.location,
			ClearMemo:                 cleared.memo,
			ClearUrl:                  cleared.url,
			ClearBlockLabel:           cleared.blockLabel,
			ClearRecurrenceRule:       cleared.recurrenceRule,
			ClearRecurrenceEnd:        cleared.recurrenceEnd,
			ClearRecurrenceExceptions: cleared.recurrenceExceptions,
			ClearNotificationOffset:   cleared.notificationOffset,
			// sqlc emits one field per textual occurrence of a named
			// argument; clear_recurrence_rule appears in three SET
			// expressions because clearing the rule clears the two
			// columns that only describe a series.
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
		if input.Body.Flexibility != nil {
			params.Flexibility = calendar.NullCalendarEventsFlexibility{
				CalendarEventsFlexibility: calendar.CalendarEventsFlexibility(*input.Body.Flexibility),
				Valid:                     true,
			}
		}
		if patchTitle != nil {
			params.Title = sql.NullString{String: *patchTitle, Valid: true}
		}
		if input.Body.AllDay != nil {
			params.AllDay = sql.NullBool{Bool: *input.Body.AllDay, Valid: true}
		}
		if patchStartAt != nil {
			params.StartAt = sql.NullTime{Time: handlerutil.UnixToTime(*patchStartAt), Valid: true}
		}
		if patchEndAt != nil {
			params.EndAt = sql.NullTime{Time: handlerutil.UnixToTime(*patchEndAt), Valid: true}
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
			if spec := validateRecurrenceExceptions(input.Body.RecurrenceExceptions); spec != nil {
				return nil, httpErr(spec)
			}
			params.RecurrenceExceptions = json.RawMessage(*input.Body.RecurrenceExceptions)
		}
		if input.Body.NotificationOffset != nil {
			params.NotificationOffset = sql.NullInt32{Int32: *input.Body.NotificationOffset, Valid: true}
		}

		// A write that moves where a series' occurrences fall — its start,
		// zone, rule or bounds — takes the overrides and exceptions naming
		// those occurrences along with it, in the same transaction. The
		// exception list stays as sent when the caller sent one.
		realignSeries := calendaroccurrence.HasRule(evt.RecurrenceRule) &&
			((params.StartAt.Valid && !params.StartAt.Time.Equal(evt.StartAt.Time)) ||
				(input.Body.Timezone != nil && *input.Body.Timezone != evt.Timezone) ||
				input.Body.RecurrenceRule != nil || cleared.recurrenceRule == 1 ||
				input.Body.RecurrenceEnd != nil || cleared.recurrenceEnd == 1)
		keepExceptions := input.Body.RecurrenceExceptions != nil || cleared.recurrenceExceptions == 1

		// updated carries the post-write row out of the transaction; evt
		// stays the pre-write snapshot the decisions above were made from.
		updated := evt
		// answered holds a response-shaped error decided inside the
		// transaction, so an itemkit invariant is not reported as a
		// generic write failure.
		var answered error
		// labelChanged says whether the label link moved in the committed attempt.
		var labelChanged bool
		// renamed carries the linked task's text out of the transaction
		// so its search embedding is refreshed once the rename has
		// committed.
		var renamed itemkit.RenamedTask
		txErr := dbretry.InTx(ctx, deps.DB, "calendars.PatchEvent", nil, func(ctx context.Context, tx *dbretry.Tx) error {
			answered = nil
			labelChanged = false
			renamed = itemkit.RenamedTask{}
			cqtx := deps.CalendarQueries.WithTx(tx.RawTx())

			// Linked title change → itemkit.RenameItem (propagates to task + siblings)
			if isLinked && titleChanged {
				var renameErr error
				renamed, renameErr = itemkit.RenameItem(ctx, tx, itemkit.RenameItemArgs{
					WorkspaceID: wsID,
					ActorUserID: actorID,
					NewTitle:    renameTitle,
					EventID:     evt.ID,
				})
				if renameErr != nil {
					answered = translateItemkitError(ctx, "itemkit.RenameItem", renameErr)
					return renameErr
				}
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
					answered = translateItemkitError(ctx, "itemkit.RescheduleEvent", err)
					return err
				}
			}

			// The series columns as they stand before this write, read
			// under the row lock so the realignment below starts from what
			// is committed rather than from the snapshot taken before the
			// transaction.
			var before calendaroccurrence.SeriesState
			if realignSeries {
				locked, lerr := cqtx.LockCalendarEventSeries(ctx, calendar.LockCalendarEventSeriesParams{
					ID:          evt.ID,
					WorkspaceID: wsID,
				})
				if lerr != nil {
					answered = httpErr(apierrors.CalendarEventStoreReadInterrupted)
					return lerr
				}
				before = calendaroccurrence.SeriesStateOf(evt)
				before.Rule = locked.RecurrenceRule
				before.End = locked.RecurrenceEnd
				before.Exceptions = locked.RecurrenceExceptions
			}

			// Not an existence check: MySQL counts changed rows, so a PATCH
			// that re-sends the event's current values reports zero. The
			// event is re-read below and that read is what would fail.
			if _, err := cqtx.PatchCalendarEvent(ctx, params); err != nil {
				return err
			}

			if labelChange.Mode != calendarlabels.ChangeKeep {
				changed, refusal, lerr := calendarlabels.ApplyToSeries(ctx, cqtx, wsID, evt.ID, labelChange)
				if refusal != nil {
					answered = handlerutil.HTTPErrFromAPIError(refusal)
					return refusal
				}
				if lerr != nil {
					return lerr
				}
				labelChanged = changed
			}

			// Re-read inside the tx so the response reflects what itemkit
			// + sqlc jointly wrote.
			var rerr error
			updated, rerr = cqtx.FindCalendarEventByPublicId(ctx, calendar.FindCalendarEventByPublicIdParams{
				PublicID:    types.FromUUID(evtUID),
				CalendarID:  cal.ID,
				WorkspaceID: wsID,
			})
			if rerr != nil {
				answered = httpErr(apierrors.CalendarEventStoreReadInterrupted)
				return rerr
			}

			if realignSeries {
				exceptions, err := calendaroccurrence.RealignSeries(ctx, seriesQueries(deps, tx), wsID, before, updated, keepExceptions)
				if err != nil {
					return err
				}
				if exceptions != nil {
					updated.RecurrenceExceptions = exceptions
				}
			}
			return nil
		})
		if answered != nil {
			return nil, answered
		}
		if txErr != nil {
			return nil, httpErr(apierrors.CalendarEventStoreWriteInterrupted)
		}

		// A linked event's title is also the task's, so renaming through
		// the event moves the text search is served from. Past the commit:
		// the write is no longer revocable, which is the only point a
		// best-effort refresh can run without a failure here costing a
		// write the caller has already been told about.
		if renamed.TaskID != 0 {
			embed.RefreshTaskAfterCommit(ctx, deps.Embedder, wsID, renamed.TaskID, renamed.Title, renamed.Description)
		}

		out := &PatchEventOutput{}
		out.Body = eventFromFullRow(updated, patchViewer(updated, attendee))

		// A request whose only content is a label that is already in place
		// writes and records nothing.
		if labelChange.Mode != calendarlabels.ChangeKeep && !labelChanged && patchIsLabelOnly(input) {
			return out, nil
		}

		payload := map[string]any{
			"eventId":    input.EvtID,
			"calendarId": input.CalID,
		}
		// itemkit emits the update event for a title or time change; a
		// label change on its own has to record it here.
		itemkitEmitted := isLinked && (titleChanged || timeChanged)
		if isLinked && !(labelChanged && !itemkitEmitted) {
			// itemkit fired item.rescheduled / item.renamed inside the
			// transaction above and dual-emitted the legacy
			// calendar.event.updated kind with it, so the event row is
			// already there; appending a second one would show the edit
			// on the timeline twice.
			recordCalendarAudit(ctx, deps, wsID, actorID, mutationlog.Mutation{
				AuditAction:  "calendar.event.update",
				ResourceType: "calendar.event",
				ResourceID:   input.EvtID,
				Payload:      payload,
				CallSite:     "calendars.PatchEvent",
			})
		} else {
			// An unlinked event has no itemkit write behind it (nor does a
			// linked event's label-only change), so the existing kind is
			// emitted here to keep webhook subscribers working.
			recordCalendarEventChange(ctx, deps, wsID, cal.ID, actorID, updated.PublicID, updated.Title, mutationlog.Mutation{
				EventType:    eventbus.CalEventUpdated,
				AuditAction:  "calendar.event.update",
				ResourceType: "calendar.event",
				ResourceID:   input.EvtID,
				Payload:      payload,
				CallSite:     "calendars.PatchEvent",
			})
		}

		return out, nil
	}
}

// patchIsLabelOnly reports whether the request carries nothing but a label
// change: no other member, and no clear entry other than labelId.
func patchIsLabelOnly(input *PatchEventInput) bool {
	body := input.Body
	body.LabelID = nil
	body.Scope = nil
	body.OccurrenceStart = nil
	body.Clear = nil
	for _, name := range input.Body.Clear {
		if name != "labelId" {
			return false
		}
	}
	return reflect.ValueOf(body).IsZero()
}

// patchTouchesDetails reports whether the request writes or clears location,
// memo or url.
func patchTouchesDetails(input *PatchEventInput) bool {
	body := input.Body
	return body.Location != nil || body.Memo != nil || body.URL != nil ||
		slices.ContainsFunc(body.Clear, func(name string) bool {
			return name == "location" || name == "memo" || name == "url"
		})
}

// resolvePatchLabel enforces the details-visibility rule and turns the
// request's label members into a Change, in the order the MCP tools use: a
// write to location, memo, url or the label needs a viewer who can see the
// target row's details, then the label must belong to the calendar. Clear
// wins over a value. The target is the override for an occurrence scope when
// one exists, else the series.
func resolvePatchLabel(
	ctx context.Context,
	deps Deps,
	wsID, actorID uint32,
	cal calendar.FindCalendarByPublicIdRow,
	evt calendar.FindCalendarEventByPublicIdRow,
	evtAttendee *calendar.FindCalendarEventAttendeeRow,
	scope occurrenceScope,
	input *PatchEventInput,
) (calendarlabels.Change, error) {
	clearLabel := slices.Contains(input.Body.Clear, "labelId")
	if input.Body.LabelID == nil && !clearLabel && !patchTouchesDetails(input) {
		return calendarlabels.Change{}, nil
	}
	target, targetAttendee := evt, evtAttendee
	if scope == scopeOccurrence {
		ov, err := deps.CalendarQueries.FindCalendarEventOverride(ctx, calendar.FindCalendarEventOverrideParams{
			WorkspaceID:             wsID,
			RecurrenceParentID:      handlerutil.NullInt32From(evt.ID),
			RecurrenceOriginalStart: sql.NullTime{Time: handlerutil.UnixToTime(*input.Body.OccurrenceStart), Valid: true},
		})
		switch {
		case err == nil:
			target, err = deps.CalendarQueries.FindCalendarEventByPublicId(ctx, calendar.FindCalendarEventByPublicIdParams{
				PublicID:    ov.PublicID,
				CalendarID:  ov.CalendarID,
				WorkspaceID: wsID,
			})
			if err == nil {
				targetAttendee, err = findActorAttendee(ctx, deps.CalendarQueries, actorID, target.ID, target.RecurrenceParentID)
			}
			if err != nil {
				return calendarlabels.Change{}, httpErr(apierrors.CalendarEventStoreReadInterrupted)
			}
		case !errors.Is(err, sql.ErrNoRows):
			return calendarlabels.Change{}, httpErr(apierrors.CalendarEventStoreReadInterrupted)
		}
	}
	visible := eventacl.CanSeeDetails(
		eventacl.Event{
			Visibility:      eventacl.Visibility(target.Visibility),
			OwnerUserID:     target.OwnerUserID,
			CreatedByUserID: target.CreatedByUserID,
			CalendarDefault: eventacl.Visibility(cal.DefaultEventVisibility),
		},
		eventacl.Actor{UserID: actorID, IsAttendee: targetAttendee != nil},
	)
	if refusal := calendarrules.RequireDetailsVisible(visible); refusal != nil {
		return calendarlabels.Change{}, handlerutil.HTTPErrFromAPIError(refusal)
	}
	if input.Body.LabelID == nil && !clearLabel {
		return calendarlabels.Change{}, nil
	}
	if clearLabel {
		return calendarlabels.Change{Mode: calendarlabels.ChangeClear}, nil
	}
	id, refusal, err := calendarlabels.ResolveLabel(ctx, deps.CalendarQueries, cal.ID, *input.Body.LabelID)
	if err != nil {
		return calendarlabels.Change{}, httpErr(apierrors.CalendarEventStoreReadInterrupted)
	}
	if refusal != nil {
		return calendarlabels.Change{}, handlerutil.HTTPErrFromAPIError(refusal)
	}
	return calendarlabels.Change{Mode: calendarlabels.ChangeSet, LabelID: id}, nil
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
			ViewerUserID: actorID,
			UserID:       actorID,
			WorkspaceID:  wsID,
			RangeEnd:     sql.NullTime{Time: endTime, Valid: true},
			RangeStart:   sql.NullTime{Time: startTime, Valid: true},
		})
		if err != nil {
			return nil, httpErr(apierrors.CalendarEventListQueryInterrupted)
		}

		// Recurring events whose recurrence window overlaps the query range
		recurringRows, err := deps.CalendarQueries.ListRecurringCalendarEventsAcrossCalendars(ctx, calendar.ListRecurringCalendarEventsAcrossCalendarsParams{
			ViewerUserID:  actorID,
			UserID:        actorID,
			WorkspaceID:   wsID,
			StartAt:       sql.NullTime{Time: endTime, Valid: true},
			RecurrenceEnd: sql.NullTime{Time: startTime, Valid: true},
		})
		if err != nil {
			return nil, httpErr(apierrors.CalendarEventListQueryInterrupted)
		}

		// One batched read for the whole page. Only a row carrying a rule
		// can be overridden — an override names its master, never another
		// override — so the ids come from the recurring result alone and
		// the non-recurring rows below never carry the field.
		masterIDs := make([]uint32, 0, len(recurringRows))
		for _, r := range recurringRows {
			masterIDs = append(masterIDs, r.ID)
		}
		overridden, err := overriddenStartsByMaster(ctx, deps.CalendarQueries, wsID, actorID, masterIDs)
		if err != nil {
			return nil, httpErr(apierrors.CalendarEventListQueryInterrupted)
		}

		out := &ListCalendarEventsOutput{}
		out.Body.Events = make([]CrossCalendarEventResponse, 0, len(rows)+len(recurringRows))

		for _, r := range rows {
			resp := CrossCalendarEventResponse{
				ID:          r.PublicID.String(),
				CalendarID:  r.CalendarPublicID.String(),
				Kind:        string(r.Kind),
				Visibility:  string(r.Visibility),
				ShowAs:      string(r.ShowAs),
				Flexibility: string(r.Flexibility),
				Title:       r.Title,
				AllDay:      r.AllDay,
				StartAt:     nullTimeUnixPtr(r.StartAt),
				EndAt:       nullTimeUnixPtr(r.EndAt),
				Timezone:    r.Timezone,
				CreatedAt:   r.CreatedAt.Unix(),
			}
			resp.Location = dbtype.PtrFromNullString(r.Location)
			resp.BlockLabel = dbtype.PtrFromNullString(r.BlockLabel)
			resp.UpdatedAt = dbtype.UnixSecondsFromNullTime(r.UpdatedAt)
			visible := scrubEventDetailsForParty(string(r.Visibility), string(r.CalendarDefaultVisibility), r.OwnerUserID, r.CreatedByUserID, actorID, r.IsAttendee, &resp.Location, nil, nil)
			c := resolveRowColor(visible, r.LabelPublicID, r.LabelName, r.LabelColor, r.OwnerMemberColor)
			resp.Color, resp.OwnerColor, resp.LabelID, resp.LabelName, resp.DetailsHidden = c.Color, c.OwnerColor, labelIDString(c), c.LabelName, c.DetailsHidden
			out.Body.Events = append(out.Body.Events, resp)
		}

		for _, r := range recurringRows {
			resp := CrossCalendarEventResponse{
				ID:          r.PublicID.String(),
				CalendarID:  r.CalendarPublicID.String(),
				Kind:        string(r.Kind),
				Visibility:  string(r.Visibility),
				ShowAs:      string(r.ShowAs),
				Flexibility: string(r.Flexibility),
				Title:       r.Title,
				AllDay:      r.AllDay,
				StartAt:     nullTimeUnixPtr(r.StartAt),
				EndAt:       nullTimeUnixPtr(r.EndAt),
				Timezone:    r.Timezone,
				CreatedAt:   r.CreatedAt.Unix(),
			}
			resp.Location = dbtype.PtrFromNullString(r.Location)
			resp.BlockLabel = dbtype.PtrFromNullString(r.BlockLabel)
			if r.RecurrenceRule != nil {
				raw := json.RawMessage(r.RecurrenceRule)
				resp.RecurrenceRule = &raw
				resp.OverriddenStarts = overridden[r.ID]
			}
			resp.RecurrenceEnd = dbtype.UnixSecondsFromNullTime(r.RecurrenceEnd)
			if r.RecurrenceExceptions != nil {
				raw := json.RawMessage(r.RecurrenceExceptions)
				resp.RecurrenceExceptions = &raw
			}
			resp.UpdatedAt = dbtype.UnixSecondsFromNullTime(r.UpdatedAt)
			visible := scrubEventDetailsForParty(string(r.Visibility), string(r.CalendarDefaultVisibility), r.OwnerUserID, r.CreatedByUserID, actorID, r.IsAttendee, &resp.Location, nil, nil)
			c := resolveRowColor(visible, r.LabelPublicID, r.LabelName, r.LabelColor, r.OwnerMemberColor)
			resp.Color, resp.OwnerColor, resp.LabelID, resp.LabelName, resp.DetailsHidden = c.Color, c.OwnerColor, labelIDString(c), c.LabelName, c.DetailsHidden
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
