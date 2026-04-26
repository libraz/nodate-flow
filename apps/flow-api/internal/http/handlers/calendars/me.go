package calendars

import (
	"context"
	"database/sql"
	"encoding/json"

	"github.com/nodate-flow/nodate-flow/apps/flow-api/internal/db/generated/calendar"
	apierrors "github.com/nodate-flow/nodate-flow/apps/flow-api/internal/errors"
	"github.com/nodate-flow/nodate-flow/apps/flow-api/internal/http/middleware"
	"github.com/nodate-flow/nodate-flow/packages/go-shared/dbtype"
)

// MyInviteResponse is a single row in the /me/invites inbox. It carries
// enough context — workspace, calendar, event title, event window — for
// the inbox UI to render "You're invited to <event> in <workspace>"
// without fanning out per-invite requests. Only active, non-accepted,
// non-expired invites for the caller's primary email are returned.
type MyInviteResponse struct {
	ID                string  `json:"id"`
	EventPublicID     string  `json:"eventPublicId"`
	EventTitle        string  `json:"eventTitle"`
	EventStartAt      *int64  `json:"eventStartAt,omitempty"`
	EventEndAt        *int64  `json:"eventEndAt,omitempty"`
	EventAllDay       bool    `json:"eventAllDay"`
	EventLocation     *string `json:"eventLocation,omitempty"`
	CalendarPublicID  string  `json:"calendarPublicId"`
	CalendarName      string  `json:"calendarName"`
	WorkspacePublicID string  `json:"workspacePublicId"`
	WorkspaceName     string  `json:"workspaceName"`
	ExpiresAt         int64   `json:"expiresAt"`
	CreatedAt         int64   `json:"createdAt"`
}

// ListMyInvitesOutput wraps the inbox list. The plural "invites" key
// matches repo conventions; an empty inbox is rendered as `{invites: []}`.
type ListMyInvitesOutput struct {
	Body struct {
		Invites []MyInviteResponse `json:"invites"`
	}
}

// ListMyInvites returns every active, non-accepted, non-expired magic
// link invite addressed to the authenticated user's primary email,
// across every workspace. The query behind it is ListMyCalendarEventInvites
// which JOINs event + calendar + workspace metadata in a single trip.
//
// The actor's email is resolved from their user profile row; stub users
// without an email produce an empty inbox. No ACL check beyond auth: the
// invites were addressed to this email, and the user proved email
// control by signing in.
func ListMyInvites(deps Deps) func(context.Context, *struct{}) (*ListMyInvitesOutput, error) {
	return func(ctx context.Context, _ *struct{}) (*ListMyInvitesOutput, error) {
		actorID, ok := middleware.ActorFromContext(ctx)
		if !ok {
			return nil, httpErr(apierrors.AuthSessionRevoked)
		}
		profile, err := deps.Queries.FindUserProfileById(ctx, actorID)
		if err != nil {
			return nil, httpErr(apierrors.CalendarInviteListQueryInterrupted)
		}

		out := &ListMyInvitesOutput{}
		out.Body.Invites = []MyInviteResponse{}
		if profile.Email == "" {
			return out, nil
		}

		rows, err := deps.CalendarQueries.ListMyCalendarEventInvites(ctx, profile.Email)
		if err != nil {
			return nil, httpErr(apierrors.CalendarInviteListQueryInterrupted)
		}
		out.Body.Invites = make([]MyInviteResponse, 0, len(rows))
		for _, r := range rows {
			item := MyInviteResponse{
				ID:                r.PublicID.String(),
				EventPublicID:     r.EventPublicID.String(),
				EventTitle:        r.EventTitle,
				EventStartAt:      nullTimeUnixPtr(r.EventStartAt),
				EventEndAt:        nullTimeUnixPtr(r.EventEndAt),
				EventAllDay:       r.EventAllDay,
				CalendarPublicID:  r.CalendarPublicID.String(),
				CalendarName:      r.CalendarName,
				WorkspacePublicID: r.WorkspacePublicID.String(),
				WorkspaceName:     r.WorkspaceName,
				ExpiresAt:         r.ExpiresAt.Unix(),
				CreatedAt:         r.CreatedAt.Unix(),
			}
			item.EventLocation = dbtype.PtrFromNullString(r.EventLocation)
			out.Body.Invites = append(out.Body.Invites, item)
		}
		return out, nil
	}
}

// ListMyCalendarEventsInput is the query for GET /me/calendar-events.
// `start` and `end` accept either a date (YYYY-MM-DD) or an RFC 3339
// datetime, matching the per-workspace cross-calendar endpoint so the
// client can share parsing code. `tz` is currently reserved for a
// future client-hint parameter; the returned start_at / end_at are unix
// seconds (UTC) regardless.
type ListMyCalendarEventsInput struct {
	Start string `query:"start" required:"true" doc:"Range start (inclusive, YYYY-MM-DD or RFC3339)"`
	End   string `query:"end" required:"true" doc:"Range end (exclusive, YYYY-MM-DD or RFC3339)"`
}

