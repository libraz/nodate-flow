package calendars

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"time"

	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/generated/calendar"
	apierrors "github.com/libraz/nodate-flow/apps/flow-api/internal/errors"
	"github.com/libraz/nodate-flow/packages/go-shared/dbtype"
)

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

// ListEventsOutput is the response for the list events endpoint.
type ListEventsOutput struct {
	Body struct {
		Events []EventResponse `json:"events"`
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
