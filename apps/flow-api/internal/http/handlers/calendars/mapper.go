package calendars

import (
	"encoding/json"

	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/generated/calendar"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/types"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/http/handlers/handlerutil"
	"github.com/libraz/nodate-flow/packages/go-shared/dbtype"
)

// scrubPrivateEvent redacts the free-text fields of a private event from
// every viewer except its owner. Calendar event visibility is the real
// access control: workspace membership only gates who may edit, so a
// private event's location, memo, and URL must never leak to co-members
// on any read path (single GET, per-calendar list, cross-calendar list,
// cross-workspace feed). This helper is the single source of truth for
// that rule; each read path routes the fields it projects through it and
// passes nil for the fields its DTO does not carry.
func scrubPrivateEvent(visibility string, ownerUserID, viewerUserID uint32, location, memo, url **string) {
	if visibility != string(calendar.CalendarEventsVisibilityPrivate) || ownerUserID == viewerUserID {
		return
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

// creatorPublicIDString renders a creator public_id to its UUID string,
// returning "" for the zero value. The creator JOIN is a LEFT JOIN, so a
// hard-deleted creator (CASCADE normally prevents this) scans as the zero
// PublicID; we suppress that rather than emit a zero UUID.
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
func (e rangeEvent) creatorAvatarURL() *string  { return dbtype.PtrFromNullString(e.r.CreatorAvatarUrl) }

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
	return dbtype.PtrFromNullString(e.r.CreatorAvatarUrl)
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
func (e fullEvent) creatorAvatarURL() *string  { return dbtype.PtrFromNullString(e.r.CreatorAvatarUrl) }

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
	scrubPrivateEvent(string(r.Visibility), r.OwnerUserID, viewerUserID, &resp.Location, &resp.Memo, &resp.URL)
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
	scrubPrivateEvent(string(r.Visibility), r.OwnerUserID, viewerUserID, &resp.Location, &resp.Memo, &resp.URL)
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
// what was just persisted.
func eventFromFullRow(r calendar.FindCalendarEventByPublicIdRow) EventResponse {
	resp := baseEventResponse(fullEvent{r})
	resp.Location = dbtype.PtrFromNullString(r.Location)
	resp.Memo = dbtype.PtrFromNullString(r.Memo)
	resp.URL = dbtype.PtrFromNullString(r.Url)
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
	resp.NotificationOffset = dbtype.PtrFromNullInt32(r.NotificationOffset)
	resp.UpdatedAt = dbtype.UnixSecondsFromNullTime(r.UpdatedAt)
	return resp
}