// MyCalendarEventResponse is a single row in the cross-workspace
// /me/calendar-events response. It carries workspace context per row so
// the caller can group/filter client-side without a second round-trip
// per workspace.
type MyCalendarEventResponse struct {
	ID                   string           `json:"id"`
	CalendarID           string           `json:"calendarId"`
	WorkspaceID          string           `json:"workspaceId"`
	WorkspaceName        string           `json:"workspaceName"`
	OwnerUserID          string           `json:"ownerUserId"`
	AttendeeCount        int64            `json:"attendeeCount"`
	ViewerAttending      bool             `json:"viewerAttending"`
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

// ListMyCalendarEventsOutput is the response for GET /me/calendar-events.
type ListMyCalendarEventsOutput struct {
	Body struct {
		Events []MyCalendarEventResponse `json:"events"`
	}
}

// ListMyCalendarEvents handles GET /me/calendar-events?start=&end=. It
// returns every visible event across every calendar the authenticated
// user is subscribed to, in every workspace they are an active member
// of. Pairs with flow-api's GET /me/tasks-with-dates so the unified
// flow-web calendar renders with two cross-service requests instead of
// fanning out per-workspace.
func ListMyCalendarEvents(deps Deps) func(context.Context, *ListMyCalendarEventsInput) (*ListMyCalendarEventsOutput, error) {
	return func(ctx context.Context, input *ListMyCalendarEventsInput) (*ListMyCalendarEventsOutput, error) {
		actorID, ok := middleware.ActorFromContext(ctx)
		if !ok {
			return nil, httpErr(apierrors.AuthSessionRevoked)
		}

		startTime, err := parseFlexibleTime(input.Start)
		if err != nil {
			return nil, httpErr(apierrors.CalendarEventDateRangeUnparseable)
		}
		endTime, err := parseFlexibleTime(input.End)
		if err != nil {
			return nil, httpErr(apierrors.CalendarEventDateRangeUnparseable)
		}
		if !endTime.After(startTime) {
			return nil, httpErr(apierrors.CalendarEventDateRangeUnparseable)
		}

		rows, err := deps.CalendarQueries.ListMyCalendarEventsAcrossWorkspaces(ctx, calendar.ListMyCalendarEventsAcrossWorkspacesParams{
			UserID:  actorID,
			StartAt: sql.NullTime{Time: endTime, Valid: true},
			EndAt:   sql.NullTime{Time: startTime, Valid: true},
		})
		if err != nil {
			return nil, httpErr(apierrors.CalendarEventListQueryInterrupted)
		}

		recurringRows, err := deps.CalendarQueries.ListMyRecurringCalendarEventsAcrossWorkspaces(ctx, calendar.ListMyRecurringCalendarEventsAcrossWorkspacesParams{
			UserID:        actorID,
			StartAt:       sql.NullTime{Time: endTime, Valid: true},
			RecurrenceEnd: sql.NullTime{Time: startTime, Valid: true},
		})
		if err != nil {
			return nil, httpErr(apierrors.CalendarEventListQueryInterrupted)
		}

		out := &ListMyCalendarEventsOutput{}
		out.Body.Events = make([]MyCalendarEventResponse, 0, len(rows)+len(recurringRows))

		for _, r := range rows {
			resp := MyCalendarEventResponse{
				ID:              r.PublicID.String(),
				CalendarID:      r.CalendarPublicID.String(),
				WorkspaceID:     r.WorkspacePublicID.String(),
				WorkspaceName:   r.WorkspaceName,
				OwnerUserID:     r.OwnerPublicID.String(),
				AttendeeCount:   r.AttendeeCount,
				ViewerAttending: r.ViewerAttending,
				Kind:            string(r.Kind),
				Visibility:      string(r.Visibility),
				ShowAs:          string(r.ShowAs),
				Title:           r.Title,
				AllDay:          r.AllDay,
				StartAt:         nullTimeUnixPtr(r.StartAt),
				EndAt:           nullTimeUnixPtr(r.EndAt),
				Timezone:        r.Timezone,
				CreatedAt:       r.CreatedAt.Unix(),
			}
			resp.Location = dbtype.PtrFromNullString(r.Location)
			resp.BlockLabel = dbtype.PtrFromNullString(r.BlockLabel)
			resp.UpdatedAt = dbtype.UnixSecondsFromNullTime(r.UpdatedAt)
			out.Body.Events = append(out.Body.Events, resp)
		}

		for _, r := range recurringRows {
			resp := MyCalendarEventResponse{
				ID:              r.PublicID.String(),
				CalendarID:      r.CalendarPublicID.String(),
				WorkspaceID:     r.WorkspacePublicID.String(),
				WorkspaceName:   r.WorkspaceName,
				OwnerUserID:     r.OwnerPublicID.String(),
				AttendeeCount:   r.AttendeeCount,
				ViewerAttending: r.ViewerAttending,
				Kind:            string(r.Kind),
				Visibility:      string(r.Visibility),
				ShowAs:          string(r.ShowAs),
				Title:           r.Title,
				AllDay:          r.AllDay,
				StartAt:         nullTimeUnixPtr(r.StartAt),
				EndAt:           nullTimeUnixPtr(r.EndAt),
				Timezone:        r.Timezone,
				CreatedAt:       r.CreatedAt.Unix(),
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
			out.Body.Events = append(out.Body.Events, resp)
		}

		return out, nil
	}
}
