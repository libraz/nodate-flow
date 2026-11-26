package calendars

import (
	"database/sql"
	"encoding/json"

	"github.com/libraz/nodate-flow/apps/flow-api/internal/calendarlabels"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/generated/calendar"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/types"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/http/handlers/handlerutil"
	"github.com/libraz/nodate-flow/packages/go-shared/dbtype"
	"github.com/libraz/nodate-flow/packages/go-shared/eventacl"
)

// scrubEventDetailsForParty redacts an event's free-text fields from a viewer
// who may see that the time is taken but not what fills it.
//
// The decision itself is eventacl.CanSeeDetails, so REST and MCP answer
// it the same way; this function only applies the answer to whichever
// of the three fields the calling DTO carries, passing nil for the rest.
//
// isAttendee comes from the is_attendee column the list queries project
// (eventacl.AttendeeExistsSQL) or from a direct lookup on the detail
// path. It is the field that makes `private` usable: whoever was invited
// to the meeting needs the room and the call link, and a rule that
// excepted only the owner withheld exactly those from the people the
// event was for.
//
// Rows the viewer may not see at all never reach here — that is a
// row-level decision and it belongs in the query, because dropping rows
// afterwards would leave any total counting events the viewer is not
// supposed to know about.
//
// createdByUserID is the row's creator, a party to the event beside its owner
// and attendees; zero treats the creator as any member.
//
// It returns the same decision (detailsVisible) so colour resolution
// shares the one answer that decided the scrubbing.
func scrubEventDetailsForParty(visibility string, calendarDefault string, ownerUserID, createdByUserID, viewerUserID uint32, isAttendee bool, location, memo, url **string) (detailsVisible bool) {
	if eventacl.CanSeeDetails(
		eventacl.Event{
			Visibility:      eventacl.Visibility(visibility),
			OwnerUserID:     ownerUserID,
			CreatedByUserID: createdByUserID,
			CalendarDefault: eventacl.Visibility(calendarDefault),
		},
		eventacl.Actor{UserID: viewerUserID, IsAttendee: isAttendee},
	) {
		return true
	}
	if location != nil {
		*location = nil
	}
	if memo != nil {
		*memo = nil
	}
	if url != nil {
		*url = nil
	}
	return false
}

// resolveRowColor resolves an event row's colour from the label and owner
// member colour columns the event queries project.
func resolveRowColor(detailsVisible bool, labelPublicID types.PublicID, labelName, labelColor, ownerMemberColor sql.NullString) calendarlabels.EventColor {
	return calendarlabels.ResolveEventColor(detailsVisible, labelPublicID, labelName, labelColor, ownerMemberColor)
}

// labelIDString renders the resolved label's public id, nil when unlabelled
// or hidden. Shared by every response type that carries the colour fields.
func labelIDString(c calendarlabels.EventColor) *string {
	if c.LabelID == nil {
		return nil
	}
	s := c.LabelID.String()
	return &s
}

// applyEventColor copies a resolved colour onto an EventResponse.
func applyEventColor(resp *EventResponse, c calendarlabels.EventColor) {
	resp.Color = c.Color
	resp.OwnerColor = c.OwnerColor
	resp.LabelID = labelIDString(c)
	resp.LabelName = c.LabelName
	resp.DetailsHidden = c.DetailsHidden
}

// eventViewer is what eventFromFullRow needs to know about the reader.
// CanEdit is computed by the caller (canEditEvent) so the mapper does no I/O.
type eventViewer struct {
	UserID          uint32
	IsAttendee      bool
	CalendarDefault string
	CanEdit         bool
}

// eventCommon captures the columns shared between every calendar_events
// query shape (full row, range row, recurring-range row). The mappers
// in this file pull the common columns into a baseline EventResponse so
// each call site stays focused on the row-specific fields it adds.
//
// Defined as an internal interface rather than a struct because sqlc
// generates distinct named types per query — we cannot embed a single
// struct, but a tiny accessor interface keeps the surface uniform.
type eventCommon interface {
	publicIDString() string
	kindString() string
	visibilityString() string
	showAsString() string
	flexibilityString() string
	title() string
	allDay() bool
	startAt() *int64
	endAt() *int64
	timezone() string
	createdAt() int64
	creatorID() string
	creatorDisplayName() string
	creatorAvatarURL() *string
}

// baseEventResponse populates the columns that every event row carries.
// Row-specific fields (RecurrenceRule, NotificationOffset, etc.) are
// filled in by the per-row mapper after this baseline is built.
func baseEventResponse(c eventCommon) EventResponse {
	return EventResponse{
		ID:          c.publicIDString(),
		Kind:        c.kindString(),
		Visibility:  c.visibilityString(),
		ShowAs:      c.showAsString(),
		Flexibility: c.flexibilityString(),
		Title:       c.title(),
		AllDay:      c.allDay(),
		StartAt:     c.startAt(),
		EndAt:       c.endAt(),
		Timezone:    c.timezone(),
		CreatedAt:   c.createdAt(),

		CreatorID:          c.creatorID(),
		CreatorDisplayName: c.creatorDisplayName(),
		CreatorAvatarURL:   c.creatorAvatarURL(),
	}
}

