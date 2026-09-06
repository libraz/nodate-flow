package calendars

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/libraz/nodate-flow/apps/flow-api/internal/ai/embed"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/audit"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/calendarrules"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/dbretry"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/generated/calendar"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/types"
	apierrors "github.com/libraz/nodate-flow/apps/flow-api/internal/errors"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/eventbus"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/http/handlers/handlerutil"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/itemkit"
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
	WsID  string `path:"wsId" doc:"Workspace public ID"`
	CalID string `path:"calId" doc:"Calendar public ID"`
	Body  struct {
		Kind               string           `json:"kind" enum:"event,block,free,milestone" doc:"Event kind"`
		Visibility         string           `json:"visibility,omitempty" required:"false" enum:"default,public,private,confidential" doc:"Visibility"`
		ShowAs             string           `json:"showAs,omitempty" required:"false" enum:"busy,free,tentative,oof" doc:"Show-as status"`
		Flexibility        string           `json:"flexibility,omitempty" required:"false" enum:"fixed,negotiable,conditional" doc:"Whether the commitment can be moved; independent of showAs, which only says whether the time reads as taken"`
		Title              string           `json:"title" minLength:"1" maxLength:"500" doc:"Event title"`
		AllDay             bool             `json:"allDay" required:"false" doc:"All-day event flag"`
		StartAt            *int64           `json:"startAt,omitempty" required:"false" doc:"Start time as unix seconds (UTC); omit for a planning-stage (undated) event"`
		EndAt              *int64           `json:"endAt,omitempty" required:"false" doc:"End time as unix seconds (UTC); omit for a planning-stage (undated) event"`
		Timezone           string           `json:"timezone" maxLength:"64" doc:"IANA timezone"`
		Location           *string          `json:"location,omitempty" required:"false" maxLength:"500" doc:"Location"`
		Memo               *string          `json:"memo,omitempty" required:"false" maxLength:"10000" doc:"Memo / notes"`
		URL                *string          `json:"url,omitempty" required:"false" maxLength:"2048" doc:"Related URL"`
		OwnerUserID        *string          `json:"ownerUserId,omitempty" required:"false" doc:"Owner user public ID (defaults to actor)"`
		BlockLabel         *string          `json:"blockLabel,omitempty" required:"false" maxLength:"100" doc:"Block label"`
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
		StartAt              *int64           `json:"startAt,omitempty" required:"false" doc:"Start time as unix seconds (UTC)"`
		EndAt                *int64           `json:"endAt,omitempty" required:"false" doc:"End time as unix seconds (UTC)"`
		Timezone             *string          `json:"timezone,omitempty" required:"false" maxLength:"64" doc:"IANA timezone"`
		Location             *string          `json:"location,omitempty" required:"false" maxLength:"500" doc:"Location"`
		Memo                 *string          `json:"memo,omitempty" required:"false" maxLength:"10000" doc:"Memo"`
		URL                  *string          `json:"url,omitempty" required:"false" maxLength:"2048" doc:"Related URL"`
		BlockLabel           *string          `json:"blockLabel,omitempty" required:"false" maxLength:"100" doc:"Block label"`
		RecurrenceRule       *json.RawMessage `json:"recurrenceRule,omitempty" required:"false" doc:"Recurrence rule"`
		RecurrenceEnd        *int64           `json:"recurrenceEnd,omitempty" required:"false" doc:"Recurrence end as unix seconds (UTC)"`
		RecurrenceExceptions *json.RawMessage `json:"recurrenceExceptions,omitempty" required:"false" doc:"Array of ISO 8601 dates/times to exclude from recurrence"`
		NotificationOffset   *int32           `json:"notificationOffset,omitempty" required:"false" doc:"Notification offset"`
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
		Clear []string `json:"clear,omitempty" required:"false" enum:"location,memo,url,blockLabel,recurrenceRule,recurrenceEnd,recurrenceExceptions,notificationOffset" doc:"Nullable fields to clear. Takes precedence over a value sent for the same field in this request."`
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

// normalizeAllDayBounds pins an all-day event's stored instants to UTC
// midnight.
//
// The rule lives in calendarrules because the MCP tools write the same
// column pair: an all-day row that two transports encode differently is
// a row whose date depends on which client wrote it, which is the defect
// the shared function describes.
func normalizeAllDayBounds(allDay bool, start, end sql.NullTime) (sql.NullTime, sql.NullTime) {
	return calendarrules.NormalizeAllDayBounds(allDay, start, end)
}

// truncateToUTCDay returns midnight UTC on the calendar day t falls on
// in UTC.
func truncateToUTCDay(t time.Time) time.Time {
	return calendarrules.TruncateToUTCDay(t)
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
		if err := requireEventStartEndPair(input.Body.StartAt, input.Body.EndAt); err != nil {
			return nil, err
		}
		if err := requireEventChronology(input.Body.StartAt, input.Body.EndAt); err != nil {
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
		if input.Body.StartAt != nil {
			startAtNT = sql.NullTime{Time: handlerutil.UnixToTime(*input.Body.StartAt), Valid: true}
			endAtNT = sql.NullTime{Time: handlerutil.UnixToTime(*input.Body.EndAt), Valid: true}
		}
		startAtNT, endAtNT = normalizeAllDayBounds(input.Body.AllDay, startAtNT, endAtNT)
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
		appendCalendarEvent(ctx, dbretry.AutoCommit(deps.DB), wsID, cal.ID, eventbus.CalEventCreated, &actorID, eventBusPayload, "calendars.CreateEvent")

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

		// Attendance decides whether a private event's room and link are
		// this viewer's to read, so it is resolved before either check.
		// One lookup is affordable here; the list paths get the same
		// answer from the is_attendee column instead of a query per row.
		isAttendee := false
		if _, aerr := deps.CalendarQueries.FindCalendarEventAttendee(ctx, calendar.FindCalendarEventAttendeeParams{
			EventID: handlerutil.NullInt32From(evt.ID),
			UserID:  actorID,
		}); aerr == nil {
			isAttendee = true
		} else if !errors.Is(aerr, sql.ErrNoRows) {
			return nil, httpErr(apierrors.CalendarEventStoreReadInterrupted)
		}

		aclEvent := eventacl.Event{
			Visibility:      eventacl.Visibility(evt.Visibility),
			OwnerUserID:     evt.OwnerUserID,
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

		resp := eventFromFullRow(evt)
		scrubEventDetails(string(evt.Visibility), string(cal.DefaultEventVisibility),
			evt.OwnerUserID, actorID, isAttendee,
			&resp.Location, &resp.Memo, &resp.URL)

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
		if err := requireEventStartEndPair(input.Body.StartAt, input.Body.EndAt); err != nil {
			return nil, err
		}
		if err := requireEventChronology(input.Body.StartAt, input.Body.EndAt); err != nil {
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
				written, scopeErr = patchEventOccurrence(ctx, deps, wsID, actorID, cal, evt, input, originalStart)
			} else {
				written, scopeErr = patchEventFollowing(ctx, deps, wsID, actorID, cal, evt, input, originalStart)
			}
			if scopeErr != nil {
				return nil, scopeErr
			}

			out := &PatchEventOutput{}
			out.Body = eventFromFullRow(written)
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
			ClearRecurrenceRule_2: cleared.recurrenceRule,
			ClearRecurrenceRule_3: cleared.recurrenceRule,
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

		// The all-day flag and the times can move in separate requests,
		// so the normalisation reads whichever value the row will end up
		// with: the one being set now, or the one already stored. A
		// patch that only flips allDay to true still has to pin the
		// existing instants, or the row keeps the wall-clock times it
		// had and reads as a different day for half the workspace.
		effectiveAllDay := evt.AllDay
		if input.Body.AllDay != nil {
			effectiveAllDay = *input.Body.AllDay
		}
		if effectiveAllDay {
			if !params.StartAt.Valid && evt.StartAt.Valid {
				params.StartAt = evt.StartAt
			}
			if !params.EndAt.Valid && evt.EndAt.Valid {
				params.EndAt = evt.EndAt
			}
			params.StartAt, params.EndAt = normalizeAllDayBounds(true, params.StartAt, params.EndAt)
		}

		// updated carries the post-write row out of the transaction; evt
		// stays the pre-write snapshot the decisions above were made from.
		updated := evt
		// answered holds a response-shaped error decided inside the
		// transaction, so an itemkit invariant is not reported as a
		// generic write failure.
		var answered error
		// renamed carries the linked task's text out of the transaction
		// so its search embedding is refreshed once the rename has
		// committed.
		var renamed itemkit.RenamedTask
		txErr := dbretry.InTx(ctx, deps.DB, "calendars.PatchEvent", nil, func(ctx context.Context, tx *dbretry.Tx) error {
			answered = nil
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

			// Not an existence check: MySQL counts changed rows, so a PATCH
			// that re-sends the event's current values reports zero. The
			// event is re-read below and that read is what would fail.
			if _, err := cqtx.PatchCalendarEvent(ctx, params); err != nil {
				return err
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
			}
			return rerr
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
		out.Body = eventFromFullRow(updated)

		// When itemkit fired item.rescheduled / item.renamed it already
		// dual-emitted the legacy calendar.event.updated kind, so no
		// extra append is needed for linked events. For unlinked events
		// we preserve the existing kind to keep webhook subscribers
		// working.
		if !isLinked {
			appendCalendarEvent(ctx, dbretry.AutoCommit(deps.DB), wsID, cal.ID, eventbus.CalEventUpdated, &actorID, map[string]any{
				"eventId":    input.EvtID,
				"calendarId": input.CalID,
			}, "calendars.PatchEvent")
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

		if scope != scopeSeries {
			occurrenceStart := handlerutil.UnixToTime(input.OccurrenceStart)
			var partErr error
			if scope == scopeOccurrence {
				partErr = deleteEventOccurrence(ctx, deps, wsID, cal, evt, occurrenceStart)
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
			scrubEventDetails(string(r.Visibility), string(r.CalendarDefaultVisibility), r.OwnerUserID, actorID, r.IsAttendee, &resp.Location, nil, nil)
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
			scrubEventDetails(string(r.Visibility), string(r.CalendarDefaultVisibility), r.OwnerUserID, actorID, r.IsAttendee, &resp.Location, nil, nil)
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
