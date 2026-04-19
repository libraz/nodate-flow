// Package calendars contains Huma operation handlers for the
// /workspaces/{wsId}/calendars endpoints.
package calendars

import (
	"database/sql"

	"github.com/danielgtaylor/huma/v2"

	"github.com/nodate-flow/nodate-flow/apps/flow-api/internal/db/generated"
	apierrors "github.com/nodate-flow/nodate-flow/apps/flow-api/internal/errors"
	"github.com/nodate-flow/nodate-flow/apps/flow-api/internal/http/handlers/handlerutil"
)

// Deps is the dependency bundle passed to each handler in this package.
type Deps struct {
	DB      *sql.DB
	Queries *generated.Queries
}

// httpErr converts an APIError Spec into a Huma status error.
func httpErr(spec *apierrors.Spec) error {
	return huma.NewError(spec.Status, spec.Code+": "+spec.Message)
}

// CalendarDTO is the public DTO for a calendar row.
type CalendarDTO struct {
	ID           string `json:"id" doc:"Calendar public id (UUID v7)"`
	Kind         string `json:"kind"`
	Name         string `json:"name"`
	Description  string `json:"description,omitempty"`
	Color        string `json:"color"`
	MemberColor  string `json:"memberColor,omitempty"`
	DisplayColor string `json:"displayColor,omitempty"`
	CoverURL     string `json:"coverUrl,omitempty"`
	Role         string `json:"role,omitempty"`
	Visible      bool   `json:"visible"`
	UpdatedAt    *int64 `json:"updatedAt,omitempty"`
	CreatedAt    int64  `json:"createdAt"`
}

// EventDTO is the public DTO for a calendar event.
type EventDTO struct {
	ID         string  `json:"id" doc:"Event public id (UUID v7)"`
	CalendarID string  `json:"calendarId"`
	Kind       string  `json:"kind"`
	Visibility string  `json:"visibility"`
	ShowAs     string  `json:"showAs"`
	Title      string  `json:"title"`
	AllDay     bool    `json:"allDay"`
	StartAt    string  `json:"startAt"`
	EndAt      string  `json:"endAt"`
	Timezone   string  `json:"timezone"`
	Location   string  `json:"location,omitempty"`
	Memo       string  `json:"memo,omitempty"`
	OwnerUserID string `json:"ownerUserId,omitempty"`
	BlockLabel string  `json:"blockLabel,omitempty"`
}

// ListCalendarsInput is the path for GET /workspaces/{wsId}/calendars.
type ListCalendarsInput struct {
	WsID string `path:"wsId"`
}

// ListCalendarsOutput is the response for GET /workspaces/{wsId}/calendars.
type ListCalendarsOutput struct {
	Body ListCalendarsOutputBody
}

// ListCalendarsOutputBody is the response body envelope for GET /workspaces/{wsId}/calendars.
type ListCalendarsOutputBody struct {
	Calendars []CalendarDTO `json:"calendars"`
}

// ListEventsInput is the query for GET /workspaces/{wsId}/calendar-events.
type ListEventsInput struct {
	WsID  string `path:"wsId"`
	Start string `query:"start" doc:"ISO date (YYYY-MM-DD)"`
	End   string `query:"end" doc:"ISO date (YYYY-MM-DD)"`
}

// ListEventsOutput is the response for GET /workspaces/{wsId}/calendar-events.
type ListEventsOutput struct {
	Body ListEventsOutputBody
}

// ListEventsOutputBody is the response body for GET /workspaces/{wsId}/calendar-events.
type ListEventsOutputBody struct {
	Events []EventDTO `json:"events"`
}

// CreateEventInput is the body for POST /workspaces/{wsId}/calendars/{calId}/events.
type CreateEventInput struct {
	WsID  string `path:"wsId"`
	CalID string `path:"calId"`
	Body  CreateEventInputBody
}

// CreateEventInputBody is the JSON body for creating an event.
type CreateEventInputBody struct {
	Title    string `json:"title" minLength:"1" maxLength:"500"`
	AllDay   bool   `json:"allDay"`
	StartAt  string `json:"startAt"`
	EndAt    string `json:"endAt"`
	Timezone string `json:"timezone"`
	Kind     string `json:"kind,omitempty"`
	ShowAs   string `json:"showAs,omitempty"`
	Location string `json:"location,omitempty" maxLength:"500"`
	Memo     string `json:"memo,omitempty" maxLength:"5000"`
}

// CreateEventOutput is the response for POST /workspaces/{wsId}/calendars/{calId}/events.
type CreateEventOutput struct {
	Body EventDTO
}

// PatchEventInput is the body for PATCH /workspaces/{wsId}/calendars/{calId}/events/{eventId}.
type PatchEventInput struct {
	WsID    string `path:"wsId"`
	CalID   string `path:"calId"`
	EventID string `path:"eventId"`
	Body    PatchEventInputBody
}

// PatchEventInputBody is the JSON body for patching an event.
type PatchEventInputBody struct {
	Title    *string `json:"title,omitempty" maxLength:"500"`
	AllDay   *bool   `json:"allDay,omitempty"`
	StartAt  *string `json:"startAt,omitempty"`
	EndAt    *string `json:"endAt,omitempty"`
	Timezone *string `json:"timezone,omitempty"`
	Kind     *string `json:"kind,omitempty"`
	ShowAs   *string `json:"showAs,omitempty"`
	Location *string `json:"location,omitempty" maxLength:"500"`
	Memo     *string `json:"memo,omitempty" maxLength:"5000"`
}

// PatchEventOutput is the response for PATCH /workspaces/{wsId}/calendars/{calId}/events/{eventId}.
type PatchEventOutput struct {
	Body EventDTO
}

// DeleteEventInput is the path for DELETE /workspaces/{wsId}/calendars/{calId}/events/{eventId}.
type DeleteEventInput struct {
	WsID    string `path:"wsId"`
	CalID   string `path:"calId"`
	EventID string `path:"eventId"`
}

// DeleteEventOutput is the response for DELETE.
type DeleteEventOutput struct {
	Body DeleteEventOutputBody
}

// DeleteEventOutputBody is the body for DELETE response.
type DeleteEventOutputBody struct {
	Ok bool `json:"ok"`
}

// nullStr delegates to handlerutil.NullStr.
var nullStr = handlerutil.NullStr

// nullTimeUnix delegates to handlerutil.NullTimeUnix (returns *int64, nil for NULL).
var nullTimeUnix = handlerutil.NullTimeUnix