// creatorPublicIDString renders a creator or owner public_id to its UUID
// string, returning "" for the zero value. Both joins are LEFT JOINs, so a
// hard-deleted user (CASCADE normally prevents this) and a suspended one
// scan as the zero PublicID; we suppress that rather than emit a zero UUID.
// The event stays in the response either way — an event belongs to the
// calendar its members share, not to the account that owns the row.
func creatorPublicIDString(id types.PublicID) string {
	if id == (types.PublicID{}) {
		return ""
	}
	return id.String()
}

// rangeEvent adapts ListCalendarEventsByRangeRow to eventCommon.
type rangeEvent struct {
	r calendar.ListCalendarEventsByRangeRow
}

func (e rangeEvent) publicIDString() string     { return e.r.PublicID.String() }
func (e rangeEvent) kindString() string         { return string(e.r.Kind) }
func (e rangeEvent) visibilityString() string   { return string(e.r.Visibility) }
func (e rangeEvent) showAsString() string       { return string(e.r.ShowAs) }
func (e rangeEvent) flexibilityString() string  { return string(e.r.Flexibility) }
func (e rangeEvent) title() string              { return e.r.Title }
func (e rangeEvent) allDay() bool               { return e.r.AllDay }
func (e rangeEvent) startAt() *int64            { return nullTimeUnixPtr(e.r.StartAt) }
func (e rangeEvent) endAt() *int64              { return nullTimeUnixPtr(e.r.EndAt) }
func (e rangeEvent) timezone() string           { return e.r.Timezone }
func (e rangeEvent) createdAt() int64           { return handlerutil.TimeToUnix(e.r.CreatedAt) }
func (e rangeEvent) creatorID() string          { return creatorPublicIDString(e.r.CreatorPublicID) }
func (e rangeEvent) creatorDisplayName() string { return e.r.CreatorDisplayName.String }
func (e rangeEvent) creatorAvatarURL() *string {
	return handlerutil.AvatarURLPtr(e.r.CreatorAvatarUrl, e.r.CreatorAvatarStorageObjectID, e.r.CreatorPublicID)
}

// recurringEvent adapts ListRecurringCalendarEventsByRangeRow.
type recurringEvent struct {
	r calendar.ListRecurringCalendarEventsByRangeRow
}

func (e recurringEvent) publicIDString() string    { return e.r.PublicID.String() }
func (e recurringEvent) kindString() string        { return string(e.r.Kind) }
func (e recurringEvent) visibilityString() string  { return string(e.r.Visibility) }
func (e recurringEvent) showAsString() string      { return string(e.r.ShowAs) }
func (e recurringEvent) flexibilityString() string { return string(e.r.Flexibility) }
func (e recurringEvent) title() string             { return e.r.Title }
func (e recurringEvent) allDay() bool              { return e.r.AllDay }
func (e recurringEvent) startAt() *int64           { return nullTimeUnixPtr(e.r.StartAt) }
func (e recurringEvent) endAt() *int64             { return nullTimeUnixPtr(e.r.EndAt) }
func (e recurringEvent) timezone() string          { return e.r.Timezone }
func (e recurringEvent) createdAt() int64          { return handlerutil.TimeToUnix(e.r.CreatedAt) }
func (e recurringEvent) creatorID() string         { return creatorPublicIDString(e.r.CreatorPublicID) }
func (e recurringEvent) creatorDisplayName() string {
	return e.r.CreatorDisplayName.String
}
func (e recurringEvent) creatorAvatarURL() *string {
	return handlerutil.AvatarURLPtr(e.r.CreatorAvatarUrl, e.r.CreatorAvatarStorageObjectID, e.r.CreatorPublicID)
}

// fullEvent adapts FindCalendarEventByPublicIdRow.
type fullEvent struct {
	r calendar.FindCalendarEventByPublicIdRow
}

func (e fullEvent) publicIDString() string     { return e.r.PublicID.String() }
func (e fullEvent) kindString() string         { return string(e.r.Kind) }
func (e fullEvent) visibilityString() string   { return string(e.r.Visibility) }
func (e fullEvent) showAsString() string       { return string(e.r.ShowAs) }
func (e fullEvent) flexibilityString() string  { return string(e.r.Flexibility) }
func (e fullEvent) title() string              { return e.r.Title }
func (e fullEvent) allDay() bool               { return e.r.AllDay }
func (e fullEvent) startAt() *int64            { return nullTimeUnixPtr(e.r.StartAt) }
func (e fullEvent) endAt() *int64              { return nullTimeUnixPtr(e.r.EndAt) }
func (e fullEvent) timezone() string           { return e.r.Timezone }
func (e fullEvent) createdAt() int64           { return handlerutil.TimeToUnix(e.r.CreatedAt) }
func (e fullEvent) creatorID() string          { return creatorPublicIDString(e.r.CreatorPublicID) }
func (e fullEvent) creatorDisplayName() string { return e.r.CreatorDisplayName.String }
func (e fullEvent) creatorAvatarURL() *string {
	return handlerutil.AvatarURLPtr(e.r.CreatorAvatarUrl, e.r.CreatorAvatarStorageObjectID, e.r.CreatorPublicID)
}

// eventFromRangeRow converts a ListCalendarEventsByRange row into the
// public EventResponse DTO. Used by the per-calendar list endpoint.
func eventFromRangeRow(r calendar.ListCalendarEventsByRangeRow, viewerUserID uint32) EventResponse {
	resp := baseEventResponse(rangeEvent{r})
	resp.Location = dbtype.PtrFromNullString(r.Location)
	resp.Memo = dbtype.PtrFromNullString(r.Memo)
	resp.URL = dbtype.PtrFromNullString(r.Url)
	resp.BlockLabel = dbtype.PtrFromNullString(r.BlockLabel)
	resp.NotificationOffset = dbtype.PtrFromNullInt32(r.NotificationOffset)
	resp.UpdatedAt = dbtype.UnixSecondsFromNullTime(r.UpdatedAt)
	visible := scrubEventDetailsForParty(string(r.Visibility), string(r.CalendarDefaultVisibility), r.OwnerUserID, r.CreatedByUserID, viewerUserID, r.IsAttendee, &resp.Location, &resp.Memo, &resp.URL)
	applyEventColor(&resp, resolveRowColor(visible, r.LabelPublicID, r.LabelName, r.LabelColor, r.OwnerMemberColor))
	return resp
}

// eventFromRecurringRow converts a ListRecurringCalendarEventsByRange
// row into the public EventResponse DTO, attaching the recurrence rule
// + exception list so the client can expand instances locally.
func eventFromRecurringRow(r calendar.ListRecurringCalendarEventsByRangeRow, viewerUserID uint32) EventResponse {
	resp := baseEventResponse(recurringEvent{r})
	resp.Location = dbtype.PtrFromNullString(r.Location)
	resp.Memo = dbtype.PtrFromNullString(r.Memo)
	resp.URL = dbtype.PtrFromNullString(r.Url)
	resp.BlockLabel = dbtype.PtrFromNullString(r.BlockLabel)
	visible := scrubEventDetailsForParty(string(r.Visibility), string(r.CalendarDefaultVisibility), r.OwnerUserID, r.CreatedByUserID, viewerUserID, r.IsAttendee, &resp.Location, &resp.Memo, &resp.URL)
	applyEventColor(&resp, resolveRowColor(visible, r.LabelPublicID, r.LabelName, r.LabelColor, r.OwnerMemberColor))
	if r.RecurrenceRule != nil {
		raw := json.RawMessage(r.RecurrenceRule)
		resp.RecurrenceRule = &raw
	}
	resp.RecurrenceEnd = dbtype.UnixSecondsFromNullTime(r.RecurrenceEnd)
	if r.RecurrenceExceptions != nil {
		raw := json.RawMessage(r.RecurrenceExceptions)
		resp.RecurrenceExceptions = &raw
	}
	resp.NotificationOffset = dbtype.PtrFromNullInt32(r.NotificationOffset)
	resp.UpdatedAt = dbtype.UnixSecondsFromNullTime(r.UpdatedAt)
	return resp
}

// eventFromFullRow converts a FindCalendarEventByPublicId row into the
// public EventResponse DTO. Used by Get + Patch handlers after the row
// has been read inside the same transaction so the response reflects
// what was just persisted. It scrubs for the viewer, resolves the colour from
// the same visibility decision and fills CanEdit.
func eventFromFullRow(r calendar.FindCalendarEventByPublicIdRow, v eventViewer) EventResponse {
	resp := baseEventResponse(fullEvent{r})
	resp.Location = dbtype.PtrFromNullString(r.Location)
	resp.Memo = dbtype.PtrFromNullString(r.Memo)
	resp.URL = dbtype.PtrFromNullString(r.Url)
	resp.BlockLabel = dbtype.PtrFromNullString(r.BlockLabel)
	visible := scrubEventDetailsForParty(string(r.Visibility), v.CalendarDefault, r.OwnerUserID, r.CreatedByUserID, v.UserID, v.IsAttendee, &resp.Location, &resp.Memo, &resp.URL)
	applyEventColor(&resp, resolveRowColor(visible, r.LabelPublicID, r.LabelName, r.LabelColor, r.OwnerMemberColor))
	resp.CanEdit = v.CanEdit
	if r.RecurrenceRule != nil {
		raw := json.RawMessage(r.RecurrenceRule)
		resp.RecurrenceRule = &raw
	}
	resp.RecurrenceEnd = dbtype.UnixSecondsFromNullTime(r.RecurrenceEnd)
	if r.RecurrenceExceptions != nil {
		raw := json.RawMessage(r.RecurrenceExceptions)
		resp.RecurrenceExceptions = &raw
	}
	resp.NotificationOffset = dbtype.PtrFromNullInt32(r.NotificationOffset)
	resp.UpdatedAt = dbtype.UnixSecondsFromNullTime(r.UpdatedAt)
	return resp
}
